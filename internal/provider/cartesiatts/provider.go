package cartesiatts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultProviderName = "cartesia_tts"
	defaultEndpoint     = "wss://api.cartesia.ai/tts/websocket"
	defaultVersion      = "2026-03-01"
	defaultModel        = "sonic-3.5"
	defaultEncoding     = "pcm_s16le"
	defaultContainer    = "raw"
	defaultSegmentID    = "seg"
	defaultContextID    = "ctx"
)

var supportedSampleRates = []int{
	audio.DefaultSampleRate,
	8000,
	22050,
	24000,
	44100,
	48000,
}

// Config configures Cartesia's WebSocket TTS provider.
type Config struct {
	Name string

	Endpoint    string
	APIKey      string
	AccessToken string
	Version     string

	Model           string
	DefaultVoice    string
	DefaultLanguage string
	SampleRate      int

	MaxBufferDelayMS        int
	AddTimestamps           bool
	AddPhonemeTimestamps    bool
	UseNormalizedTimestamps bool
	PronunciationDictID     string

	DefaultSpeed   float64
	DefaultVolume  float64
	DefaultEmotion string
}

// Provider adapts Cartesia's realtime WebSocket TTS API to the TTS Provider
// interface.
type Provider struct {
	name string

	endpoint    string
	apiKey      string
	accessToken string
	version     string

	model           string
	defaultVoice    string
	defaultLanguage string
	sampleRate      int

	maxBufferDelayMS        int
	addTimestamps           bool
	addPhonemeTimestamps    bool
	useNormalizedTimestamps bool
	pronunciationDictID     string

	defaultSpeed   float64
	defaultVolume  float64
	defaultEmotion string
}

// NewProvider creates a Cartesia TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	if cfg.Version == "" {
		cfg.Version = defaultVersion
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = audio.DefaultSampleRate
	}
	if !isSupportedSampleRate(cfg.SampleRate) {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  fmt.Sprintf("cartesia sample rate %d is not supported", cfg.SampleRate),
			Provider: cfg.Name,
		}
	}
	if cfg.MaxBufferDelayMS < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "cartesia max buffer delay must be non-negative",
			Provider: cfg.Name,
		}
	}

	return &Provider{
		name:                    cfg.Name,
		endpoint:                cfg.Endpoint,
		apiKey:                  cfg.APIKey,
		accessToken:             cfg.AccessToken,
		version:                 cfg.Version,
		model:                   cfg.Model,
		defaultVoice:            cfg.DefaultVoice,
		defaultLanguage:         cfg.DefaultLanguage,
		sampleRate:              cfg.SampleRate,
		maxBufferDelayMS:        cfg.MaxBufferDelayMS,
		addTimestamps:           cfg.AddTimestamps,
		addPhonemeTimestamps:    cfg.AddPhonemeTimestamps,
		useNormalizedTimestamps: cfg.UseNormalizedTimestamps,
		pronunciationDictID:     cfg.PronunciationDictID,
		defaultSpeed:            cfg.DefaultSpeed,
		defaultVolume:           cfg.DefaultVolume,
		defaultEmotion:          cfg.DefaultEmotion,
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
		SupportsEmotion:         true,
		SupportsSpeed:           true,
		SupportsVolume:          true,
		SupportsSegmentEndEvent: true,
		SupportsPCMOutput:       true,
		OutputCodecs:            []audio.Codec{audio.CodecPCM},
		OutputContainers:        []audio.Container{audio.ContainerRaw},
		OutputSampleRates:       append([]int(nil), supportedSampleRates...),
		OutputChannels:          []int{audio.DefaultChannels},
	}
	return caps, nil
}

func (p *Provider) SynthesizeOnce(context.Context, *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	return nil, &tts.Error{
		Code:     tts.ErrUnsupportedFeature,
		Message:  "cartesia tts provider only supports websocket sessions",
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

	realtimeURL, err := p.realtimeURL()
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
		header.Set("X-API-Key", p.apiKey)
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
		sessionID = newID("sess")
	}
	sampleRate := p.sampleRateFor(req.Output)
	session := newRealtimeSession(realtimeSessionConfig{
		id:                      sessionID,
		provider:                p.name,
		conn:                    conn,
		model:                   p.model,
		defaultVoice:            p.defaultVoice,
		initialVoice:            req.Voice,
		defaultLanguage:         p.defaultLanguage,
		initialLanguage:         req.Language,
		sampleRate:              sampleRate,
		maxBufferDelayMS:        p.maxBufferDelayMS,
		addTimestamps:           p.addTimestamps,
		addPhonemeTimestamps:    p.addPhonemeTimestamps,
		useNormalizedTimestamps: p.useNormalizedTimestamps,
		pronunciationDictID:     p.pronunciationDictID,
		defaultSpeed:            p.defaultSpeed,
		defaultVolume:           p.defaultVolume,
		defaultEmotion:          p.defaultEmotion,
	})
	go session.readLoop()
	return session, nil
}

