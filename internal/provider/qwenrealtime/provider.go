package qwenrealtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultProviderName = "qwen_realtime_tts"
	defaultModel        = "qwen3-tts-instruct-flash-realtime"

	Chinese    = "Chinese"
	English    = "English"
	German     = "German"
	Italian    = "Italian"
	Portuguese = "Portuguese"
	Spanish    = "Spanish"
	Japanese   = "Japanese"
	Korean     = "Korean"
	French     = "French"
	Russian    = "Russian"
	Auto       = "Auto"
)

// Config configures an Alibaba Cloud Qwen realtime WebSocket TTS provider.
type Config struct {
	Name string

	Endpoint string
	Token    string

	Model           string
	DefaultVoice    string
	DefaultLanguage string
	SampleRate      int
}

// Provider adapts Qwen's realtime WebSocket TTS API to the TTS Provider
// interface.
type Provider struct {
	name string

	endpoint string
	token    string

	model           string
	defaultVoice    string
	defaultLanguage string
	sampleRate      int
}

// NewProvider creates a Qwen realtime WebSocket TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen realtime tts endpoint is required",
			Provider: cfg.Name,
		}
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = audio.OpusSampleRate
	}
	if cfg.SampleRate != audio.OpusSampleRate {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen realtime opus sample rate must be 48000",
			Provider: cfg.Name,
		}
	}

	return &Provider{
		name:            cfg.Name,
		endpoint:        cfg.Endpoint,
		token:           cfg.Token,
		model:           cfg.Model,
		defaultVoice:    cfg.DefaultVoice,
		defaultLanguage: cfg.DefaultLanguage,
		sampleRate:      cfg.SampleRate,
	}, nil
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) Capabilities(context.Context) (*tts.ProviderCapabilities, error) {
	caps := &tts.ProviderCapabilities{
		Name:                         p.name,
		Transports:                   []tts.TransportType{tts.TransportWebSocket},
		SupportsStreaming:            true,
		SupportsAppendText:           true,
		SupportsGuidanceText:         true,
		SupportsSegmentLevelGuidance: true,
		SupportsSegmentEndEvent:      true,
		SupportsOggOpusOutput:        true,
		OutputCodecs:                 []audio.Codec{audio.CodecOpus},
		OutputContainers:             []audio.Container{audio.ContainerOgg},
		OutputSampleRates:            []int{p.sampleRate},
		OutputChannels:               []int{audio.DefaultChannels},
	}
	return caps, nil
}

func (p *Provider) SynthesizeOnce(context.Context, *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	return nil, &tts.Error{
		Code:     tts.ErrUnsupportedFeature,
		Message:  "qwen realtime tts provider only supports sessions",
		Provider: p.name,
	}
}

func (p *Provider) OpenSession(ctx context.Context, req *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
	if req == nil {
		return nil, &tts.Error{
			Code:     tts.ErrInternal,
			Message:  "open session request is nil",
			Provider: p.name,
		}
	}

	header := http.Header{}
	if p.token != "" {
		header.Set("Authorization", "Bearer "+p.token)
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, p.realtimeURL(), header)
	if err != nil {
		if resp != nil && resp.Body != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			err = fmt.Errorf("%w: %s", err, string(body))
		}
		return nil, &tts.Error{
			Code:      tts.ErrProviderUnavailable,
			Message:   err.Error(),
			Provider:  p.name,
			Cause:     err,
			Retryable: true,
		}
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = newEventID("sess")
	}
	session := newRealtimeSession(realtimeSessionConfig{
		id:              sessionID,
		provider:        p.name,
		conn:            conn,
		defaultVoice:    p.defaultVoice,
		defaultLanguage: p.defaultLanguage,
		sampleRate:      p.sampleRate,
	})
	go session.readLoop()
	if err := session.updateSession(ctx, sessionUpdateOptions{
		voice:        req.Voice,
		language:     req.Language,
		instructions: req.GuidanceText,
	}); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func (p *Provider) realtimeURL() string {
	if p.model == "" || !shouldAppendModel(p.endpoint) {
		return p.endpoint
	}
	return p.endpoint + p.model
}

