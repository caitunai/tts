package doubaotts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/caitunai/tts/internal/audio"
	platformtts "github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultProviderName      = "doubao_tts"
	defaultEndpoint          = "wss://openspeech.bytedance.com/api/v3/tts/bidirection"
	defaultNamespace         = "BidirectionalTTS"
	defaultResourceID        = "seed-tts-2.0"
	defaultCloneResourceID   = "volc.megatts.default"
	defaultRequestSampleRate = 16000
	defaultSegmentIdle       = 800 * time.Millisecond
	defaultUserIDPrefix      = "user"
	defaultSessionIDPrefix   = "sess"
	defaultConnectIDPrefix   = "conn"
)

// Config configures a Doubao bidirectional WebSocket TTS provider.
type Config struct {
	Name string

	Endpoint string
	APIKey   string

	// Legacy authentication fields are kept for older Volcengine console
	// credentials, while APIKey maps to the newer X-Api-Key header.
	AppID     string
	AccessKey string

	ResourceID         string
	DefaultVoice       string
	DefaultLanguage    string
	RequestSampleRate  int
	DefaultUserID      string
	DefaultSectionID   string
	SegmentIdleTimeout time.Duration
	ConnectIDGenerator func() string
}

// Provider adapts Doubao bidirectional WebSocket TTS to the TTS Provider
// interface.
type Provider struct {
	name string

	endpoint  string
	apiKey    string
	appID     string
	accessKey string

	resourceID         string
	defaultVoice       string
	defaultLanguage    string
	requestSampleRate  int
	defaultUserID      string
	defaultSectionID   string
	segmentIdleTimeout time.Duration

	connectIDGenerator func() string
}

// NewProvider creates a Doubao TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	if cfg.ResourceID == "" {
		cfg.ResourceID = defaultResourceID
	}
	if cfg.RequestSampleRate == 0 {
		cfg.RequestSampleRate = defaultRequestSampleRate
	}
	if cfg.SegmentIdleTimeout == 0 {
		cfg.SegmentIdleTimeout = defaultSegmentIdle
	}
	if cfg.ConnectIDGenerator == nil {
		cfg.ConnectIDGenerator = func() string { return newID(defaultConnectIDPrefix) }
	}

	return &Provider{
		name:               cfg.Name,
		endpoint:           cfg.Endpoint,
		apiKey:             cfg.APIKey,
		appID:              cfg.AppID,
		accessKey:          cfg.AccessKey,
		resourceID:         cfg.ResourceID,
		defaultVoice:       cfg.DefaultVoice,
		defaultLanguage:    cfg.DefaultLanguage,
		requestSampleRate:  cfg.RequestSampleRate,
		defaultUserID:      cfg.DefaultUserID,
		defaultSectionID:   cfg.DefaultSectionID,
		segmentIdleTimeout: cfg.SegmentIdleTimeout,
		connectIDGenerator: cfg.ConnectIDGenerator,
	}, nil
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) Capabilities(context.Context) (*platformtts.ProviderCapabilities, error) {
	caps := &platformtts.ProviderCapabilities{
		Name:                         p.name,
		Transports:                   []platformtts.TransportType{platformtts.TransportWebSocket},
		SupportsStreaming:            true,
		SupportsAppendText:           true,
		SupportsGuidanceText:         true,
		SupportsSegmentLevelGuidance: true,
		SupportsSegmentEndEvent:      true,
		SupportsOggOpusOutput:        true,
		OutputCodecs:                 []audio.Codec{audio.CodecOpus},
		OutputContainers:             []audio.Container{audio.ContainerOgg},
		OutputSampleRates:            []int{audio.OpusSampleRate},
		OutputChannels:               []int{audio.DefaultChannels},
		Languages: []platformtts.LanguageInfo{
			{Code: "zh", Name: "Chinese"},
			{Code: "zh-cn", Name: "Chinese"},
			{Code: "en", Name: "English"},
			{Code: "ja", Name: "Japanese"},
			{Code: "ko", Name: "Korean"},
			{Code: "es-mx", Name: "Spanish (Mexico)"},
			{Code: "id", Name: "Indonesian"},
			{Code: "pt-br", Name: "Portuguese (Brazil)"},
		},
	}
	if p.defaultVoice != "" {
		caps.Voices = []platformtts.VoiceInfo{{ID: p.defaultVoice, Name: p.defaultVoice}}
	}
	return caps, nil
}