func (p *Provider) realtimeURL() (string, error) {
	parsed, err := url.Parse(p.endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	if p.version != "" {
		query.Set("cartesia_version", p.version)
	}
	if p.accessToken != "" {
		query.Set("access_token", p.accessToken)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (p *Provider) sampleRateFor(output audio.OutputConfig) int {
	if isSupportedSampleRate(output.SampleRate) {
		return output.SampleRate
	}
	return p.sampleRate
}

type realtimeSessionConfig struct {
	id       string
	provider string
	conn     *websocket.Conn

	model           string
	defaultVoice    string
	initialVoice    string
	defaultLanguage string
	initialLanguage string
	sampleRate      int

	maxBufferDelayMS        int
	addTimestamps           bool
	addPhonemeTimestamps    bool
	useNormalizedTimestamps bool
	pronunciationDictID     string

	defaultSpeed   float64
	defaultVolume  float64
	defaultEmotion string
}

type realtimeSession struct {
	id       string
	provider string
	conn     *websocket.Conn

	model           string
	defaultVoice    string
	initialVoice    string
	defaultLanguage string
	initialLanguage string
	sampleRate      int

	maxBufferDelayMS        int
	addTimestamps           bool
	addPhonemeTimestamps    bool
	useNormalizedTimestamps bool
	pronunciationDictID     string

	defaultSpeed   float64
	defaultVolume  float64
	defaultEmotion string

	writeMu sync.Mutex

	mu        sync.Mutex
	closed    bool
	finishing bool
	contexts  map[string]string

	events    chan *tts.ProviderEvent
	closeOnce sync.Once
	done      chan struct{}
}

func newRealtimeSession(cfg realtimeSessionConfig) *realtimeSession {
	return &realtimeSession{
		id:                      cfg.id,
		provider:                cfg.provider,
		conn:                    cfg.conn,
		model:                   cfg.model,
		defaultVoice:            cfg.defaultVoice,
		initialVoice:            cfg.initialVoice,
		defaultLanguage:         cfg.defaultLanguage,
		initialLanguage:         cfg.initialLanguage,
		sampleRate:              cfg.sampleRate,
		maxBufferDelayMS:        cfg.maxBufferDelayMS,
		addTimestamps:           cfg.addTimestamps,
		addPhonemeTimestamps:    cfg.addPhonemeTimestamps,
		useNormalizedTimestamps: cfg.useNormalizedTimestamps,
		pronunciationDictID:     cfg.pronunciationDictID,
		defaultSpeed:            cfg.defaultSpeed,
		defaultVolume:           cfg.defaultVolume,
		defaultEmotion:          cfg.defaultEmotion,
		contexts:                make(map[string]string),
		events:                  make(chan *tts.ProviderEvent, 32),
		done:                    make(chan struct{}),
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

	segmentID := segment.SegmentID
	if segmentID == "" {
		segmentID = newID(defaultSegmentID)
	}
	voice := valueOrDefault(segment.Voice, valueOrDefault(s.initialVoice, s.defaultVoice))
	if voice == "" {
		return &tts.Error{
			Code:      tts.ErrUnsupportedVoice,
			Message:   "cartesia voice is required",
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segmentID,
		}
	}
	language := valueOrDefault(segment.Language, valueOrDefault(s.initialLanguage, s.defaultLanguage))
	contextID := s.contextID(segmentID)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return sessionClosedError(s.provider, s.id, segmentID)
	}
	s.contexts[contextID] = segmentID
	s.mu.Unlock()

	if !s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentStart,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
	}) {
		return sessionClosedError(s.provider, s.id, segmentID)
	}

	request := generationRequest{
		ModelID:    s.model,
		Transcript: segment.Text,
		Voice: voicePayload{
			Mode: "id",
			ID:   voice,
		},
		OutputFormat: outputFormatPayload{
			Container:  defaultContainer,
			Encoding:   defaultEncoding,
			SampleRate: s.sampleRate,
		},
		Language:                language,
		ContextID:               contextID,
		MaxBufferDelayMS:        omitZeroInt(s.maxBufferDelayMS),
		AddTimestamps:           omitFalse(s.addTimestamps),
		AddPhonemeTimestamps:    omitFalse(s.addPhonemeTimestamps),
		UseNormalizedTimestamps: omitFalse(s.useNormalizedTimestamps),
		PronunciationDictID:     s.pronunciationDictID,
		GenerationConfig:        s.generationConfig(segment),
	}
	if err := s.writeJSON(ctx, request); err != nil {
		return s.writeError(err, segmentID)
	}
	return nil
}

func (s *realtimeSession) Finish(context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.finishing = true
	shouldEnd := len(s.contexts) == 0
	s.mu.Unlock()

	if shouldEnd {
		s.emitSessionEndAndClose()
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
		_ = s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
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

		var resp responseMessage
		if err := json.Unmarshal(message, &resp); err != nil {
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				Error: &tts.Error{
					Code:      tts.ErrProviderUnavailable,
					Message:   fmt.Sprintf("decode cartesia message: %v", err),
					Provider:  s.provider,
					SessionID: s.id,
					Cause:     err,
				},
			})
			continue
		}

		if s.handleResponse(resp) {
			return
		}
	}
}

