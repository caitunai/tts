package qwentts

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

const (
	defaultProviderName = "qwen_tts"
	defaultModel        = "qwen3-tts-instruct-flash"
	defaultSampleRate   = 24000
	defaultSegmentID    = "seg_001"

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

// Config configures an Alibaba Cloud Qwen HTTP TTS provider.
type Config struct {
	Name string

	Endpoint string
	Token    string

	Model           string
	DefaultVoice    string
	DefaultLanguage string
	SampleRate      int

	Client *resty.Client
}

// Provider adapts Qwen's HTTP SSE TTS API to the TTS Provider interface.
type Provider struct {
	name string

	endpoint string
	token    string

	model           string
	defaultVoice    string
	defaultLanguage string
	sampleRate      int

	client *resty.Client
}

// NewProvider creates a Qwen HTTP TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen tts endpoint is required",
			Provider: cfg.Name,
		}
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = defaultSampleRate
	}
	if cfg.SampleRate < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "qwen tts sample rate must be positive",
			Provider: cfg.Name,
		}
	}
	if cfg.Client == nil {
		cfg.Client = resty.New()
	}

	return &Provider{
		name:            cfg.Name,
		endpoint:        cfg.Endpoint,
		token:           cfg.Token,
		model:           cfg.Model,
		defaultVoice:    cfg.DefaultVoice,
		defaultLanguage: cfg.DefaultLanguage,
		sampleRate:      cfg.SampleRate,
		client:          cfg.Client,
	}, nil
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) Capabilities(context.Context) (*tts.ProviderCapabilities, error) {
	caps := &tts.ProviderCapabilities{
		Name:                    p.name,
		Transports:              []tts.TransportType{tts.TransportHTTP},
		SupportsStreaming:       true,
		SupportsAppendText:      false,
		SupportsSegmentEndEvent: true,
		SupportsPCMOutput:       true,
		OutputCodecs:            []audio.Codec{audio.CodecPCM},
		OutputContainers:        []audio.Container{audio.ContainerRaw},
		OutputSampleRates:       []int{p.sampleRate},
		OutputChannels:          []int{audio.DefaultChannels},
		Languages: []tts.LanguageInfo{
			{Code: "zh", Name: Chinese},
			{Code: "en", Name: English},
			{Code: "de", Name: German},
			{Code: "it", Name: Italian},
			{Code: "pt", Name: Portuguese},
			{Code: "es", Name: Spanish},
			{Code: "ja", Name: Japanese},
			{Code: "ko", Name: Korean},
			{Code: "fr", Name: French},
			{Code: "ru", Name: Russian},
			{Code: "auto", Name: Auto},
		},
	}
	if p.defaultVoice != "" {
		caps.Voices = []tts.VoiceInfo{{ID: p.defaultVoice, Name: p.defaultVoice}}
	}
	return caps, nil
}

func (p *Provider) SynthesizeOnce(ctx context.Context, req *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	if req == nil {
		return nil, &tts.Error{
			Code:     tts.ErrInternal,
			Message:  "synthesize request is nil",
			Provider: p.name,
		}
	}

	events := make(chan *tts.ProviderEvent, 16)
	go p.stream(ctx, req, events)
	return events, nil
}

func (p *Provider) OpenSession(context.Context, *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
	return nil, &tts.Error{
		Code:     tts.ErrUnsupportedFeature,
		Message:  "qwen tts HTTP provider does not support sessions",
		Provider: p.name,
	}
}

func (p *Provider) stream(ctx context.Context, req *tts.ProviderSynthesizeRequest, events chan<- *tts.ProviderEvent) {
	defer close(events)

	segmentID := defaultSegmentID
	if req.RequestID != "" {
		segmentID = req.RequestID
	}

	resp, err := p.doRequest(ctx, req)
	if err != nil {
		events <- p.errorEvent(req, segmentID, &tts.Error{
			Code:      tts.ErrProviderUnavailable,
			Message:   err.Error(),
			Provider:  p.name,
			SegmentID: segmentID,
			Cause:     err,
			Retryable: true,
		})
		return
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.RawBody())

	if resp.StatusCode() < http.StatusOK || resp.StatusCode() >= http.StatusMultipleChoices {
		events <- p.errorEvent(req, segmentID, statusError(p.name, segmentID, resp))
		return
	}

	events <- &tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentStart,
		Provider:  p.name,
		RequestID: req.RequestID,
		SegmentID: segmentID,
	}

	parser := &tts.SSEParser{}
	reader := bufio.NewReader(resp.RawBody())
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if err := p.handleLine(parser, line, req, segmentID, events); err != nil {
				events <- p.errorEvent(req, segmentID, err)
				return
			}
		}

		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			if err := p.handleLine(parser, "", req, segmentID, events); err != nil {
				events <- p.errorEvent(req, segmentID, err)
				return
			}
			break
		}
		events <- p.errorEvent(req, segmentID, &tts.Error{
			Code:      tts.ErrProviderUnavailable,
			Message:   readErr.Error(),
			Provider:  p.name,
			SegmentID: segmentID,
			Cause:     readErr,
			Retryable: true,
		})
		return
	}

	events <- &tts.ProviderEvent{
		Type:      tts.ProviderEventSegmentEnd,
		Provider:  p.name,
		RequestID: req.RequestID,
		SegmentID: segmentID,
	}
}