func (p *Provider) SynthesizeOnce(context.Context, *platformtts.ProviderSynthesizeRequest) (<-chan *platformtts.ProviderEvent, error) {
	return nil, &platformtts.Error{
		Code:     platformtts.ErrUnsupportedFeature,
		Message:  "doubao tts provider only supports sessions",
		Provider: p.name,
	}
}

func (p *Provider) OpenSession(ctx context.Context, req *platformtts.ProviderOpenSessionRequest) (platformtts.ProviderSession, error) {
	if req == nil {
		return nil, &platformtts.Error{
			Code:     platformtts.ErrInternal,
			Message:  "open session request is nil",
			Provider: p.name,
		}
	}

	voice := valueOrDefault(req.Voice, p.defaultVoice)
	if voice == "" {
		return nil, &platformtts.Error{
			Code:     platformtts.ErrUnsupportedProvider,
			Message:  "doubao voice is required",
			Provider: p.name,
		}
	}

	header := http.Header{}
	if p.apiKey != "" {
		header.Set("X-Api-Key", p.apiKey)
	}
	if p.appID != "" {
		header.Set("X-Api-App-Key", p.appID)
	}
	if p.accessKey != "" {
		header.Set("X-Api-Access-Key", p.accessKey)
	}
	resourceID := p.resourceIDForVoice(voice)
	header.Set("X-Api-Resource-Id", resourceID)
	header.Set("X-Api-Connect-Id", p.connectIDGenerator())

	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, p.endpoint, header)
	if err != nil {
		if resp != nil && resp.Body != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			err = fmt.Errorf("%w: %s", err, string(body))
		}
		return nil, &platformtts.Error{
			Code:      platformtts.ErrProviderUnavailable,
			Message:   err.Error(),
			Provider:  p.name,
			Cause:     err,
			Retryable: true,
		}
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = newID(defaultSessionIDPrefix)
	}
	userID := valueOrDefault(p.defaultUserID, sessionID)

	session := newRealtimeSession(realtimeSessionConfig{
		id:                sessionID,
		provider:          p.name,
		conn:              conn,
		defaultVoice:      p.defaultVoice,
		defaultLanguage:   p.defaultLanguage,
		initialVoice:      req.Voice,
		initialLanguage:   req.Language,
		initialGuidance:   req.GuidanceText,
		userID:            userID,
		sectionID:         p.defaultSectionID,
		requestSampleRate: p.requestSampleRate,
		segmentIdle:       p.segmentIdleTimeout,
	})
	if err := session.start(ctx, voice, req.Language, req.GuidanceText); err != nil {
		_ = session.Close()
		return nil, err
	}
	go session.readLoop()
	return session, nil
}

func (p *Provider) resourceIDForVoice(voice string) string {
	if p.resourceID != "" && p.resourceID != defaultResourceID {
		return p.resourceID
	}
	if strings.HasPrefix(voice, "S_") {
		return defaultCloneResourceID
	}
	return p.resourceID
}

type realtimeSessionConfig struct {
	id       string
	provider string
	conn     *websocket.Conn

	defaultVoice    string
	defaultLanguage string
	initialVoice    string
	initialLanguage string
	initialGuidance string

	userID            string
	sectionID         string
	requestSampleRate int
	segmentIdle       time.Duration
}

