package minimaxtts

import (
	"context"
	"encoding/hex"
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
	defaultProviderName = "minimax_tts"
	defaultModel        = "speech-2.8-turbo"
	defaultSampleRate   = 16000
	defaultBitrate      = 128000
	defaultChannels     = 2
	defaultSpeed        = 1.0
	defaultVolume       = 1
	defaultPitch        = 0

	Chinese    = "Chinese"
	Yue        = "Chinese,Yue"
	English    = "English"
	Arabic     = "Arabic"
	Russian    = "Russian"
	Spanish    = "Spanish"
	French     = "French"
	Portuguese = "Portuguese"
	German     = "German"
	Turkish    = "Turkish"
	Dutch      = "Dutch"
	Ukrainian  = "Ukrainian"
	Vietnamese = "Vietnamese"
	Indonesian = "Indonesian"
	Japanese   = "Japanese"
	Italian    = "Italian"
	Korean     = "Korean"
	Thai       = "Thai"
	Polish     = "Polish"
	Romanian   = "Romanian"
	Greek      = "Greek"
	Czech      = "Czech"
	Finnish    = "Finnish"
	Hindi      = "Hindi"
	Bulgarian  = "Bulgarian"
	Danish     = "Danish"
	Hebrew     = "Hebrew"
	Malay      = "Malay"
	Persian    = "Persian"
	Slovak     = "Slovak"
	Swedish    = "Swedish"
	Croatian   = "Croatian"
	Filipino   = "Filipino"
	Hungarian  = "Hungarian"
	Norwegian  = "Norwegian"
	Slovenian  = "Slovenian"
	Catalan    = "Catalan"
	Nynorsk    = "Nynorsk"
	Tamil      = "Tamil"
	Afrikaans  = "Afrikaans"
	Auto       = "auto"
)

// Emotion is a Minimax voice emotion value.
type Emotion string

const (
	EmotionUnknown   Emotion = ""
	EmotionHappy     Emotion = "happy"
	EmotionSad       Emotion = "sad"
	EmotionAngry     Emotion = "angry"
	EmotionFearful   Emotion = "fearful"
	EmotionDisgusted Emotion = "disgusted"
	EmotionSurprised Emotion = "surprised"
	EmotionCalm      Emotion = "calm"
	EmotionFluent    Emotion = "fluent"
	EmotionWhisper   Emotion = "whisper"
)

// Config configures a Minimax realtime WebSocket TTS provider.
type Config struct {
	Name string

	Endpoint string
	Token    string

	Model           string
	DefaultVoice    string
	DefaultLanguage string

	SampleRate int
	Bitrate    int
	Channels   int
}

// Provider adapts Minimax realtime WebSocket TTS to the TTS Provider interface.
type Provider struct {
	name string

	endpoint string
	token    string

	model           string
	defaultVoice    string
	defaultLanguage string

	sampleRate int
	bitrate    int
	channels   int
}

// NewProvider creates a Minimax realtime TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "minimax tts endpoint is required",
			Provider: cfg.Name,
		}
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = defaultSampleRate
	}
	if cfg.Bitrate == 0 {
		cfg.Bitrate = defaultBitrate
	}
	if cfg.Channels == 0 {
		cfg.Channels = defaultChannels
	}
	if cfg.SampleRate < 0 || cfg.Bitrate < 0 || cfg.Channels < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "minimax tts audio settings must be positive",
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
		bitrate:         cfg.Bitrate,
		channels:        cfg.Channels,
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
		SupportsSegmentEndEvent:      true,
		SupportsPCMOutput:            true,
		OutputCodecs:                 []audio.Codec{audio.CodecMP3, audio.CodecPCM},
		OutputContainers:             []audio.Container{audio.ContainerRaw},
		OutputSampleRates:            []int{p.sampleRate},
		OutputChannels:               []int{audio.DefaultChannels},
		SupportsSegmentLevelGuidance: false,
	}
	return caps, nil
}

