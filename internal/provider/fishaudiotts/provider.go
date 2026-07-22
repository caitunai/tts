package fishaudiotts

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultProviderName = "fishaudio_tts"
	defaultEndpoint     = "wss://api.fish.audio/v1/tts/live"
	defaultModel        = "s1"
	defaultFormat       = "opus"
	defaultLatency      = "normal"
	defaultOpusBitrate  = -1000
	defaultSegmentIdle  = 800 * time.Millisecond
)

// Config configures Fish Audio's WebSocket TTS provider.
type Config struct {
	Name string

	Endpoint string
	APIKey   string
	Model    string

	DefaultVoice    string
	DefaultLanguage string

	Latency            string
	OpusBitrate        int
	SegmentIdleTimeout time.Duration

	MaxNewTokens              int
	Temperature               float64
	TopP                      float64
	RepetitionPenalty         float64
	MinChunkLength            int
	ChunkLength               int
	ConditionOnPreviousChunks *bool
	Normalize                 *bool
	EarlyStopThreshold        float64
	DefaultSpeed              float64
	DefaultVolume             float64
	NormalizeLoudness         *bool
}

// Provider adapts Fish Audio's MessagePack WebSocket TTS API to the TTS
// Provider interface.
type Provider struct {
	name string

	endpoint string
	apiKey   string
	model    string

	defaultVoice    string
	defaultLanguage string

	latency     string
	opusBitrate int
	segmentIdle time.Duration

	maxNewTokens              int
	temperature               float64
	topP                      float64
	repetitionPenalty         float64
	minChunkLength            int
	chunkLength               int
	conditionOnPreviousChunks *bool
	normalize                 *bool
	earlyStopThreshold        float64
	defaultSpeed              float64
	defaultVolume             float64
	normalizeLoudness         *bool
}

// NewProvider creates a Fish Audio TTS provider.
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
	if cfg.Latency == "" {
		cfg.Latency = defaultLatency
	}
	if cfg.OpusBitrate == 0 {
		cfg.OpusBitrate = defaultOpusBitrate
	}
	if cfg.SegmentIdleTimeout == 0 {
		cfg.SegmentIdleTimeout = defaultSegmentIdle
	}
	if cfg.SegmentIdleTimeout < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "fish audio segment idle timeout must be non-negative",
			Provider: cfg.Name,
		}
	}

	return &Provider{
		name:                      cfg.Name,
		endpoint:                  cfg.Endpoint,
		apiKey:                    cfg.APIKey,
		model:                     cfg.Model,
		defaultVoice:              cfg.DefaultVoice,
		defaultLanguage:           cfg.DefaultLanguage,
		latency:                   cfg.Latency,
		opusBitrate:               cfg.OpusBitrate,
		segmentIdle:               cfg.SegmentIdleTimeout,
		maxNewTokens:              cfg.MaxNewTokens,
		temperature:               cfg.Temperature,
		topP:                      cfg.TopP,
		repetitionPenalty:         cfg.RepetitionPenalty,
		minChunkLength:            cfg.MinChunkLength,
		chunkLength:               cfg.ChunkLength,
		conditionOnPreviousChunks: cfg.ConditionOnPreviousChunks,
		normalize:                 cfg.Normalize,
		earlyStopThreshold:        cfg.EarlyStopThreshold,
		defaultSpeed:              cfg.DefaultSpeed,
		defaultVolume:             cfg.DefaultVolume,
		normalizeLoudness:         cfg.NormalizeLoudness,
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
		SupportsVolume:          true,
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
		Message:  "fish audio tts provider only supports websocket sessions",
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
	if p.apiKey != "" {
		header.Set("Authorization", "Bearer "+p.apiKey)
	}
	if p.model != "" {
		header.Set("model", p.model)
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, p.endpoint, header)
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
	session := newRealtimeSession(realtimeSessionConfig{
		id:                        sessionID,
		provider:                  p.name,
		conn:                      conn,
		defaultVoice:              p.defaultVoice,
		initialVoice:              req.Voice,
		defaultLanguage:           p.defaultLanguage,
		initialLanguage:           req.Language,
		latency:                   p.latency,
		opusBitrate:               p.opusBitrate,
		segmentIdle:               p.segmentIdle,
		maxNewTokens:              p.maxNewTokens,
		temperature:               p.temperature,
		topP:                      p.topP,
		repetitionPenalty:         p.repetitionPenalty,
		minChunkLength:            p.minChunkLength,
		chunkLength:               p.chunkLength,
		conditionOnPreviousChunks: p.conditionOnPreviousChunks,
		normalize:                 p.normalize,
		earlyStopThreshold:        p.earlyStopThreshold,
		defaultSpeed:              p.defaultSpeed,
		defaultVolume:             p.defaultVolume,
		normalizeLoudness:         p.normalizeLoudness,
	})
	if err := session.start(ctx); err != nil {
		_ = session.Close()
		return nil, err
	}
	go session.readLoop()
	return session, nil
}

