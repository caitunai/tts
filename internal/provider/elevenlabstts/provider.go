package elevenlabstts

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultProviderName = "elevenlabs_tts"
	defaultModel        = "eleven_flash_v2_5"
	defaultOutputFormat = "opus_48000_32"
	defaultStability    = 0.5
	defaultSimilarity   = 0.8
	defaultSpeed        = 1.0
)

// Config configures an ElevenLabs realtime WebSocket TTS provider.
type Config struct {
	Name string

	Endpoint string
	APIKey   string

	Model           string
	OutputFormat    string
	DefaultVoice    string
	DefaultLanguage string

	Stability       float64
	SimilarityBoost float64
	Speed           float64
}

// Provider adapts ElevenLabs realtime WebSocket TTS to the TTS Provider
// interface.
type Provider struct {
	name string

	endpoint string
	apiKey   string

	model           string
	outputFormat    string
	defaultVoice    string
	defaultLanguage string

	stability       float64
	similarityBoost float64
	speed           float64
}

// NewProvider creates an ElevenLabs realtime TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "elevenlabs tts endpoint is required",
			Provider: cfg.Name,
		}
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.OutputFormat == "" {
		cfg.OutputFormat = defaultOutputFormat
	}
	if cfg.Stability == 0 {
		cfg.Stability = defaultStability
	}
	if cfg.SimilarityBoost == 0 {
		cfg.SimilarityBoost = defaultSimilarity
	}
	if cfg.Speed == 0 {
		cfg.Speed = defaultSpeed
	}

	return &Provider{
		name:            cfg.Name,
		endpoint:        cfg.Endpoint,
		apiKey:          cfg.APIKey,
		model:           cfg.Model,
		outputFormat:    cfg.OutputFormat,
		defaultVoice:    cfg.DefaultVoice,
		defaultLanguage: cfg.DefaultLanguage,
		stability:       cfg.Stability,
		similarityBoost: cfg.SimilarityBoost,
		speed:           cfg.Speed,
	}, nil
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) Capabilities(context.Context) (*tts.ProviderCapabilities, error) {
	caps := &tts.ProviderCapabilities{
		Name:                    p.name,
		Transports:              []tts.TransportType{tts.TransportWebSocket},
		SupportsStreaming:       true,
		SupportsAppendText:      true,
		SupportsSpeed:           true,
		SupportsSegmentEndEvent: true,
		SupportsOggOpusOutput:   true,
		OutputCodecs:            []audio.Codec{audio.CodecOpus},
		OutputContainers:        []audio.Container{audio.ContainerOgg},
		OutputSampleRates:       []int{audio.OpusSampleRate},
		OutputChannels:          []int{audio.DefaultChannels},
		Languages: []tts.LanguageInfo{
			{Code: "auto", Name: "Auto"},
			{Code: "zh", Name: "Chinese"},
			{Code: "en", Name: "English"},
			{Code: "ja", Name: "Japanese"},
			{Code: "ko", Name: "Korean"},
			{Code: "de", Name: "German"},
			{Code: "fr", Name: "French"},
			{Code: "es", Name: "Spanish"},
		},
	}
	if p.defaultVoice != "" {
		caps.Voices = []tts.VoiceInfo{{ID: p.defaultVoice, Name: p.defaultVoice}}
	}
	return caps, nil
}

func (p *Provider) SynthesizeOnce(context.Context, *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	return nil, &tts.Error{
		Code:     tts.ErrUnsupportedFeature,
		Message:  "elevenlabs tts provider only supports sessions",
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

	voice := valueOrDefault(req.Voice, p.defaultVoice)
	realtimeURL, err := p.realtimeURL(voice)
	if err != nil {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  err.Error(),
			Provider: p.name,
			Cause:    err,
		}
	}

	header := http.Header{}
	if p.apiKey != "" {
		header.Set("xi-api-key", p.apiKey)
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, realtimeURL, header)
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
		apiKey:          p.apiKey,
		defaultVoice:    p.defaultVoice,
		defaultLanguage: p.defaultLanguage,
		initialVoice:    req.Voice,
		initialLanguage: req.Language,
		stability:       p.stability,
		similarityBoost: p.similarityBoost,
		speed:           p.speed,
	})
	if err := session.start(ctx); err != nil {
		_ = session.Close()
		return nil, err
	}
	go session.readLoop()
	return session, nil
}