func (p *Provider) doRequest(ctx context.Context, req *tts.ProviderSynthesizeRequest) (*resty.Response, error) {
	body := requestBody{
		Model: p.model,
		Input: requestInput{
			Text:         req.Text,
			Voice:        valueOrDefault(req.Voice, p.defaultVoice),
			LanguageType: rewriteLang(valueOrDefault(req.Language, p.defaultLanguage)),
		},
	}

	request := p.client.R().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "text/event-stream").
		SetHeader("X-DashScope-SSE", "enable").
		SetBody(body)
	if p.token != "" {
		request.SetAuthToken(p.token)
	}

	return request.Post(p.endpoint)
}

func (p *Provider) handleLine(parser *tts.SSEParser, line string, req *tts.ProviderSynthesizeRequest, segmentID string, events chan<- *tts.ProviderEvent) error {
	event := parser.FeedLine(line)
	if event == nil || event.Data == "" {
		return nil
	}

	var resp responseBody
	if err := json.Unmarshal([]byte(event.Data), &resp); err != nil {
		return &tts.Error{
			Code:      tts.ErrProviderUnavailable,
			Message:   fmt.Sprintf("decode qwen tts SSE response: %v", err),
			Provider:  p.name,
			SegmentID: segmentID,
			Cause:     err,
		}
	}
	if resp.Output.Audio.Data == "" {
		return nil
	}

	pcm, err := base64.StdEncoding.DecodeString(resp.Output.Audio.Data)
	if err != nil {
		return &tts.Error{
			Code:      tts.ErrAudioDecodeFailed,
			Message:   fmt.Sprintf("decode qwen tts audio: %v", err),
			Provider:  p.name,
			SegmentID: segmentID,
			Cause:     err,
		}
	}

	events <- &tts.ProviderEvent{
		Type:      tts.ProviderEventAudio,
		Provider:  p.name,
		RequestID: req.RequestID,
		SegmentID: segmentID,
		Audio: &tts.ProviderAudioChunk{
			Codec:      audio.CodecPCM,
			Container:  audio.ContainerRaw,
			SampleRate: p.sampleRate,
			Channels:   audio.DefaultChannels,
			Format:     audio.PCMFormatS16LE,
			Data:       pcm,
		},
	}
	return nil
}

func (p *Provider) errorEvent(req *tts.ProviderSynthesizeRequest, segmentID string, err error) *tts.ProviderEvent {
	return &tts.ProviderEvent{
		Type:      tts.ProviderEventError,
		Provider:  p.name,
		RequestID: req.RequestID,
		SegmentID: segmentID,
		Error:     errorToTTSError(err, p.name, segmentID),
	}
}

func statusError(provider, segmentID string, resp *resty.Response) *tts.Error {
	body, _ := io.ReadAll(io.LimitReader(resp.RawBody(), 4096))

	code := tts.ErrProviderUnavailable
	retryable := resp.StatusCode() == http.StatusTooManyRequests || resp.StatusCode() >= http.StatusInternalServerError
	switch resp.StatusCode() {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = tts.ErrProviderAuthFailed
	case http.StatusTooManyRequests:
		code = tts.ErrProviderRateLimited
	}

	return &tts.Error{
		Code:      code,
		Message:   fmt.Sprintf("qwen tts status %d: %s", resp.StatusCode(), string(body)),
		Provider:  provider,
		SegmentID: segmentID,
		Retryable: retryable,
	}
}

func errorToTTSError(err error, provider, segmentID string) *tts.Error {
	if err == nil {
		return nil
	}
	if ttsErr, ok := errors.AsType[*tts.Error](err); ok {
		return ttsErr
	}
	return &tts.Error{
		Code:      tts.ErrInternal,
		Message:   err.Error(),
		Provider:  provider,
		SegmentID: segmentID,
		Cause:     err,
	}
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

type requestBody struct {
	Model string       `json:"model"`
	Input requestInput `json:"input"`
}

type requestInput struct {
	Text         string `json:"text"`
	Voice        string `json:"voice"`
	LanguageType string `json:"language_type"`
}

type responseBody struct {
	Output responseOutput `json:"output"`
}

type responseOutput struct {
	Audio responseAudio `json:"audio"`
}

type responseAudio struct {
	Data string `json:"data"`
}
