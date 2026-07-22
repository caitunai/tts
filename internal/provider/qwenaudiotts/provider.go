package qwenaudiotts

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/caitunai/tts/internal/audio"
	langnorm "github.com/caitunai/tts/internal/language"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

const (
	defaultProviderName = "qwen_audio_tts"
	defaultModel        = "qwen-audio-3.0-tts-flash"
	defaultFormat       = "opus"
	defaultTextType     = "PlainText"
	defaultVolume       = 50
	defaultRate         = 1.0
	defaultPitch        = 1.0
	defaultBitRate      = 32
	defaultFinishDelay  = 500 * time.Millisecond

	writeTimeout = 30 * time.Second
)

// Config configures Alibaba Cloud Qwen Audio 3.0 realtime WebSocket TTS.
type Config struct {
	Name string

	Endpoint      string
	APIKey        string
	Authorization string

	Model               string
	DefaultVoice        string
	DefaultLanguage     string
	DefaultInstructions string

	SampleRate int
	BitRate    int
	Volume     int
	Rate       float64
	Pitch      float64
	EnableSSML bool

	LanguageHints []string

	DataInspection string
	FinishDelay    time.Duration

	Dialer *websocket.Dialer
}

// Provider adapts Qwen Audio's raw WebSocket TTS protocol to the TTS Provider
// interface.
type Provider struct {
	name string

	endpoint      string
	apiKey        string
	authorization string

	model               string
	defaultVoice        string
	defaultLanguage     string
	defaultInstructions string

	sampleRate int
	bitRate    int
	volume     int
	rate       float64
	pitch      float64
	enableSSML bool

	languageHints  []string
	dataInspection string
	finishDelay    time.Duration

	dialer *websocket.Dialer
}

// NewProvider creates a Qwen Audio realtime WebSocket TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen audio tts endpoint is required",
			Provider: cfg.Name,
		}
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = audio.OpusSampleRate
	}
	if cfg.SampleRate != audio.OpusSampleRate {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen audio opus sample rate must be 48000",
			Provider: cfg.Name,
		}
	}
	if cfg.BitRate == 0 {
		cfg.BitRate = defaultBitRate
	}
	if cfg.BitRate < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen audio bit rate must be positive",
			Provider: cfg.Name,
		}
	}
	if cfg.Volume == 0 {
		cfg.Volume = defaultVolume
	}
	if cfg.Volume < 0 || cfg.Volume > 100 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen audio volume must be between 0 and 100",
			Provider: cfg.Name,
		}
	}
	if cfg.Rate == 0 {
		cfg.Rate = defaultRate
	}
	if cfg.Pitch == 0 {
		cfg.Pitch = defaultPitch
	}
	if cfg.Rate < 0.5 || cfg.Rate > 2 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen audio rate must be between 0.5 and 2.0",
			Provider: cfg.Name,
		}
	}
	if cfg.Pitch < 0.5 || cfg.Pitch > 2 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen audio pitch must be between 0.5 and 2.0",
			Provider: cfg.Name,
		}
	}
	if cfg.DataInspection == "" {
		cfg.DataInspection = "enable"
	}
	if cfg.FinishDelay < 0 {
		cfg.FinishDelay = defaultFinishDelay
	}
	if cfg.Dialer == nil {
		cfg.Dialer = websocket.DefaultDialer
	}

	return &Provider{
		name:                cfg.Name,
		endpoint:            cfg.Endpoint,
		apiKey:              cfg.APIKey,
		authorization:       cfg.Authorization,
		model:               cfg.Model,
		defaultVoice:        cfg.DefaultVoice,
		defaultLanguage:     cfg.DefaultLanguage,
		defaultInstructions: cfg.DefaultInstructions,
		sampleRate:          cfg.SampleRate,
		bitRate:             cfg.BitRate,
		volume:              cfg.Volume,
		rate:                cfg.Rate,
		pitch:               cfg.Pitch,
		enableSSML:          cfg.EnableSSML,
		languageHints:       append([]string(nil), cfg.LanguageHints...),
		dataInspection:      cfg.DataInspection,
		finishDelay:         cfg.FinishDelay,
		dialer:              cfg.Dialer,
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
		SupportsSSML:            true,
		SupportsGuidanceText:    true,
		SupportsSpeed:           true,
		SupportsPitch:           true,
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
		Message:  "qwen audio tts provider only supports websocket sessions",
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
	if auth := p.authHeader(); auth != "" {
		header.Set("Authorization", auth)
	}
	if p.dataInspection != "" {
		header.Set("X-DashScope-DataInspection", p.dataInspection)
	}

	conn, resp, err := p.dialer.DialContext(ctx, p.endpoint, header)
	if err != nil {
		if resp != nil && resp.Body != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(body)))
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
		sessionID = newID("qwen_audio")
	}
	taskID := newUUID()
	session := newSession(sessionConfig{
		id:          sessionID,
		taskID:      taskID,
		provider:    p.name,
		conn:        conn,
		finishDelay: p.finishDelay,
	})

	go session.readLoop()
	runTask := p.runTaskRequest(taskID, req)
	if err := session.runTask(ctx, runTask); err != nil {
		_ = session.Close()
		return nil, err
	}
	if err := session.waitStarted(ctx); err != nil {
		_ = session.Close()
		return nil, err
	}
	return session, nil
}