func (p *Provider) SynthesizeOnce(context.Context, *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	return nil, &tts.Error{
		Code:     tts.ErrUnsupportedFeature,
		Message:  "minimax tts provider only supports sessions",
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
		sessionID = newEventID("sess")
	}
	session := newRealtimeSession(realtimeSessionConfig{
		id:              sessionID,
		provider:        p.name,
		conn:            conn,
		model:           p.model,
		defaultVoice:    p.defaultVoice,
		defaultLanguage: p.defaultLanguage,
		initialVoice:    req.Voice,
		initialLanguage: req.Language,
		initialStyle:    req.GuidanceText,
		sampleRate:      p.sampleRate,
		bitrate:         p.bitrate,
		channels:        p.channels,
	})
	go session.readLoop()
	return session, nil
}

var providerLanguageMapper = langnorm.NewMapper(
	langnorm.Map(Chinese, langnorm.MatchLanguage, "zh", "cmn"),
	langnorm.Map(Yue, langnorm.MatchLanguage, "yue"),
	langnorm.Map(English, langnorm.MatchLanguage, "en"),
	langnorm.Map(Arabic, langnorm.MatchLanguage, "ar", "arz"),
	langnorm.Map(Russian, langnorm.MatchLanguage, "ru"),
	langnorm.Map(Spanish, langnorm.MatchLanguage, "es"),
	langnorm.Map(French, langnorm.MatchLanguage, "fr"),
	langnorm.Map(Portuguese, langnorm.MatchLanguage, "pt"),
	langnorm.Map(German, langnorm.MatchLanguage, "de"),
	langnorm.Map(Turkish, langnorm.MatchLanguage, "tr"),
	langnorm.Map(Dutch, langnorm.MatchLanguage, "nl"),
	langnorm.Map(Ukrainian, langnorm.MatchLanguage, "uk"),
	langnorm.Map(Vietnamese, langnorm.MatchLanguage, "vi"),
	langnorm.Map(Indonesian, langnorm.MatchLanguage, "id"),
	langnorm.Map(Japanese, langnorm.MatchLanguage, "ja"),
	langnorm.Map(Italian, langnorm.MatchLanguage, "it"),
	langnorm.Map(Korean, langnorm.MatchLanguage, "ko"),
	langnorm.Map(Thai, langnorm.MatchLanguage, "th"),
	langnorm.Map(Polish, langnorm.MatchLanguage, "pl"),
	langnorm.Map(Romanian, langnorm.MatchLanguage, "ro"),
	langnorm.Map(Greek, langnorm.MatchLanguage, "el"),
	langnorm.Map(Czech, langnorm.MatchLanguage, "cs"),
	langnorm.Map(Finnish, langnorm.MatchLanguage, "fi"),
	langnorm.Map(Hindi, langnorm.MatchLanguage, "hi"),
	langnorm.Map(Bulgarian, langnorm.MatchLanguage, "bg"),
	langnorm.Map(Danish, langnorm.MatchLanguage, "da"),
	langnorm.Map(Hebrew, langnorm.MatchLanguage, "he"),
	langnorm.Map(Malay, langnorm.MatchLanguage, "ms"),
	langnorm.Map(Persian, langnorm.MatchLanguage, "fa"),
	langnorm.Map(Slovak, langnorm.MatchLanguage, "sk"),
	langnorm.Map(Swedish, langnorm.MatchLanguage, "sv"),
	langnorm.Map(Croatian, langnorm.MatchLanguage, "hr"),
	langnorm.Map(Filipino, langnorm.MatchLanguage, "fil", "tl"),
	langnorm.Map(Hungarian, langnorm.MatchLanguage, "hu"),
	langnorm.Map(Nynorsk, langnorm.MatchLanguage, "nn"),
	langnorm.Map(Norwegian, langnorm.MatchLanguage, "no"),
	langnorm.Map(Slovenian, langnorm.MatchLanguage, "sl"),
	langnorm.Map(Catalan, langnorm.MatchLanguage, "ca"),
	langnorm.Map(Tamil, langnorm.MatchLanguage, "ta"),
	langnorm.Map(Afrikaans, langnorm.MatchLanguage, "af"),
)