func (s *realtimeSession) handleResponse(resp responseMessage) bool {
	if resp.StatusCode >= http.StatusBadRequest || resp.Type == "error" {
		s.handleError(resp)
		return false
	}

	switch resp.Type {
	case "chunk":
		s.handleChunk(resp)
		if resp.Done {
			return s.endContext(resp.ContextID)
		}
	case "done":
		return s.endContext(resp.ContextID)
	case "flush_done", "timestamps", "phoneme_timestamps":
		return false
	}
	return false
}

func (s *realtimeSession) handleChunk(resp responseMessage) {
	if resp.Data == "" {
		return
	}
	data, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		segmentID := s.segmentID(resp.ContextID)
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventError,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segmentID,
			Error: &tts.Error{
				Code:      tts.ErrAudioDecodeFailed,
				Message:   fmt.Sprintf("decode cartesia audio: %v", err),
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: segmentID,
				Cause:     err,
			},
		})
		return
	}

	segmentID := s.segmentID(resp.ContextID)
	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventAudio,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		Audio: &tts.ProviderAudioChunk{
			Codec:      audio.CodecPCM,
			Container:  audio.ContainerRaw,
			SampleRate: s.sampleRate,
			Channels:   audio.DefaultChannels,
			Format:     audio.PCMFormatS16LE,
			Data:       data,
		},
		RawMeta: map[string]any{
			"context_id":  resp.ContextID,
			"status_code": resp.StatusCode,
			"step_time":   resp.StepTime,
		},
	})
}

func (s *realtimeSession) handleError(resp responseMessage) {
	segmentID := s.segmentID(resp.ContextID)
	message := resp.Message
	if message == "" {
		message = resp.Title
	}
	if message == "" {
		message = fmt.Sprintf("cartesia tts status %d", resp.StatusCode)
	}
	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventError,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		Error: &tts.Error{
			Code:      statusCodeToErrorCode(resp.StatusCode),
			Message:   message,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segmentID,
			Retryable: resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError,
		},
		RawMeta: map[string]any{
			"context_id":  resp.ContextID,
			"error_code":  resp.ErrorCode,
			"title":       resp.Title,
			"request_id":  resp.RequestID,
			"status_code": resp.StatusCode,
		},
	})
	if resp.Done && resp.ContextID != "" {
		if s.failContext(resp.ContextID) {
			s.emitSessionEndAndClose()
		}
	}
}

func (s *realtimeSession) endContext(contextID string) bool {
	segmentID, shouldEndSession := s.clearContext(contextID)
	if segmentID != "" {
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventSegmentEnd,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segmentID,
			RawMeta:   map[string]any{"context_id": contextID},
		})
	}
	if shouldEndSession {
		s.emitSessionEndAndClose()
		return true
	}
	return false
}

func (s *realtimeSession) clearContext(contextID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	segmentID := s.contexts[contextID]
	if segmentID == "" {
		return "", false
	}
	delete(s.contexts, contextID)
	return segmentID, s.finishing && len(s.contexts) == 0
}