type realtimeSession struct {
	id       string
	provider string
	conn     *websocket.Conn

	defaultVoice    string
	defaultLanguage string
	initialVoice    string
	initialLanguage string
	initialGuidance string

	userID            string
	sectionID         string
	requestSampleRate int
	segmentIdle       time.Duration

	writeMu sync.Mutex

	mu                   sync.Mutex
	closed               bool
	currentSegmentID     string
	currentSegmentIsLast bool
	currentVoice         string
	currentLanguage      string
	currentGuidance      string
	segmentSeq           uint64
	segmentIdleTimer     *time.Timer
	finishSent           bool

	events    chan *platformtts.ProviderEvent
	closeOnce sync.Once
	done      chan struct{}
}

func newRealtimeSession(cfg realtimeSessionConfig) *realtimeSession {
	return &realtimeSession{
		id:                cfg.id,
		provider:          cfg.provider,
		conn:              cfg.conn,
		defaultVoice:      cfg.defaultVoice,
		defaultLanguage:   cfg.defaultLanguage,
		initialVoice:      cfg.initialVoice,
		initialLanguage:   cfg.initialLanguage,
		initialGuidance:   cfg.initialGuidance,
		userID:            cfg.userID,
		sectionID:         cfg.sectionID,
		requestSampleRate: cfg.requestSampleRate,
		segmentIdle:       cfg.segmentIdle,
		events:            make(chan *platformtts.ProviderEvent, 32),
		done:              make(chan struct{}),
	}
}

func (s *realtimeSession) ID() string {
	return s.id
}

func (s *realtimeSession) AppendText(ctx context.Context, segment *platformtts.ProviderSegmentRequest) error {
	if segment == nil {
		return &platformtts.Error{
			Code:      platformtts.ErrInternal,
			Message:   "segment request is nil",
			Provider:  s.provider,
			SessionID: s.id,
		}
	}

	voice := valueOrDefault(segment.Voice, valueOrDefault(s.currentVoiceValue(), valueOrDefault(s.initialVoice, s.defaultVoice)))
	language := valueOrDefault(segment.Language, valueOrDefault(s.currentLanguageValue(), valueOrDefault(s.initialLanguage, s.defaultLanguage)))
	guidance := valueOrDefault(segment.GuidanceText, valueOrDefault(s.currentGuidanceValue(), s.initialGuidance))

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return &platformtts.Error{
			Code:      platformtts.ErrSessionClosed,
			Message:   "doubao tts session is closed",
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segment.SegmentID,
		}
	}
	s.currentSegmentID = segment.SegmentID
	s.currentSegmentIsLast = segment.IsLast
	s.currentVoice = voice
	s.currentLanguage = language
	s.currentGuidance = guidance
	s.segmentSeq++
	s.stopSegmentIdleTimerLocked()
	s.mu.Unlock()

	if !s.emit(&platformtts.ProviderEvent{
		Type:      platformtts.ProviderEventSegmentStart,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segment.SegmentID,
	}) {
		return sessionClosedError(s.provider, s.id, segment.SegmentID)
	}

	payload, err := json.Marshal(taskRequestPayload{
		User:      userPayload{UID: s.userID},
		Event:     int(EventTypeTaskRequest),
		Namespace: defaultNamespace,
		ReqParams: s.requestParams(voice, language, guidance, segment.Text),
	})
	if err != nil {
		return s.eventError(platformtts.ErrInternal, fmt.Sprintf("encode doubao task request: %v", err), segment.SegmentID, err, false)
	}
	if err := s.write(ctx, func() error { return TaskRequest(s.conn, payload, s.id) }); err != nil {
		return s.writeError(err, segment.SegmentID)
	}
	return nil
}

func (s *realtimeSession) Finish(ctx context.Context) error {
	return s.finishSessionOnce(ctx, "")
}