func (p *Provider) runTaskRequest(taskID string, req *tts.ProviderOpenSessionRequest) protocolMessage {
	languageHints := p.languageHintsFor(req.Language)
	return protocolMessage{
		Header: protocolHeader{
			Action:    "run-task",
			TaskID:    taskID,
			Streaming: "duplex",
		},
		Payload: protocolPayload{
			TaskGroup: "audio",
			Task:      "tts",
			Function:  "SpeechSynthesizer",
			Model:     p.model,
			Parameters: &protocolParameters{
				TextType:      defaultTextType,
				Voice:         valueOrDefault(req.Voice, p.defaultVoice),
				Format:        defaultFormat,
				SampleRate:    p.sampleRate,
				BitRate:       p.bitRate,
				Volume:        p.volume,
				Rate:          p.rate,
				Pitch:         p.pitch,
				EnableSSML:    p.enableSSML,
				Instruction:   valueOrDefault(req.GuidanceText, p.defaultInstructions),
				LanguageHints: languageHints,
			},
			Input: map[string]any{},
		},
	}
}

func (p *Provider) languageHintsFor(language string) []string {
	if language != "" {
		return []string{normalizeLanguage(language)}
	}
	if len(p.languageHints) > 0 {
		return append([]string(nil), p.languageHints...)
	}
	if p.defaultLanguage != "" {
		return []string{normalizeLanguage(p.defaultLanguage)}
	}
	return nil
}

func (p *Provider) authHeader() string {
	if p.authorization != "" {
		return p.authorization
	}
	if p.apiKey != "" {
		return "bearer " + p.apiKey
	}
	return ""
}

type sessionConfig struct {
	id          string
	taskID      string
	provider    string
	conn        *websocket.Conn
	finishDelay time.Duration
}

type session struct {
	id       string
	taskID   string
	provider string
	conn     *websocket.Conn

	finishDelay time.Duration

	mu                    sync.Mutex
	currentSegmentID      string
	currentSegmentEnded   bool
	endedSegments         map[string]bool
	pendingAudioSegmentID string
	activeAudioSegmentID  string
	audioSegmentQueue     []string
	lastSegmentID         string
	startErrValue         error
	startedFlag           bool
	closed                bool

	writeMu sync.Mutex

	events chan *tts.ProviderEvent
	done   chan struct{}

	startOnce sync.Once
	started   chan struct{}
	startErr  chan error

	finishOnce sync.Once
	finishErr  error
	closeOnce  sync.Once
}

func newSession(cfg sessionConfig) *session {
	return &session{
		id:            cfg.id,
		taskID:        cfg.taskID,
		provider:      cfg.provider,
		conn:          cfg.conn,
		finishDelay:   cfg.finishDelay,
		events:        make(chan *tts.ProviderEvent, 32),
		done:          make(chan struct{}),
		started:       make(chan struct{}),
		startErr:      make(chan error, 1),
		endedSegments: make(map[string]bool),
	}
}

func (s *session) ID() string {
	return s.id
}