func rewriteLang(lang string) string {
	return providerLanguageMapper.Resolve(lang, Auto)
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

type realtimeSessionConfig struct {
	id       string
	provider string
	conn     *websocket.Conn

	model           string
	defaultVoice    string
	defaultLanguage string
	initialVoice    string
	initialLanguage string
	initialStyle    string

	sampleRate int
	bitrate    int
	channels   int
}

type realtimeSession struct {
	id       string
	provider string
	conn     *websocket.Conn

	model           string
	defaultVoice    string
	defaultLanguage string
	initialVoice    string
	initialLanguage string
	initialStyle    string
	sampleRate      int
	bitrate         int
	channels        int

	writeMu sync.Mutex

	mu               sync.Mutex
	closed           bool
	connected        bool
	taskStarted      bool
	taskStartSent    bool
	currentSegmentID string
	currentText      string
	currentVoice     string
	currentLanguage  string
	currentStyle     string

	events    chan *tts.ProviderEvent
	closeOnce sync.Once
	done      chan struct{}
}

func newRealtimeSession(cfg realtimeSessionConfig) *realtimeSession {
	return &realtimeSession{
		id:              cfg.id,
		provider:        cfg.provider,
		conn:            cfg.conn,
		model:           cfg.model,
		defaultVoice:    cfg.defaultVoice,
		defaultLanguage: cfg.defaultLanguage,
		initialVoice:    cfg.initialVoice,
		initialLanguage: cfg.initialLanguage,
		initialStyle:    cfg.initialStyle,
		sampleRate:      cfg.sampleRate,
		bitrate:         cfg.bitrate,
		channels:        cfg.channels,
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

	voice := valueOrDefault(segment.Voice, valueOrDefault(s.initialVoice, s.defaultVoice))
	language := valueOrDefault(segment.Language, valueOrDefault(s.initialLanguage, s.defaultLanguage))
	style := valueOrDefault(segment.GuidanceText, s.initialStyle)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return &tts.Error{
			Code:      tts.ErrSessionClosed,
			Message:   "minimax tts session is closed",
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segment.SegmentID,
		}
	}
	s.currentSegmentID = segment.SegmentID
	s.currentText = segment.Text
	s.currentVoice = voice
	s.currentLanguage = language
	s.currentStyle = style
	shouldStart := s.connected && !s.taskStartSent
	shouldSendText := s.taskStarted
	s.mu.Unlock()

	if !s.emit(&tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentStart,
		Provider:  s.provider,
		SessionID: s.id,
		SegmentID: segment.SegmentID,
	}) {
		return &tts.Error{
			Code:      tts.ErrSessionClosed,
			Message:   "minimax tts session is closed",
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segment.SegmentID,
		}
	}

	if shouldStart {
		if err := s.startTask(ctx, voice, language, style); err != nil {
			return err
		}
	}
	if shouldSendText {
		if err := s.writeText(ctx, segment.Text, segment.SegmentID); err != nil {
			return err
		}
	}
	return nil
}

func (s *realtimeSession) Finish(ctx context.Context) error {
	if err := s.writeJSON(ctx, taskEventRequest{Event: "task_finish"}); err != nil {
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
		var resp taskEventResponse
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

		switch resp.Event {
		case "connected_success":
			s.handleConnected()
		case "task_started":
			s.handleTaskStarted()
		case "task_continued":
			s.handleTaskContinued(&resp)
		case "task_finished":
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventSessionEnd,
				Provider:  s.provider,
				SessionID: s.id,
				RawMeta:   resp.meta(),
			})
			return
		case "task_failed":
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: s.segmentID(),
				RawMeta:   resp.meta(),
				Error: &tts.Error{
					Code:      tts.ErrSegmentFailed,
					Message:   resp.statusMessage(),
					Provider:  s.provider,
					SessionID: s.id,
					SegmentID: s.segmentID(),
				},
			})
			return
		}
	}
}