func rewriteLang(lang string) string {
	switch strings.ToLower(lang) {
	case "zh", "chinese":
		return Chinese
	case "en", "english":
		return English
	case "de", "german":
		return German
	case "it", "italian":
		return Italian
	case "pt", "portuguese":
		return Portuguese
	case "es", "spanish":
		return Spanish
	case "ja", "japanese":
		return Japanese
	case "ko", "korean":
		return Korean
	case "fr", "french":
		return French
	case "ru", "russian":
		return Russian
	default:
		return Auto
	}
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func shouldAppendModel(endpoint string) bool {
	return strings.HasSuffix(endpoint, "/") || strings.HasSuffix(endpoint, "=")
}

func newEventID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

type realtimeSessionConfig struct {
	id       string
	provider string
	conn     *websocket.Conn

	defaultVoice    string
	defaultLanguage string
	sampleRate      int
}

type realtimeSession struct {
	id       string
	provider string
	conn     *websocket.Conn

	defaultVoice    string
	defaultLanguage string
	sampleRate      int

	writeMu sync.Mutex

	mu               sync.Mutex
	closed           bool
	currentSegmentID string
	currentVoice     string
	currentLanguage  string
	currentStyle     string

	events    chan *tts.ProviderEvent
	closeOnce sync.Once
	done      chan struct{}
	counter   atomic.Int64
}

type sessionUpdateOptions struct {
	voice        string
	language     string
	instructions string
}

func newRealtimeSession(cfg realtimeSessionConfig) *realtimeSession {
	return &realtimeSession{
		id:              cfg.id,
		provider:        cfg.provider,
		conn:            cfg.conn,
		defaultVoice:    cfg.defaultVoice,
		defaultLanguage: cfg.defaultLanguage,
		sampleRate:      cfg.sampleRate,
		events:          make(chan *tts.ProviderEvent, 32),
		done:            make(chan struct{}),
	}
}

func (s *realtimeSession) ID() string {
	return s.id
}

func (s *realtimeSession) AppendText(ctx context.Context, segment *tts.ProviderSegmentRequest) error {
	if segment == nil {
		return &tts.Error{
			Code:      tts.ErrInternal,
			Message:   "segment request is nil",
			Provider:  s.provider,
			SessionID: s.id,
		}
	}

	currentVoice, currentLanguage, currentStyle := s.currentSessionOptions()
	voice := valueOrDefault(segment.Voice, valueOrDefault(currentVoice, s.defaultVoice))
	language := valueOrDefault(segment.Language, valueOrDefault(currentLanguage, s.defaultLanguage))
	style := currentStyle
	if segment.GuidanceText != "" {
		style = segment.GuidanceText
	}

	if s.needsSessionUpdate(voice, language, style) {
		if err := s.updateSession(ctx, sessionUpdateOptions{
			voice:        voice,
			language:     language,
			instructions: style,
		}); err != nil {
			return err
		}
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return &tts.Error{
			Code:      tts.ErrSessionClosed,
			Message:   "qwen realtime tts session is closed",
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segment.SegmentID,
		}
	}
	s.currentSegmentID = segment.SegmentID
	s.mu.Unlock()

	if !s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentStart,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segment.SegmentID,
	}) {
		return &tts.Error{
			Code:      tts.ErrSessionClosed,
			Message:   "qwen realtime tts session is closed",
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segment.SegmentID,
		}
	}

	if err := s.writeJSON(ctx, textMessage{
		EventID: s.nextEventID("append"),
		Type:    "input_text_buffer.append",
		Text:    segment.Text,
	}); err != nil {
		return s.writeError(err, segment.SegmentID)
	}
	if err := s.writeJSON(ctx, textMessage{
		EventID: s.nextEventID("commit"),
		Type:    "input_text_buffer.commit",
	}); err != nil {
		return s.writeError(err, segment.SegmentID)
	}
	return nil
}

func (s *realtimeSession) Finish(ctx context.Context) error {
	if err := s.writeJSON(ctx, textMessage{
		EventID: s.nextEventID("finish"),
		Type:    "session.finish",
	}); err != nil {
		return s.writeError(err, "")
	}
	return nil
}

func (s *realtimeSession) Events() <-chan *tts.ProviderEvent {
	return s.events
}

func (s *realtimeSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		err = s.conn.Close()
	})
	return err
}

func (s *realtimeSession) readLoop() {
	defer close(s.events)
	defer func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	}()

	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				Error: &tts.Error{
					Code:      tts.ErrProviderUnavailable,
					Message:   err.Error(),
					Provider:  s.provider,
					SessionID: s.id,
					Cause:     err,
					Retryable: true,
				},
			})
			return
		}

		var resp realtimeMessage
		if err := json.Unmarshal(message, &resp); err != nil {
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				Error: &tts.Error{
					Code:      tts.ErrProviderUnavailable,
					Message:   fmt.Sprintf("decode qwen realtime message: %v", err),
					Provider:  s.provider,
					SessionID: s.id,
					Cause:     err,
				},
			})
			continue
		}

		switch resp.Type {
		case "response.audio.delta":
			s.handleAudioDelta(resp.Delta)
		case "response.done":
			s.handleResponseDone()
		case "session.finished":
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventSessionEnd,
				Provider:  s.provider,
				SessionID: s.id,
			})
			return
		}
	}
}