func (s *session) AppendText(ctx context.Context, segment *tts.ProviderSegmentRequest) error {
	if segment == nil {
		return &tts.Error{
			Code:     tts.ErrInternal,
			Message:  "segment request is nil",
			Provider: s.provider,
		}
	}
	if err := s.waitStarted(ctx); err != nil {
		return err
	}

	segmentID := segment.SegmentID
	if segmentID == "" {
		segmentID = newID("seg")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return &tts.Error{
			Code:      tts.ErrSessionClosed,
			Message:   "session is closed",
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segmentID,
		}
	}
	s.currentSegmentID = segmentID
	s.currentSegmentEnded = false
	s.audioSegmentQueue = append(s.audioSegmentQueue, segmentID)
	s.mu.Unlock()

	s.emit(&tts.ProviderEvent{
		Type:           tts.ProviderEventSegmentStart,
		Provider:       s.provider,
		SessionID:      s.id,
		SegmentID:      segmentID,
		ProviderTaskID: s.taskID,
	})

	msg := protocolMessage{
		Header: protocolHeader{
			Action:    "continue-task",
			TaskID:    s.taskID,
			Streaming: "duplex",
		},
		Payload: protocolPayload{
			Input: map[string]any{
				"text": segment.Text,
			},
		},
	}
	if err := s.writeJSON(ctx, msg); err != nil {
		return err
	}
	if segment.IsLast {
		if s.finishDelay > 0 {
			timer := time.NewTimer(s.finishDelay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return &tts.Error{
					Code:      tts.ErrProviderTimeout,
					Message:   ctx.Err().Error(),
					Provider:  s.provider,
					SessionID: s.id,
					SegmentID: segmentID,
					Cause:     ctx.Err(),
					Retryable: true,
				}
			case <-s.done:
				if err := s.startError(); err != nil {
					return err
				}
				return nil
			}
		}
		return s.Finish(ctx)
	}
	s.emitSegmentEnd(segmentID)
	return nil
}

func (s *session) Finish(ctx context.Context) error {
	s.finishOnce.Do(func() {
		msg := protocolMessage{
			Header: protocolHeader{
				Action:    "finish-task",
				TaskID:    s.taskID,
				Streaming: "duplex",
			},
			Payload: protocolPayload{
				Input: map[string]any{},
			},
		}
		s.finishErr = s.writeJSON(ctx, msg)
	})
	return s.finishErr
}

func (s *session) Events() <-chan *tts.ProviderEvent {
	return s.events
}

func (s *session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.done)
		}
		s.mu.Unlock()
		if s.conn != nil {
			_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			err = s.conn.Close()
		}
	})
	return err
}

func (s *session) runTask(ctx context.Context, msg protocolMessage) error {
	return s.writeJSON(ctx, msg)
}

func (s *session) waitStarted(ctx context.Context) error {
	select {
	case <-s.started:
		return nil
	case err := <-s.startErr:
		return err
	case <-ctx.Done():
		return &tts.Error{
			Code:      tts.ErrProviderTimeout,
			Message:   ctx.Err().Error(),
			Provider:  s.provider,
			SessionID: s.id,
			Cause:     ctx.Err(),
			Retryable: true,
		}
	case <-s.done:
		if err := s.startError(); err != nil {
			return err
		}
		return &tts.Error{
			Code:      tts.ErrSessionClosed,
			Message:   "session is closed before task started",
			Provider:  s.provider,
			SessionID: s.id,
		}
	}
}

func (s *session) startError() error {
	s.mu.Lock()
	err := s.startErrValue
	s.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case err := <-s.startErr:
		return err
	default:
		return nil
	}
}

func (s *session) isStarted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startedFlag
}

func (s *session) writeJSON(ctx context.Context, value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if deadline, ok := ctx.Deadline(); ok {
		_ = s.conn.SetWriteDeadline(deadline)
	} else {
		_ = s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	}
	if err := s.conn.WriteJSON(value); err != nil {
		return &tts.Error{
			Code:      tts.ErrProviderUnavailable,
			Message:   err.Error(),
			Provider:  s.provider,
			SessionID: s.id,
			Cause:     err,
			Retryable: true,
		}
	}
	return nil
}

func (s *session) readLoop() {
	defer func() {
		s.markClosed()
		close(s.events)
	}()

	for {
		messageType, message, err := s.conn.ReadMessage()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			s.signalStartError(&tts.Error{
				Code:      tts.ErrProviderUnavailable,
				Message:   err.Error(),
				Provider:  s.provider,
				SessionID: s.id,
				Cause:     err,
				Retryable: true,
			})
			s.emitError("", err.Error(), err, true)
			return
		}

		switch messageType {
		case websocket.TextMessage:
			if !s.handleTextMessage(message) {
				return
			}
		case websocket.BinaryMessage:
			s.handleAudioMessage(message)
		}
	}
}

