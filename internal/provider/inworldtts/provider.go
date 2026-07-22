package inworldtts

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
	defaultProviderName  = "inworld_tts"
	defaultEndpoint      = "wss://api.inworld.ai/tts/v1/voice:streamBidirectional"
	defaultModel         = "inworld-tts-2"
	defaultAudioEncoding = "OGG_OPUS"
	defaultBitRate       = 128000
	defaultContextPrefix = "ctx"
)

// Config configures an Inworld AI bidirectional WebSocket TTS provider.
type Config struct {
	Name string

	Endpoint      string
	APIKey        string
	Authorization string

	Model           string
	DefaultVoice    string
	DefaultLanguage string
	ContextID       string

	BitRate                    int
	BufferCharThreshold        int
	MaxBufferDelayMS           int
	AutoMode                   bool
	ApplyTextNormalization     string
	DeliveryMode               string
	TimestampType              string
	TimestampTransportStrategy string
}

// Provider adapts Inworld AI WebSocket TTS to the TTS Provider interface.
type Provider struct {
	name string

	endpoint      string
	apiKey        string
	authorization string

	model           string
	defaultVoice    string
	defaultLanguage string
	contextID       string

	bitRate                    int
	bufferCharThreshold        int
	maxBufferDelayMS           int
	autoMode                   bool
	applyTextNormalization     string
	deliveryMode               string
	timestampType              string
	timestampTransportStrategy string
}

// NewProvider creates an Inworld AI TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.BitRate == 0 {
		cfg.BitRate = defaultBitRate
	}
	if cfg.BitRate < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "inworld tts bit rate must be positive",
			Provider: cfg.Name,
		}
	}

	return &Provider{
		name:                       cfg.Name,
		endpoint:                   cfg.Endpoint,
		apiKey:                     cfg.APIKey,
		authorization:              cfg.Authorization,
		model:                      cfg.Model,
		defaultVoice:               cfg.DefaultVoice,
		defaultLanguage:            cfg.DefaultLanguage,
		contextID:                  cfg.ContextID,
		bitRate:                    cfg.BitRate,
		bufferCharThreshold:        cfg.BufferCharThreshold,
		maxBufferDelayMS:           cfg.MaxBufferDelayMS,
		autoMode:                   cfg.AutoMode,
		applyTextNormalization:     cfg.ApplyTextNormalization,
		deliveryMode:               cfg.DeliveryMode,
		timestampType:              cfg.TimestampType,
		timestampTransportStrategy: cfg.TimestampTransportStrategy,
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
		SupportsSegmentEndEvent: true,
		SupportsOggOpusOutput:   true,
		OutputCodecs:            []audio.Codec{audio.CodecOpus},
		OutputContainers:        []audio.Container{audio.ContainerOgg},
		OutputSampleRates:       []int{audio.OpusSampleRate},
		OutputChannels:          []int{audio.DefaultChannels},
	}
	return caps, nil
}

func (p *Provider) SynthesizeOnce(context.Context, *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	return nil, &tts.Error{
		Code:     tts.ErrUnsupportedFeature,
		Message:  "inworld tts provider only supports sessions",
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
	if voice == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedVoice,
			Message:  "inworld tts voice is required",
			Provider: p.name,
		}
	}

	endpoint, err := p.websocketURL()
	if err != nil {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  err.Error(),
			Provider: p.name,
			Cause:    err,
		}
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, endpoint, http.Header{})
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
		sessionID = newID(defaultContextPrefix)
	}
	contextID := valueOrDefault(p.contextID, sessionID)

	session := newRealtimeSession(realtimeSessionConfig{
		id:                         sessionID,
		contextID:                  contextID,
		provider:                   p.name,
		conn:                       conn,
		model:                      p.model,
		voice:                      voice,
		language:                   valueOrDefault(req.Language, p.defaultLanguage),
		bitRate:                    p.bitRate,
		bufferCharThreshold:        p.bufferCharThreshold,
		maxBufferDelayMS:           p.maxBufferDelayMS,
		autoMode:                   p.autoMode,
		applyTextNormalization:     p.applyTextNormalization,
		deliveryMode:               p.deliveryMode,
		timestampType:              p.timestampType,
		timestampTransportStrategy: p.timestampTransportStrategy,
	})
	if err := session.createContext(ctx); err != nil {
		_ = session.Close()
		return nil, err
	}
	go session.readLoop()
	return session, nil
}