func (s *realtimeSession) Events() <-chan *platformtts.ProviderEvent {
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

func (s *realtimeSession) start(ctx context.Context, voice, language, guidance string) error {
	if err := s.write(ctx, func() error { return StartConnection(s.conn) }); err != nil {
		return s.writeError(err, "")
	}
	if _, err := WaitForEvent(s.conn, MsgTypeFullServerResponse, EventTypeConnectionStarted); err != nil {
		return s.writeError(err, "")
	}

	payload, err := json.Marshal(taskRequestPayload{
		User:      userPayload{UID: s.userID},
		Event:     int(EventTypeStartSession),
		Namespace: defaultNamespace,
		ReqParams: s.requestParams(valueOrDefault(voice, s.defaultVoice), language, guidance, ""),
	})
	if err != nil {
		return s.eventError(platformtts.ErrInternal, fmt.Sprintf("encode doubao start session: %v", err), "", err, false)
	}
	if err := s.write(ctx, func() error { return StartSession(s.conn, payload, s.id) }); err != nil {
		return s.writeError(err, "")
	}
	if _, err := WaitForEvent(s.conn, MsgTypeFullServerResponse, EventTypeSessionStarted); err != nil {
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
		msg, err := ReceiveMessage(s.conn)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			s.emit(&platformtts.ProviderEvent{
				Type:      platformtts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				Error:     s.writeError(err, s.segmentID()),
			})
			return
		}

		if s.handleMessage(msg) {
			return
		}
	}
}

func (s *realtimeSession) handleMessage(msg *Message) bool {
	if msg == nil {
		return false
	}

	switch msg.MsgType {
	case MsgTypeAudioOnlyServer:
		if len(msg.Payload) > 0 {
			s.emit(&platformtts.ProviderEvent{
				Type:      platformtts.ProviderEventAudio,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: s.segmentID(),
				Audio: &platformtts.ProviderAudioChunk{
					Codec:      audio.CodecOpus,
					Container:  audio.ContainerOgg,
					SampleRate: audio.OpusSampleRate,
					Channels:   audio.DefaultChannels,
					Data:       append([]byte(nil), msg.Payload...),
				},
			})
		}
		if msg.MsgTypeFlag == MsgTypeFlagNegativeSeq || msg.MsgTypeFlag == MsgTypeFlagLastNoSeq {
			s.scheduleSegmentEnd(msg.meta(), s.segmentEndGrace(), "audio_final")
		} else if len(msg.Payload) > 0 {
			s.scheduleSegmentIdleEnd(msg.meta())
		}
		return false
	case MsgTypeError:
		s.emit(&platformtts.ProviderEvent{
			Type:      platformtts.ProviderEventError,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: s.segmentID(),
			Error:     s.eventError(platformtts.ErrProviderUnavailable, fmt.Sprintf("doubao error %d: %s", msg.ErrorCode, string(msg.Payload)), s.segmentID(), nil, false),
		})
		return true
	}

	switch msg.EventType {
	case EventTypeTTSResponse:
		if len(msg.Payload) > 0 {
			s.emit(&platformtts.ProviderEvent{
				Type:      platformtts.ProviderEventAudio,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: s.segmentID(),
				RawMeta:   msg.meta(),
				Audio: &platformtts.ProviderAudioChunk{
					Codec:      audio.CodecOpus,
					Container:  audio.ContainerOgg,
					SampleRate: audio.OpusSampleRate,
					Channels:   audio.DefaultChannels,
					Data:       append([]byte(nil), msg.Payload...),
				},
			})
			s.scheduleSegmentIdleEnd(msg.meta())
		}
	case EventTypeTTSSentenceEnd:
		s.scheduleSegmentEnd(msg.meta(), s.segmentEndGrace(), "sentence_end")
	case EventTypeSessionFinished:
		s.endCurrentSegment(msg.meta())
		s.emit(&platformtts.ProviderEvent{
			Type:      platformtts.ProviderEventSessionEnd,
			Provider:  s.provider,
			SessionID: s.id,
			RawMeta:   msg.meta(),
		})
		_ = s.write(context.Background(), func() error { return FinishConnection(s.conn) })
		_, _ = WaitForEvent(s.conn, MsgTypeFullServerResponse, EventTypeConnectionFinished)
		return true
	case EventTypeSessionFailed, EventTypeConnectionFailed:
		s.emit(&platformtts.ProviderEvent{
			Type:      platformtts.ProviderEventError,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: s.segmentID(),
			RawMeta:   msg.meta(),
			Error:     s.eventError(platformtts.ErrSegmentFailed, string(msg.Payload), s.segmentID(), nil, false),
		})
		return true
	}
	return false
}