func (s *session) handleTextMessage(message []byte) bool {
	var msg protocolMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		s.emitError("", "decode qwen audio websocket event: "+err.Error(), err, false)
		return true
	}

	event := msg.Header.Event
	if event == "" {
		event = msg.Header.Action
	}
	switch event {
	case "task-started":
		s.startOnce.Do(func() {
			s.mu.Lock()
			s.startedFlag = true
			s.mu.Unlock()
			close(s.started)
			s.emit(&tts.ProviderEvent{
				Type:           tts.ProviderEventSessionStart,
				Provider:       s.provider,
				SessionID:      s.id,
				ProviderTaskID: s.taskID,
				RawMeta:        rawMeta(msg),
			})
		})
	case "result-generated":
		s.handleResultGenerated(msg)
	case "task-finished":
		if !s.isStarted() {
			s.signalStartError(&tts.Error{
				Code:      tts.ErrProviderUnavailable,
				Message:   "qwen audio task finished before task-started",
				Provider:  s.provider,
				SessionID: s.id,
				Retryable: false,
			})
		}
		s.emitSegmentEndIfNeeded()
		s.emit(&tts.ProviderEvent{
			Type:           tts.ProviderEventSessionEnd,
			Provider:       s.provider,
			SessionID:      s.id,
			ProviderTaskID: s.taskID,
			RawMeta:        rawMeta(msg),
		})
		return false
	case "task-failed":
		message := msg.Header.ErrorMessage
		if message == "" {
			message = "qwen audio task failed"
		}
		err := &tts.Error{
			Code:      tts.ErrProviderUnavailable,
			Message:   message,
			Provider:  s.provider,
			SessionID: s.id,
			Retryable: false,
		}
		s.signalStartError(err)
		s.emit(&tts.ProviderEvent{
			Type:           tts.ProviderEventError,
			Provider:       s.provider,
			SessionID:      s.id,
			ProviderTaskID: s.taskID,
			RawMeta:        rawMeta(msg),
			Error:          err,
		})
		return false
	}
	return true
}

func (s *session) handleResultGenerated(msg protocolMessage) {
	if msg.Payload.Output == nil {
		return
	}
	switch msg.Payload.Output.Type {
	case "sentence-begin":
		s.beginAudioSegment()
	case "sentence-synthesis":
		s.mu.Lock()
		s.pendingAudioSegmentID = s.audioSegmentIDLocked()
		s.mu.Unlock()
	case "sentence-end":
		if segmentID := s.currentAudioSegmentID(); segmentID != "" {
			s.emitSegmentEnd(segmentID)
		} else {
			s.emitSegmentEndIfNeeded()
		}
		s.clearAudioSegment()
	}
}

func (s *session) handleAudioMessage(message []byte) {
	if len(message) == 0 {
		return
	}
	data := make([]byte, len(message))
	copy(data, message)

	segmentID := s.audioSegmentID()
	s.emit(&tts.ProviderEvent{
		Type:           tts.ProviderEventAudio,
		Provider:       s.provider,
		SessionID:      s.id,
		SegmentID:      segmentID,
		ProviderTaskID: s.taskID,
		Audio: &tts.ProviderAudioChunk{
			Codec:      audio.CodecOpus,
			Container:  audio.ContainerOgg,
			SampleRate: audio.OpusSampleRate,
			Channels:   audio.DefaultChannels,
			Data:       data,
		},
	})
}

func (s *session) audioSegmentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pendingAudioSegmentID != "" {
		segmentID := s.pendingAudioSegmentID
		s.pendingAudioSegmentID = ""
		s.lastSegmentID = segmentID
		return segmentID
	}
	if s.currentSegmentID != "" {
		s.lastSegmentID = s.currentSegmentID
		return s.currentSegmentID
	}
	return s.lastSegmentID
}

func (s *session) beginAudioSegment() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeAudioSegmentID != "" {
		return
	}
	if len(s.audioSegmentQueue) == 0 {
		s.activeAudioSegmentID = s.currentSegmentID
		return
	}
	s.activeAudioSegmentID = s.audioSegmentQueue[0]
	s.audioSegmentQueue = s.audioSegmentQueue[1:]
}

func (s *session) clearAudioSegment() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeAudioSegmentID != "" {
		s.lastSegmentID = s.activeAudioSegmentID
	}
	s.activeAudioSegmentID = ""
	s.pendingAudioSegmentID = ""
}

func (s *session) currentAudioSegmentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeAudioSegmentID
}