type realtimeSessionConfig struct {
	id       string
	provider string
	conn     *websocket.Conn

	defaultVoice    string
	initialVoice    string
	defaultLanguage string
	initialLanguage string

	latency     string
	opusBitrate int
	segmentIdle time.Duration

	maxNewTokens              int
	temperature               float64
	topP                      float64
	repetitionPenalty         float64
	minChunkLength            int
	chunkLength               int
	conditionOnPreviousChunks *bool
	normalize                 *bool
	earlyStopThreshold        float64
	defaultSpeed              float64
	defaultVolume             float64
	normalizeLoudness         *bool
}

type realtimeSession struct {
	id       string
	provider string
	conn     *websocket.Conn

	defaultVoice    string
	initialVoice    string
	defaultLanguage string
	initialLanguage string

	latency     string
	opusBitrate int
	segmentIdle time.Duration

	maxNewTokens              int
	temperature               float64
	topP                      float64
	repetitionPenalty         float64
	minChunkLength            int
	chunkLength               int
	conditionOnPreviousChunks *bool
	normalize                 *bool
	earlyStopThreshold        float64
	defaultSpeed              float64
	defaultVolume             float64
	normalizeLoudness         *bool

	writeMu sync.Mutex

	mu                   sync.Mutex
	closed               bool
	currentSegmentID     string
	currentSegmentIsLast bool
	segmentSeq           uint64
	segmentIdleTimer     *time.Timer
	finishSent           bool

	events    chan *tts.ProviderEvent
	closeOnce sync.Once
	done      chan struct{}
}

func newRealtimeSession(cfg realtimeSessionConfig) *realtimeSession {
	return &realtimeSession{
		id:                        cfg.id,
		provider:                  cfg.provider,
		conn:                      cfg.conn,
		defaultVoice:              cfg.defaultVoice,
		initialVoice:              cfg.initialVoice,
		defaultLanguage:           cfg.defaultLanguage,
		initialLanguage:           cfg.initialLanguage,
		latency:                   cfg.latency,
		opusBitrate:               cfg.opusBitrate,
		segmentIdle:               cfg.segmentIdle,
		maxNewTokens:              cfg.maxNewTokens,
		temperature:               cfg.temperature,
		topP:                      cfg.topP,
		repetitionPenalty:         cfg.repetitionPenalty,
		minChunkLength:            cfg.minChunkLength,
		chunkLength:               cfg.chunkLength,
		conditionOnPreviousChunks: cfg.conditionOnPreviousChunks,
		normalize:                 cfg.normalize,
		earlyStopThreshold:        cfg.earlyStopThreshold,
		defaultSpeed:              cfg.defaultSpeed,
		defaultVolume:             cfg.defaultVolume,
		normalizeLoudness:         cfg.normalizeLoudness,
		events:                    make(chan *tts.ProviderEvent, 32),
		done:                      make(chan struct{}),
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
		segmentID = newID("seg")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return sessionClosedError(s.provider, s.id, segmentID)
	}
	s.currentSegmentID = segmentID
	s.currentSegmentIsLast = segment.IsLast
	s.segmentSeq++
	s.stopSegmentIdleTimerLocked()
	s.mu.Unlock()

	if !s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentStart,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
	}) {
		return sessionClosedError(s.provider, s.id, segmentID)
	}

	if err := s.writeMsgpack(ctx, map[string]any{
		"event": "text",
		"text":  segment.Text,
	}); err != nil {
		return s.writeError(err, segmentID)
	}
	if err := s.writeMsgpack(ctx, map[string]any{"event": "flush"}); err != nil {
		return s.writeError(err, segmentID)
	}
	if segment.IsLast {
		if err := s.finishSessionOnce(ctx, segmentID); err != nil {
			return err
		}
	}
	return nil
}