func (s *realtimeSession) endCurrentSegment(meta map[string]any) {
	segmentID := s.clearSegmentID()
	if segmentID == "" {
		return
	}
	s.emit(&platformtts.ProviderEvent{
		Type:      platformtts.ProviderEventSegmentEnd,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		RawMeta:   meta,
	})
}

func (s *realtimeSession) scheduleSegmentIdleEnd(meta map[string]any) {
	s.scheduleSegmentEnd(meta, s.segmentIdle, "audio_idle")
}

func (s *realtimeSession) scheduleSegmentEnd(meta map[string]any, timeout time.Duration, reason string) {
	s.mu.Lock()
	if timeout <= 0 || s.currentSegmentID == "" {
		s.mu.Unlock()
		return
	}
	segmentID := s.currentSegmentID
	segmentSeq := s.segmentSeq
	s.stopSegmentIdleTimerLocked()
	s.segmentIdleTimer = time.AfterFunc(timeout, func() {
		idleMeta := cloneMeta(meta)
		if idleMeta == nil {
			idleMeta = make(map[string]any)
		}
		idleMeta["segment_end_reason"] = reason
		s.endSegmentIfCurrent(segmentID, segmentSeq, idleMeta)
	})
	s.mu.Unlock()
}

func (s *realtimeSession) segmentEndGrace() time.Duration {
	if s.segmentIdle <= 0 {
		return 0
	}
	if s.segmentIdle < 200*time.Millisecond {
		return s.segmentIdle
	}
	return 200 * time.Millisecond
}

func (s *realtimeSession) endSegmentIfCurrent(segmentID string, segmentSeq uint64, meta map[string]any) {
	s.mu.Lock()
	if s.currentSegmentID != segmentID || s.segmentSeq != segmentSeq {
		s.mu.Unlock()
		return
	}
	if s.currentSegmentIsLast {
		s.stopSegmentIdleTimerLocked()
		s.mu.Unlock()
		if err := s.finishSessionOnce(context.Background(), segmentID); err != nil {
			ttsErr, ok := err.(*platformtts.Error)
			if !ok {
				ttsErr = s.writeError(err, segmentID)
			}
			s.emit(&platformtts.ProviderEvent{
				Type:      platformtts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: segmentID,
				RawMeta:   meta,
				Error:     ttsErr,
			})
		}
		return
	}
	s.currentSegmentID = ""
	s.currentSegmentIsLast = false
	s.stopSegmentIdleTimerLocked()
	s.mu.Unlock()

	s.emit(&platformtts.ProviderEvent{
		Type:      platformtts.ProviderEventSegmentEnd,
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

	if err := s.write(ctx, func() error { return FinishSession(s.conn, s.id) }); err != nil {
		return s.writeError(err, segmentID)
	}
	return nil
}

func (s *realtimeSession) requestParams(voice, language, guidance, text string) requestParams {
	styles := make([]string, 0, 1)
	if guidance != "" {
		styles = append(styles, guidance)
	}
	additions, _ := json.Marshal(additionsPayload{
		DisableMarkdownFilter:        true,
		EnableLatexTN:                true,
		LatexParser:                  true,
		MaxLengthToFilterParenthesis: 100,
		EnableLanguageDetector:       true,
		ContextTexts:                 styles,
		SectionID:                    s.sectionID,
		ExplicitLanguage:             normalizeDoubaoLanguage(language),
	})
	params := requestParams{
		Speaker: voice,
		AudioParams: audioParams{
			Format:          "ogg_opus",
			SampleRate:      s.requestSampleRate,
			EnableTimestamp: false,
		},
		Additions: string(additions),
	}
	if text != "" {
		params.Text = text
	}
	return params
}

func (s *realtimeSession) write(ctx context.Context, fn func() error) error {
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
	return fn()
}

func (s *realtimeSession) emit(event *platformtts.ProviderEvent) bool {
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

func (s *realtimeSession) currentVoiceValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentVoice
}

func (s *realtimeSession) currentLanguageValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLanguage
}

func (s *realtimeSession) currentGuidanceValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentGuidance
}