func (p *Provider) websocketURL() (string, error) {
	parsed, err := url.Parse(p.endpoint)
	if err != nil {
		return "", err
	}
	auth := p.authorization
	if auth == "" && p.apiKey != "" {
		auth = p.apiKey
		if !strings.HasPrefix(strings.ToLower(auth), "basic ") {
			auth = "Basic " + auth
		}
	}
	if auth != "" {
		query := parsed.Query()
		query.Set("authorization", auth)
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

type realtimeSessionConfig struct {
	id        string
	contextID string
	provider  string
	conn      *websocket.Conn

	model    string
	voice    string
	language string

	bitRate                    int
	bufferCharThreshold        int
	maxBufferDelayMS           int
	autoMode                   bool
	applyTextNormalization     string
	deliveryMode               string
	timestampType              string
	timestampTransportStrategy string
}

type realtimeSession struct {
	id        string
	contextID string
	provider  string
	conn      *websocket.Conn

	model    string
	voice    string
	language string

	bitRate                    int
	bufferCharThreshold        int
	maxBufferDelayMS           int
	autoMode                   bool
	applyTextNormalization     string
	deliveryMode               string
	timestampType              string
	timestampTransportStrategy string

	writeMu sync.Mutex

	mu               sync.Mutex
	closed           bool
	currentSegmentID string

	events    chan *tts.ProviderEvent
	closeOnce sync.Once
	done      chan struct{}
}

func newRealtimeSession(cfg realtimeSessionConfig) *realtimeSession {
	return &realtimeSession{
		id:                         cfg.id,
		contextID:                  cfg.contextID,
		provider:                   cfg.provider,
		conn:                       cfg.conn,
		model:                      cfg.model,
		voice:                      cfg.voice,
		language:                   cfg.language,
		bitRate:                    cfg.bitRate,
		bufferCharThreshold:        cfg.bufferCharThreshold,
		maxBufferDelayMS:           cfg.maxBufferDelayMS,
		autoMode:                   cfg.autoMode,
		applyTextNormalization:     cfg.applyTextNormalization,
		deliveryMode:               cfg.deliveryMode,
		timestampType:              cfg.timestampType,
		timestampTransportStrategy: cfg.timestampTransportStrategy,
		events:                     make(chan *tts.ProviderEvent, 32),
		done:                       make(chan struct{}),
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
		return sessionClosedError(s.provider, s.id, segment.SegmentID)
	}
	s.currentSegmentID = segment.SegmentID
	s.mu.Unlock()

	if !s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentStart,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segment.SegmentID,
	}) {
		return sessionClosedError(s.provider, s.id, segment.SegmentID)
	}

	if err := s.writeJSON(ctx, sendTextMessage{
		ContextID: s.contextID,
		SendText: sendTextPayload{
			Text:         segment.Text,
			FlushContext: &emptyObject{},
		},
	}); err != nil {
		return s.writeError(err, segment.SegmentID)
	}
	return nil
}

func (s *realtimeSession) Finish(ctx context.Context) error {
	if err := s.writeJSON(ctx, closeContextMessage{
		ContextID:    s.contextID,
		CloseContext: &emptyObject{},
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
		_ = s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		err = s.conn.Close()
	})
	return err
}

func (s *realtimeSession) createContext(ctx context.Context) error {
	if err := s.writeJSON(ctx, createContextMessage{
		ContextID: s.contextID,
		Create: createContextPayload{
			VoiceID: s.voice,
			ModelID: s.model,
			AudioConfig: audioConfigPayload{
				AudioEncoding:   defaultAudioEncoding,
				SampleRateHertz: audio.OpusSampleRate,
				BitRate:         s.bitRate,
			},
			BufferCharThreshold:        s.bufferCharThreshold,
			MaxBufferDelayMS:           s.maxBufferDelayMS,
			AutoMode:                   s.autoMode,
			ApplyTextNormalization:     s.applyTextNormalization,
			DeliveryMode:               s.deliveryMode,
			TimestampType:              s.timestampType,
			TimestampTransportStrategy: s.timestampTransportStrategy,
			Language:                   s.language,
		},
	}); err != nil {
		return s.writeError(err, "")
	}

	for {
		var resp responseMessage
		if err := s.conn.ReadJSON(&resp); err != nil {
			return s.writeError(err, "")
		}
		if err := s.statusError(&resp, "create context"); err != nil {
			return err
		}
		if resp.Result != nil && resp.Result.ContextCreated != nil {
			if resp.Result.ContextID != "" {
				s.contextID = resp.Result.ContextID
			}
			return nil
		}
	}
}

func (s *realtimeSession) readLoop() {
	defer close(s.events)
	defer func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
	}()

	for {
		var resp responseMessage
		if err := s.conn.ReadJSON(&resp); err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: s.segmentID(),
				Error:     s.writeError(err, s.segmentID()),
			})
			return
		}
		if s.handleResponse(&resp) {
			return
		}
	}
}

func (s *realtimeSession) handleResponse(resp *responseMessage) bool {
	if resp == nil || resp.Result == nil {
		return false
	}
	if err := s.statusError(resp, "inworld response"); err != nil {
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventError,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: s.segmentID(),
			Error:     err,
		})
		return true
	}
	result := resp.Result
	switch {
	case result.AudioChunk != nil:
		s.handleAudioChunk(result)
	case result.FlushCompleted != nil:
		s.handleFlushCompleted(result)
	case result.ContextClosed != nil:
		s.endCurrentSegment(result.meta())
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventSessionEnd,
			Provider:  s.provider,
			SessionID: s.id,
			RawMeta:   result.meta(),
		})
		return true
	}
	return false
}