func (s *realtimeSession) handleAudioDelta(delta string) {
	if delta == "" {
		return
	}
	data, err := base64.StdEncoding.DecodeString(delta)
	if err != nil {
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventError,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: s.segmentID(),
			Error: &tts.Error{
				Code:      tts.ErrAudioDecodeFailed,
				Message:   fmt.Sprintf("decode qwen realtime audio: %v", err),
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: s.segmentID(),
				Cause:     err,
			},
		})
		return
	}

	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventAudio,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: s.segmentID(),
		Audio: &tts.ProviderAudioChunk{
			Codec:      audio.CodecOpus,
			Container:  audio.ContainerOgg,
			SampleRate: s.sampleRate,
			Channels:   audio.DefaultChannels,
			Data:       data,
		},
	})
}

func (s *realtimeSession) handleResponseDone() {
	segmentID := s.clearSegmentID()
	if segmentID == "" {
		return
	}
	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentEnd,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
	})
}

func (s *realtimeSession) updateSession(ctx context.Context, opts sessionUpdateOptions) error {
	voice := valueOrDefault(opts.voice, s.defaultVoice)
	language := valueOrDefault(opts.language, s.defaultLanguage)
	style := opts.instructions

	req := sessionUpdate{
		EventID: s.nextEventID("session"),
		Type:    "session.update",
		Session: &realtimeSessionPayload{
			Mode:                 "commit",
			Voice:                voice,
			LanguageType:         rewriteLang(language),
			ResponseFormat:       "opus",
			SampleRate:           s.sampleRate,
			Instructions:         style,
			OptimizeInstructions: false,
		},
	}
	if err := s.writeJSON(ctx, req); err != nil {
		return s.writeError(err, "")
	}

	s.mu.Lock()
	s.currentVoice = voice
	s.currentLanguage = language
	s.currentStyle = style
	s.mu.Unlock()
	return nil
}

func (s *realtimeSession) needsSessionUpdate(voice, language, style string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return voice != "" && voice != s.currentVoice ||
		language != "" && language != s.currentLanguage ||
		style != s.currentStyle
}

func (s *realtimeSession) currentSessionOptions() (string, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentVoice, s.currentLanguage, s.currentStyle
}

func (s *realtimeSession) writeJSON(ctx context.Context, value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if deadline, ok := ctx.Deadline(); ok {
		if err := s.conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer func() {
			_ = s.conn.SetWriteDeadline(time.Time{})
		}()
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, data)
}

func (s *realtimeSession) emit(event *tts.ProviderEvent) bool {
	select {
	case <-s.done:
		return false
	case s.events <- event:
		return true
	}
}

func (s *realtimeSession) segmentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentSegmentID
}

func (s *realtimeSession) clearSegmentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	segmentID := s.currentSegmentID
	s.currentSegmentID = ""
	return segmentID
}

func (s *realtimeSession) nextEventID(kind string) string {
	return fmt.Sprintf("%s_%s_%d", s.id, kind, s.counter.Add(1))
}

func (s *realtimeSession) writeError(err error, segmentID string) *tts.Error {
	return &tts.Error{
		Code:      tts.ErrProviderUnavailable,
		Message:   err.Error(),
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		Cause:     err,
		Retryable: true,
	}
}

type sessionUpdate struct {
	Session *realtimeSessionPayload `json:"session"`
	EventID string                  `json:"event_id"`
	Type    string                  `json:"type"`
}

type realtimeSessionPayload struct {
	ID                   string `json:"id,omitempty"`
	Object               string `json:"object,omitempty"`
	Model                string `json:"model,omitempty"`
	Mode                 string `json:"mode"`
	Voice                string `json:"voice"`
	LanguageType         string `json:"language_type"`
	ResponseFormat       string `json:"response_format"`
	Instructions         string `json:"instructions"`
	SampleRate           int    `json:"sample_rate"`
	OptimizeInstructions bool   `json:"optimize_instructions"`
}

type textMessage struct {
	EventID string `json:"event_id"`
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
}

type realtimeMessage struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
}