func (s *realtimeSession) Finish(ctx context.Context) error {
	return s.finishSessionOnce(ctx, "")
}

func (s *realtimeSession) Events() <-chan *tts.ProviderEvent {
	return s.events
}

func (s *realtimeSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.stopSegmentIdleTimerLocked()
		s.mu.Unlock()
		close(s.done)
		_ = s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		err = s.conn.Close()
	})
	return err
}

func (s *realtimeSession) start(ctx context.Context) error {
	if err := s.writeMsgpack(ctx, map[string]any{
		"event":   "start",
		"request": s.startRequest(),
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
				Error:     s.writeError(err, s.segmentID()),
			})
			return
		}
		msg, err := unmarshalMsgpackMap(message)
		if err != nil {
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: s.segmentID(),
				Error:     s.eventError(tts.ErrProviderUnavailable, fmt.Sprintf("decode fish audio message: %v", err), s.segmentID(), err, false),
			})
			continue
		}
		if s.handleMessage(msg) {
			return
		}
	}
}

func (s *realtimeSession) handleMessage(msg map[string]any) bool {
	switch stringValue(msg["event"]) {
	case "audio":
		audioData := bytesValue(msg["audio"])
		if len(audioData) == 0 {
			return false
		}
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventAudio,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: s.segmentID(),
			Audio: &tts.ProviderAudioChunk{
				Codec:      audio.CodecOpus,
				Container:  audio.ContainerOgg,
				SampleRate: audio.OpusSampleRate,
				Channels:   audio.DefaultChannels,
				Data:       audioData,
			},
		})
		s.scheduleSegmentIdleEnd(map[string]any{"segment_end_reason": "audio_idle"})
	case "finish":
		if stringValue(msg["reason"]) == "error" {
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: s.segmentID(),
				Error:     s.eventError(tts.ErrProviderUnavailable, "fish audio synthesis finished with error", s.segmentID(), nil, false),
			})
		}
		s.endCurrentSegment(map[string]any{"finish_reason": stringValue(msg["reason"])})
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventSessionEnd,
			Provider:  s.provider,
			SessionID: s.id,
			RawMeta:   map[string]any{"finish_reason": stringValue(msg["reason"])},
		})
		return true
	}
	return false
}