func (p *Provider) realtimeURL(voice string) (string, error) {
	if voice == "" {
		return "", fmt.Errorf("elevenlabs voice is required")
	}

	raw := strings.ReplaceAll(p.endpoint, ":voice_id", url.PathEscape(voice))
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	if p.outputFormat != "" {
		query.Set("output_format", p.outputFormat)
	}
	if p.model != "" {
		query.Set("model_id", p.model)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func newEventID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func textWithTrailingSpace(text string) string {
	if text == "" || strings.HasSuffix(text, " ") {
		return text
	}
	return text + " "
}

type realtimeSessionConfig struct {
	id       string
	provider string
	conn     *websocket.Conn
	apiKey   string

	defaultVoice    string
	defaultLanguage string
	initialVoice    string
	initialLanguage string

	stability       float64
	similarityBoost float64
	speed           float64
}

type realtimeSession struct {
	id       string
	provider string
	conn     *websocket.Conn
	apiKey   string

	defaultVoice    string
	defaultLanguage string
	initialVoice    string
	initialLanguage string

	stability       float64
	similarityBoost float64
	speed           float64

	writeMu sync.Mutex

	mu               sync.Mutex
	closed           bool
	finishing        bool
	endSent          bool
	currentSegmentID string

	events    chan *tts.ProviderEvent
	closeOnce sync.Once
	done      chan struct{}
}

func newRealtimeSession(cfg realtimeSessionConfig) *realtimeSession {
	return &realtimeSession{
		id:              cfg.id,
		provider:        cfg.provider,
		conn:            cfg.conn,
		apiKey:          cfg.apiKey,
		defaultVoice:    cfg.defaultVoice,
		defaultLanguage: cfg.defaultLanguage,
		initialVoice:    cfg.initialVoice,
		initialLanguage: cfg.initialLanguage,
		stability:       cfg.stability,
		similarityBoost: cfg.similarityBoost,
		speed:           cfg.speed,
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

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return &tts.Error{
			Code:      tts.ErrSessionClosed,
			Message:   "elevenlabs tts session is closed",
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
			Message:   "elevenlabs tts session is closed",
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segment.SegmentID,
		}
	}

	if err := s.writeJSON(ctx, speakRequest{
		Text:                 textWithTrailingSpace(segment.Text),
		TryTriggerGeneration: true,
		Flush:                segment.IsLast,
	}); err != nil {
		return s.writeError(err, segment.SegmentID)
	}
	if segment.IsLast {
		if err := s.sendEnd(ctx); err != nil {
			return s.writeError(err, segment.SegmentID)
		}
		return nil
	}
	s.endSegment(segment.SegmentID, nil)
	return nil
}

func (s *realtimeSession) Finish(ctx context.Context) error {
	if err := s.sendEnd(ctx); err != nil {
		return s.writeError(err, "")
	}
	return nil
}

func (s *realtimeSession) sendEnd(ctx context.Context) error {
	s.mu.Lock()
	if s.endSent {
		s.mu.Unlock()
		return nil
	}
	s.finishing = true
	s.endSent = true
	s.mu.Unlock()
	return s.writeJSON(ctx, speakRequest{Text: ""})
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

func (s *realtimeSession) start(ctx context.Context) error {
	if err := s.writeJSON(ctx, startRequest{
		Text: " ",
		VoiceSettings: &voiceSettings{
			Speed:           s.speed,
			Stability:       s.stability,
			SimilarityBoost: s.similarityBoost,
		},
		XiAPIKey: s.apiKey,
	}); err != nil {
		return s.writeError(err, "")
	}
	return nil
}

func (s *realtimeSession) readLoop() {
	defer close(s.events)
	defer func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	}()

	for {
		var resp speakResponse
		if err := s.conn.ReadJSON(&resp); err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if s.isEndSent() && s.segmentID() == "" {
				s.emit(&tts.ProviderEvent{
					Type:      tts.ProviderEventSessionEnd,
					Provider:  s.provider,
					SessionID: s.id,
				})
				return
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

		if s.handleResponse(&resp) {
			return
		}
	}
}

func (s *realtimeSession) handleResponse(resp *speakResponse) bool {
	if resp == nil {
		return false
	}
	segmentID := s.segmentID()
	if resp.Audio != "" {
		data, err := base64.StdEncoding.DecodeString(resp.Audio)
		if err != nil {
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: segmentID,
				RawMeta:   resp.meta(),
				Error: &tts.Error{
					Code:      tts.ErrAudioDecodeFailed,
					Message:   fmt.Sprintf("decode elevenlabs audio: %v", err),
					Provider:  s.provider,
					SessionID: s.id,
					SegmentID: segmentID,
					Cause:     err,
				},
			})
			return false
		}
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventAudio,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segmentID,
			RawMeta:   resp.meta(),
			Audio: &tts.ProviderAudioChunk{
				Codec:      audio.CodecOpus,
				Container:  audio.ContainerOgg,
				SampleRate: audio.OpusSampleRate,
				Channels:   audio.DefaultChannels,
				Data:       data,
			},
		})
	}
	if !resp.IsFinal {
		return false
	}

	endedSegmentID := s.clearSegmentID()
	if endedSegmentID != "" {
		s.endSegment(endedSegmentID, resp.meta())
		return false
	}
	if !s.isFinishing() {
		return false
	}

	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSessionEnd,
		Provider:  s.provider,
		SessionID: s.id,
		RawMeta:   resp.meta(),
	})
	return true
}