func (s *realtimeSession) failContext(contextID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.contexts[contextID] == "" {
		return false
	}
	delete(s.contexts, contextID)
	return s.finishing && len(s.contexts) == 0
}

func (s *realtimeSession) emitSessionEndAndClose() {
	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSessionEnd,
		Provider:  s.provider,
		SessionID: s.id,
	})
	_ = s.Close()
}

func (s *realtimeSession) segmentID(contextID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contexts[contextID]
}

func (s *realtimeSession) contextID(segmentID string) string {
	return fmt.Sprintf("%s_%s", s.id, segmentID)
}

func (s *realtimeSession) generationConfig(segment *tts.ProviderSegmentRequest) *generationConfigPayload {
	cfg := &generationConfigPayload{}
	if segment.Volume != 0 {
		cfg.Volume = segment.Volume
	} else if s.defaultVolume != 0 {
		cfg.Volume = s.defaultVolume
	}
	if segment.Speed != 0 {
		cfg.Speed = segment.Speed
	} else if s.defaultSpeed != 0 {
		cfg.Speed = s.defaultSpeed
	}
	if segment.Emotion != "" {
		cfg.Emotion = segment.Emotion
	} else if s.defaultEmotion != "" {
		cfg.Emotion = s.defaultEmotion
	}
	if cfg.Volume == 0 && cfg.Speed == 0 && cfg.Emotion == "" {
		return nil
	}
	return cfg
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

type generationRequest struct {
	ModelID                 string                   `json:"model_id"`
	Transcript              string                   `json:"transcript"`
	Voice                   voicePayload             `json:"voice"`
	OutputFormat            outputFormatPayload      `json:"output_format"`
	Language                string                   `json:"language,omitempty"`
	ContextID               string                   `json:"context_id"`
	Continue                bool                     `json:"continue,omitempty"`
	MaxBufferDelayMS        *int                     `json:"max_buffer_delay_ms,omitempty"`
	Flush                   bool                     `json:"flush,omitempty"`
	AddTimestamps           *bool                    `json:"add_timestamps,omitempty"`
	AddPhonemeTimestamps    *bool                    `json:"add_phoneme_timestamps,omitempty"`
	UseNormalizedTimestamps *bool                    `json:"use_normalized_timestamps,omitempty"`
	PronunciationDictID     string                   `json:"pronunciation_dict_id,omitempty"`
	GenerationConfig        *generationConfigPayload `json:"generation_config,omitempty"`
}

type voicePayload struct {
	Mode string `json:"mode"`
	ID   string `json:"id"`
}

type outputFormatPayload struct {
	Container  string `json:"container"`
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
}

type generationConfigPayload struct {
	Volume  float64 `json:"volume,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
	Emotion string  `json:"emotion,omitempty"`
}

type responseMessage struct {
	Type       string  `json:"type"`
	Data       string  `json:"data,omitempty"`
	Done       bool    `json:"done,omitempty"`
	StatusCode int     `json:"status_code,omitempty"`
	StepTime   float64 `json:"step_time,omitempty"`
	ContextID  string  `json:"context_id,omitempty"`

	FlushDone bool `json:"flush_done,omitempty"`
	FlushID   int  `json:"flush_id,omitempty"`

	ErrorCode string `json:"error_code,omitempty"`
	Title     string `json:"title,omitempty"`
	Message   string `json:"message,omitempty"`
	DocURL    string `json:"doc_url,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func sessionClosedError(provider, sessionID, segmentID string) *tts.Error {
	return &tts.Error{
		Code:      tts.ErrSessionClosed,
		Message:   "cartesia tts session is closed",
		Provider:  provider,
		SessionID: sessionID,
		SegmentID: segmentID,
	}
}

func statusCodeToErrorCode(statusCode int) tts.ErrorCode {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return tts.ErrProviderAuthFailed
	case http.StatusTooManyRequests:
		return tts.ErrProviderRateLimited
	default:
		return tts.ErrProviderUnavailable
	}
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func isSupportedSampleRate(sampleRate int) bool {
	for _, supported := range supportedSampleRates {
		if sampleRate == supported {
			return true
		}
	}
	return false
}

func omitZeroInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

func omitFalse(value bool) *bool {
	if !value {
		return nil
	}
	return &value
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