func (s *session) audioSegmentIDLocked() string {
	if s.activeAudioSegmentID != "" {
		s.lastSegmentID = s.activeAudioSegmentID
		return s.activeAudioSegmentID
	}
	if len(s.audioSegmentQueue) > 0 {
		s.activeAudioSegmentID = s.audioSegmentQueue[0]
		s.audioSegmentQueue = s.audioSegmentQueue[1:]
		s.lastSegmentID = s.activeAudioSegmentID
		return s.activeAudioSegmentID
	}
	if s.currentSegmentID != "" {
		s.lastSegmentID = s.currentSegmentID
		return s.currentSegmentID
	}
	return s.lastSegmentID
}

func (s *session) emitSegmentEndIfNeeded() {
	s.mu.Lock()
	segmentID := s.currentSegmentID
	if segmentID == "" || s.currentSegmentEnded {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.emitSegmentEnd(segmentID)
}

func (s *session) emitSegmentEnd(segmentID string) {
	s.mu.Lock()
	if segmentID == "" || s.endedSegments[segmentID] {
		s.mu.Unlock()
		return
	}
	s.endedSegments[segmentID] = true
	if segmentID == s.currentSegmentID {
		s.currentSegmentEnded = true
		s.lastSegmentID = segmentID
		s.currentSegmentID = ""
	}
	s.mu.Unlock()

	s.emit(&tts.ProviderEvent{
		Type:           tts.ProviderEventSegmentEnd,
		Provider:       s.provider,
		SessionID:      s.id,
		SegmentID:      segmentID,
		ProviderTaskID: s.taskID,
	})
}

func (s *session) markClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
}

func (s *session) signalStartError(err error) {
	s.startOnce.Do(func() {
		s.mu.Lock()
		s.startErrValue = err
		s.mu.Unlock()
		select {
		case s.startErr <- err:
		default:
		}
	})
}

func (s *session) emitError(segmentID, message string, cause error, retryable bool) {
	s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventError,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segmentID,
		Error: &tts.Error{
			Code:      tts.ErrProviderUnavailable,
			Message:   message,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segmentID,
			Cause:     cause,
			Retryable: retryable,
		},
	})
}

func (s *session) emit(event *tts.ProviderEvent) {
	select {
	case <-s.done:
	case s.events <- event:
	}
}

type protocolMessage struct {
	Header  protocolHeader  `json:"header"`
	Payload protocolPayload `json:"payload"`
}

type protocolHeader struct {
	Action       string         `json:"action,omitempty"`
	Event        string         `json:"event,omitempty"`
	TaskID       string         `json:"task_id,omitempty"`
	Streaming    string         `json:"streaming,omitempty"`
	ErrorCode    string         `json:"error_code,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

type protocolPayload struct {
	TaskGroup  string              `json:"task_group,omitempty"`
	Task       string              `json:"task,omitempty"`
	Function   string              `json:"function,omitempty"`
	Model      string              `json:"model,omitempty"`
	Parameters *protocolParameters `json:"parameters,omitempty"`
	Input      map[string]any      `json:"input"`
	Output     *protocolOutput     `json:"output,omitempty"`
	Usage      map[string]any      `json:"usage,omitempty"`
}

type protocolParameters struct {
	TextType      string   `json:"text_type,omitempty"`
	Voice         string   `json:"voice,omitempty"`
	Format        string   `json:"format,omitempty"`
	SampleRate    int      `json:"sample_rate,omitempty"`
	BitRate       int      `json:"bit_rate,omitempty"`
	Volume        int      `json:"volume,omitempty"`
	Rate          float64  `json:"rate,omitempty"`
	Pitch         float64  `json:"pitch,omitempty"`
	EnableSSML    bool     `json:"enable_ssml"`
	Instruction   string   `json:"instruction,omitempty"`
	LanguageHints []string `json:"language_hints,omitempty"`
}

type protocolOutput struct {
	Type string `json:"type,omitempty"`
}

func rawMeta(msg protocolMessage) map[string]any {
	return map[string]any{
		"header":  msg.Header,
		"payload": msg.Payload,
	}
}

var providerLanguageMapper = langnorm.NewMapper(
	langnorm.Map("zh", langnorm.MatchLanguage, "zh", "cmn", "yue"),
)

func normalizeLanguage(lang string) string {
	return providerLanguageMapper.Resolve(lang, langnorm.Primary(lang))
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

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return newID("task")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