func (s *realtimeSession) endSegment(segmentID string, meta map[string]any) {
	if segmentID == "" {
		return
	}
	s.mu.Lock()
	if s.currentSegmentID == segmentID {
		s.currentSegmentID = ""
	}
	s.mu.Unlock()
	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentEnd,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		RawMeta:   meta,
	})
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
	return s.conn.WriteJSON(value)
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

func (s *realtimeSession) isFinishing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishing
}

func (s *realtimeSession) isEndSent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endSent
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

type startRequest struct {
	Text          string         `json:"text"`
	VoiceSettings *voiceSettings `json:"voice_settings"`
	XiAPIKey      string         `json:"xi_api_key"`
}

type voiceSettings struct {
	Speed           float64 `json:"speed"`
	Stability       float64 `json:"stability"`
	SimilarityBoost float64 `json:"similarity_boost"`
}

type speakRequest struct {
	Text                 string `json:"text"`
	TryTriggerGeneration bool   `json:"try_trigger_generation,omitempty"`
	Flush                bool   `json:"flush,omitempty"`
}

type speakResponse struct {
	NormalizedAlignment *alignment `json:"normalizedAlignment,omitempty"`
	Alignment           *alignment `json:"alignment,omitempty"`
	Audio               string     `json:"audio"`
	IsFinal             bool       `json:"isFinal"`
}

func (r *speakResponse) meta() map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"is_final":                 r.IsFinal,
		"has_alignment":            r.Alignment != nil,
		"has_normalized_alignment": r.NormalizedAlignment != nil,
	}
}

type alignment struct {
	CharStartTimesMs []int    `json:"charStartTimesMs"`
	CharDurationsMs  []int    `json:"charDurationsMs"`
	Chars            []string `json:"chars"`
}