func (s *realtimeSession) handleConnected() {
	s.mu.Lock()
	s.connected = true
	s.mu.Unlock()

	voice, language, style := s.currentSessionOptions()
	if voice == "" {
		voice = valueOrDefault(s.initialVoice, s.defaultVoice)
	}
	if language == "" {
		language = valueOrDefault(s.initialLanguage, s.defaultLanguage)
	}
	if style == "" {
		style = s.initialStyle
	}
	if err := s.startTask(context.Background(), voice, language, style); err != nil {
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventError,
			Provider:  s.provider,
			SessionID: s.id,
			Error:     s.eventError(err, ""),
		})
	}
}

func (s *realtimeSession) handleTaskStarted() {
	s.mu.Lock()
	s.taskStarted = true
	text := s.currentText
	segmentID := s.currentSegmentID
	s.mu.Unlock()

	if text != "" {
		if err := s.writeText(context.Background(), text, segmentID); err != nil {
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: segmentID,
				Error:     s.eventError(err, segmentID),
			})
		}
	}
}

func (s *realtimeSession) handleTaskContinued(resp *taskEventResponse) {
	if resp == nil {
		return
	}
	segmentID := s.segmentID()
	if resp.Data != nil && resp.Data.Audio != "" {
		data, err := hex.DecodeString(resp.Data.Audio)
		if err != nil {
			s.emit(&tts.ProviderEvent{
				Type:      tts.ProviderEventError,
				Provider:  s.provider,
				SessionID: s.id,
				SegmentID: segmentID,
				RawMeta:   resp.meta(),
				Error: &tts.Error{
					Code:      tts.ErrAudioDecodeFailed,
					Message:   fmt.Sprintf("decode minimax mp3 audio: %v", err),
					Provider:  s.provider,
					SessionID: s.id,
					SegmentID: segmentID,
					Cause:     err,
				},
			})
			return
		}
		s.emit(&tts.ProviderEvent{
			Type:      tts.ProviderEventAudio,
			Provider:  s.provider,
			SessionID: s.id,
			SegmentID: segmentID,
			RawMeta:   resp.meta(),
			Audio: &tts.ProviderAudioChunk{
				Codec:      audio.CodecMP3,
				Container:  audio.ContainerRaw,
				SampleRate: s.sampleRate,
				Channels:   s.channels,
				Data:       data,
			},
		})
	}
	if resp.IsFinal {
		s.handleSegmentDone()
	}
}