func (s *realtimeSession) handleAudioChunk(result *responseResult) {
	if result.AudioChunk.AudioContent == "" {
		return
	}
	data, err := base64.StdEncoding.DecodeString(result.AudioChunk.AudioContent)
	if err != nil {
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventError,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: s.segmentID(),
			RawMeta:   result.meta(),
			Error: &tts.Error{
				Code:      tts.ErrAudioDecodeFailed,
				Message:   fmt.Sprintf("decode inworld audio: %v", err),
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
		RawMeta:   result.meta(),
		Audio: &tts.ProviderAudioChunk{
			Codec:      audio.CodecOpus,
			Container:  audio.ContainerOgg,
			SampleRate: audio.OpusSampleRate,
			Channels:   audio.DefaultChannels,
			Data:       data,
		},
	})
}

func (s *realtimeSession) handleFlushCompleted(result *responseResult) {
	s.endCurrentSegment(result.meta())
}

func (s *realtimeSession) endCurrentSegment(meta map[string]any) {
	segmentID := s.clearSegmentID()
	if segmentID == "" {
		return
	}
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

func (s *realtimeSession) statusError(resp *responseMessage, action string) *tts.Error {
	if resp == nil || resp.Result == nil || resp.Result.Status == nil || resp.Result.Status.Code == 0 {
		return nil
	}
	return &tts.Error{
		Code:      tts.ErrProviderUnavailable,
		Message:   fmt.Sprintf("%s failed: %s", action, resp.Result.Status.Message),
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: s.segmentID(),
		Retryable: false,
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

type createContextMessage struct {
	Create    createContextPayload `json:"create"`
	ContextID string               `json:"contextId,omitempty"`
}

type createContextPayload struct {
	VoiceID                    string             `json:"voiceId"`
	ModelID                    string             `json:"modelId"`
	AudioConfig                audioConfigPayload `json:"audioConfig,omitempty"`
	BufferCharThreshold        int                `json:"bufferCharThreshold,omitempty"`
	MaxBufferDelayMS           int                `json:"maxBufferDelayMs,omitempty"`
	AutoMode                   bool               `json:"autoMode,omitempty"`
	ApplyTextNormalization     string             `json:"applyTextNormalization,omitempty"`
	DeliveryMode               string             `json:"deliveryMode,omitempty"`
	TimestampType              string             `json:"timestampType,omitempty"`
	TimestampTransportStrategy string             `json:"timestampTransportStrategy,omitempty"`
	Language                   string             `json:"language,omitempty"`
}

type audioConfigPayload struct {
	AudioEncoding   string  `json:"audioEncoding,omitempty"`
	SampleRateHertz int     `json:"sampleRateHertz,omitempty"`
	BitRate         int     `json:"bitRate,omitempty"`
	SpeakingRate    float64 `json:"speakingRate,omitempty"`
}

type sendTextMessage struct {
	SendText  sendTextPayload `json:"send_text"`
	ContextID string          `json:"contextId,omitempty"`
}

type sendTextPayload struct {
	Text         string       `json:"text"`
	FlushContext *emptyObject `json:"flush_context,omitempty"`
}

type closeContextMessage struct {
	CloseContext *emptyObject `json:"close_context"`
	ContextID    string       `json:"contextId,omitempty"`
}

type responseMessage struct {
	Result *responseResult `json:"result,omitempty"`
}

type responseResult struct {
	ContextID      string            `json:"contextId,omitempty"`
	ContextCreated *emptyObject      `json:"contextCreated,omitempty"`
	AudioChunk     *audioChunkResult `json:"audioChunk,omitempty"`
	FlushCompleted *emptyObject      `json:"flushCompleted,omitempty"`
	ContextClosed  *emptyObject      `json:"contextClosed,omitempty"`
	Status         *statusResult     `json:"status,omitempty"`
}

type audioChunkResult struct {
	AudioContent  string         `json:"audioContent,omitempty"`
	Usage         map[string]any `json:"usage,omitempty"`
	TimestampInfo map[string]any `json:"timestampInfo,omitempty"`
	Status        *statusResult  `json:"status,omitempty"`
}

type statusResult struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type emptyObject struct{}

func (r *responseResult) meta() map[string]any {
	meta := map[string]any{"context_id": r.ContextID}
	if r.AudioChunk != nil && r.AudioChunk.Usage != nil {
		if modelID, ok := r.AudioChunk.Usage["modelId"].(string); ok {
			meta["model_id"] = modelID
		}
	}
	return meta
}

func sessionClosedError(provider, sessionID, segmentID string) *tts.Error {
	return &tts.Error{
		Code:      tts.ErrSessionClosed,
		Message:   "inworld tts session is closed",
		Provider:  provider,
		SessionID: sessionID,
		SegmentID: segmentID,
	}
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