func (s *realtimeSession) writeError(err error, segmentID string) *platformtts.Error {
	return s.eventError(platformtts.ErrProviderUnavailable, err.Error(), segmentID, err, true)
}

func (s *realtimeSession) eventError(code platformtts.ErrorCode, message, segmentID string, cause error, retryable bool) *platformtts.Error {
	return &platformtts.Error{
		Code:      code,
		Message:   message,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		Cause:     cause,
		Retryable: retryable,
	}
}

func sessionClosedError(provider, sessionID, segmentID string) *platformtts.Error {
	return &platformtts.Error{
		Code:      platformtts.ErrSessionClosed,
		Message:   "doubao tts session is closed",
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

func normalizeDoubaoLanguage(language string) string {
	switch strings.ToLower(language) {
	case "zh", "zh-cn", "chinese":
		return "zh-cn"
	case "en", "english":
		return "en"
	case "ja", "japanese":
		return "ja"
	case "ko", "korean":
		return "ko"
	case "es", "es-mx", "spanish":
		return "es-mx"
	case "id", "indonesian":
		return "id"
	case "pt", "pt-br", "portuguese":
		return "pt-br"
	default:
		return ""
	}
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := make([]byte, 32)
	hex.Encode(hexed, b[:])
	return fmt.Sprintf("%s_%s-%s-%s-%s-%s", prefix, hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32])
}

func (m *Message) meta() map[string]any {
	if m == nil {
		return nil
	}
	return map[string]any{
		"event_type": m.EventType.String(),
		"msg_type":   m.MsgType.String(),
		"session_id": m.SessionID,
		"connect_id": m.ConnectID,
	}
}

func cloneMeta(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	cloned := make(map[string]any, len(meta))
	for key, value := range meta {
		cloned[key] = value
	}
	return cloned
}

type taskRequestPayload struct {
	User      userPayload   `json:"user"`
	Event     int           `json:"event"`
	Namespace string        `json:"namespace"`
	ReqParams requestParams `json:"req_params"`
}

type userPayload struct {
	UID string `json:"uid"`
}

type requestParams struct {
	Model       string      `json:"model,omitempty"`
	Speaker     string      `json:"speaker"`
	AudioParams audioParams `json:"audio_params"`
	Additions   string      `json:"additions,omitempty"`
	Text        string      `json:"text,omitempty"`
}

type audioParams struct {
	Format          string `json:"format"`
	SampleRate      int    `json:"sample_rate"`
	EnableTimestamp bool   `json:"enable_timestamp"`
}

type additionsPayload struct {
	DisableMarkdownFilter        bool     `json:"disable_markdown_filter"`
	EnableLatexTN                bool     `json:"enable_latex_tn"`
	LatexParser                  bool     `json:"latex_parser"`
	MaxLengthToFilterParenthesis int      `json:"max_length_to_filter_parenthesis"`
	EnableLanguageDetector       bool     `json:"enable_language_detector"`
	ContextTexts                 []string `json:"context_texts,omitempty"`
	SectionID                    string   `json:"section_id,omitempty"`
	ExplicitLanguage             string   `json:"explicit_language,omitempty"`
}