func (s *realtimeSession) handleSegmentDone() {
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

func (s *realtimeSession) startTask(ctx context.Context, voice, language, style string) error {
	s.mu.Lock()
	if s.taskStartSent {
		s.mu.Unlock()
		return nil
	}
	s.taskStartSent = true
	s.mu.Unlock()

	req := taskStartRequest{
		Event:         "task_start",
		Model:         s.model,
		LanguageBoost: rewriteLang(language),
		VoiceSetting: &voiceSetting{
			VoiceID: voice,
			Emotion: emotionFromStyle(style),
			Speed:   defaultSpeed,
			Vol:     defaultVolume,
			Pitch:   defaultPitch,
		},
		PronunciationDict: &pronunciationDict{Tone: []string{}},
		AudioSetting: &audioSetting{
			Format:     "mp3",
			SampleRate: s.sampleRate,
			Bitrate:    s.bitrate,
			Channel:    s.channels,
		},
	}
	if req.VoiceSetting.Emotion == EmotionUnknown {
		req.VoiceSetting.Emotion = ""
	}
	if err := s.writeJSON(ctx, req); err != nil {
		return s.writeError(err, "")
	}
	return nil
}

func (s *realtimeSession) writeText(ctx context.Context, text, segmentID string) error {
	if err := s.writeJSON(ctx, taskEventRequest{
		Event: "task_continue",
		Text:  text,
	}); err != nil {
		return s.writeError(err, segmentID)
	}
	return nil
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
	s.currentText = ""
	return segmentID
}

func (s *realtimeSession) currentSessionOptions() (string, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentVoice, s.currentLanguage, s.currentStyle
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

func (s *realtimeSession) eventError(err error, segmentID string) *tts.Error {
	if ttsErr, ok := err.(*tts.Error); ok {
		return ttsErr
	}
	return s.writeError(err, segmentID)
}

func emotionFromStyle(style string) Emotion {
	switch Emotion(strings.ToLower(style)) {
	case EmotionHappy:
		return EmotionHappy
	case EmotionSad:
		return EmotionSad
	case EmotionAngry:
		return EmotionAngry
	case EmotionFearful:
		return EmotionFearful
	case EmotionDisgusted:
		return EmotionDisgusted
	case EmotionSurprised:
		return EmotionSurprised
	case EmotionCalm:
		return EmotionCalm
	case EmotionFluent:
		return EmotionFluent
	case EmotionWhisper:
		return EmotionWhisper
	default:
		return EmotionUnknown
	}
}

type taskStartRequest struct {
	VoiceSetting      *voiceSetting      `json:"voice_setting"`
	PronunciationDict *pronunciationDict `json:"pronunciation_dict"`
	AudioSetting      *audioSetting      `json:"audio_setting"`
	Event             string             `json:"event"`
	Model             string             `json:"model"`
	LanguageBoost     string             `json:"language_boost"`
}

type voiceSetting struct {
	VoiceID string  `json:"voice_id"`
	Emotion Emotion `json:"emotion,omitempty"`
	Speed   float64 `json:"speed"`
	Vol     int     `json:"vol"`
	Pitch   int     `json:"pitch"`
}

type pronunciationDict struct {
	Tone []string `json:"tone"`
}

type audioSetting struct {
	Format     string `json:"format,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Bitrate    int    `json:"bitrate,omitempty"`
	Channel    int    `json:"channel,omitempty"`
}

type taskEventRequest struct {
	Event string `json:"event"`
	Text  string `json:"text,omitempty"`
}

type taskEventResponse struct {
	Data      *responseData `json:"data"`
	ExtraInfo *extraInfo    `json:"extra_info,omitempty"`
	BaseResp  *baseResp     `json:"base_resp,omitempty"`
	Event     string        `json:"event"`
	SessionID string        `json:"session_id"`
	TraceID   string        `json:"trace_id"`
	IsFinal   bool          `json:"is_final"`
}

func (r *taskEventResponse) meta() map[string]any {
	if r == nil {
		return nil
	}
	meta := map[string]any{
		"provider_session_id": r.SessionID,
		"trace_id":            r.TraceID,
		"event":               r.Event,
		"is_final":            r.IsFinal,
	}
	if r.ExtraInfo != nil {
		meta["extra_info"] = r.ExtraInfo
	}
	if r.BaseResp != nil {
		meta["base_resp"] = r.BaseResp
	}
	return meta
}

func (r *taskEventResponse) statusMessage() string {
	if r != nil && r.BaseResp != nil && r.BaseResp.StatusMsg != "" {
		return r.BaseResp.StatusMsg
	}
	return "minimax tts task failed"
}

type responseData struct {
	Audio string `json:"audio"`
}

type extraInfo struct {
	AudioFormat             string `json:"audio_format"`
	AudioChannel            int    `json:"audio_channel"`
	AudioLength             int    `json:"audio_length"`
	AudioSampleRate         int    `json:"audio_sample_rate"`
	AudioSize               int    `json:"audio_size"`
	Bitrate                 int    `json:"bitrate"`
	InvisibleCharacterRatio int    `json:"invisible_character_ratio"`
	UsageCharacters         int    `json:"usage_characters"`
	WordCount               int    `json:"word_count"`
}

type baseResp struct {
	StatusMsg  string `json:"status_msg"`
	StatusCode int    `json:"status_code"`
}