func (s *realtimeSession) startRequest() map[string]any {
	request := map[string]any{
		"text":        "",
		"format":      defaultFormat,
		"sample_rate": audio.OpusSampleRate,
	}
	if s.latency != "" {
		request["latency"] = s.latency
	}
	if s.opusBitrate != 0 {
		request["opus_bitrate"] = s.opusBitrate
	}
	if voice := valueOrDefault(s.initialVoice, s.defaultVoice); voice != "" {
		request["reference_id"] = voice
	}
	if s.maxNewTokens > 0 {
		request["max_new_tokens"] = s.maxNewTokens
	}
	if s.temperature > 0 {
		request["temperature"] = s.temperature
	}
	if s.topP > 0 {
		request["top_p"] = s.topP
	}
	if s.repetitionPenalty > 0 {
		request["repetition_penalty"] = s.repetitionPenalty
	}
	if s.minChunkLength > 0 {
		request["min_chunk_length"] = s.minChunkLength
	}
	if s.chunkLength > 0 {
		request["chunk_length"] = s.chunkLength
	}
	if s.conditionOnPreviousChunks != nil {
		request["condition_on_previous_chunks"] = *s.conditionOnPreviousChunks
	}
	if s.normalize != nil {
		request["normalize"] = *s.normalize
	}
	if s.earlyStopThreshold > 0 {
		request["early_stop_threshold"] = s.earlyStopThreshold
	}
	prosody := map[string]any{}
	if s.defaultSpeed > 0 {
		prosody["speed"] = s.defaultSpeed
	}
	if s.defaultVolume != 0 {
		prosody["volume"] = s.defaultVolume
	}
	if s.normalizeLoudness != nil {
		prosody["normalize_loudness"] = *s.normalizeLoudness
	}
	if len(prosody) > 0 {
		request["prosody"] = prosody
	}
	return request
}

func (s *realtimeSession) scheduleSegmentIdleEnd(meta map[string]any) {
	s.mu.Lock()
	if s.segmentIdle <= 0 || s.currentSegmentID == "" || s.currentSegmentIsLast {
		s.mu.Unlock()
		return
	}
	segmentID := s.currentSegmentID
	segmentSeq := s.segmentSeq
	s.stopSegmentIdleTimerLocked()
	s.segmentIdleTimer = time.AfterFunc(s.segmentIdle, func() {
		s.endSegmentIfCurrent(segmentID, segmentSeq, meta)
	})
	s.mu.Unlock()
}

func (s *realtimeSession) endSegmentIfCurrent(segmentID string, segmentSeq uint64, meta map[string]any) {
	s.mu.Lock()
	if s.currentSegmentID != segmentID || s.segmentSeq != segmentSeq {
		s.mu.Unlock()
		return
	}
	s.currentSegmentID = ""
	s.currentSegmentIsLast = false
	s.stopSegmentIdleTimerLocked()
	s.mu.Unlock()

	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentEnd,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		RawMeta:   meta,
	})
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

func (s *realtimeSession) finishSessionOnce(ctx context.Context, segmentID string) error {
	s.mu.Lock()
	if s.finishSent {
		s.mu.Unlock()
		return nil
	}
	s.finishSent = true
	s.stopSegmentIdleTimerLocked()
	s.mu.Unlock()

	if err := s.writeMsgpack(ctx, map[string]any{"event": "stop"}); err != nil {
		return s.writeError(err, segmentID)
	}
	return nil
}

func (s *realtimeSession) writeMsgpack(ctx context.Context, value map[string]any) error {
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
	data, err := marshalMsgpack(value)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
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
	s.currentSegmentIsLast = false
	s.stopSegmentIdleTimerLocked()
	return segmentID
}

func (s *realtimeSession) stopSegmentIdleTimerLocked() {
	if s.segmentIdleTimer == nil {
		return
	}
	s.segmentIdleTimer.Stop()
	s.segmentIdleTimer = nil
}

func (s *realtimeSession) writeError(err error, segmentID string) *tts.Error {
	return s.eventError(tts.ErrProviderUnavailable, err.Error(), segmentID, err, true)
}

func (s *realtimeSession) eventError(code tts.ErrorCode, message, segmentID string, cause error, retryable bool) *tts.Error {
	return &tts.Error{
		Code:      code,
		Message:   message,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		Cause:     cause,
		Retryable: retryable,
	}
}

func sessionClosedError(provider, sessionID, segmentID string) *tts.Error {
	return &tts.Error{
		Code:      tts.ErrSessionClosed,
		Message:   "fish audio tts session is closed",
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

func stringValue(value any) string {
	if value, ok := value.(string); ok {
		return value
	}
	return ""
}

func bytesValue(value any) []byte {
	switch v := value.(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	default:
		return nil
	}
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
