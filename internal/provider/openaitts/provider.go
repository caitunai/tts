package openaitts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

const (
	defaultProviderName   = "openai_tts"
	defaultEndpoint       = "https://api.openai.com/v1/audio/speech"
	defaultModel          = "gpt-4o-mini-tts"
	defaultVoice          = "coral"
	defaultResponseFormat = "opus"
	defaultStreamFormat   = "audio"
	defaultSegmentID      = "seg_001"
)

// Config configures OpenAI's HTTP Speech API provider.
type Config struct {
	Name string

	Endpoint      string
	APIKey        string
	Authorization string

	Model               string
	DefaultVoice        string
	DefaultInstructions string
	Speed               float64
	StreamFormat        string

	Client *resty.Client
}

// Provider adapts OpenAI's Speech API to the TTS Provider interface.
type Provider struct {
	name string

	endpoint      string
	apiKey        string
	authorization string

	model               string
	defaultVoice        string
	defaultInstructions string
	speed               float64
	streamFormat        string

	client *resty.Client
}

// NewProvider creates an OpenAI TTS provider.
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
	if cfg.DefaultVoice == "" {
		cfg.DefaultVoice = defaultVoice
	}
	if cfg.StreamFormat == "" {
		cfg.StreamFormat = defaultStreamFormat
	}
	if cfg.Speed < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "openai tts speed must be positive",
			Provider: cfg.Name,
		}
	}
	if cfg.Client == nil {
		cfg.Client = resty.New()
	}

	return &Provider{
		name:                cfg.Name,
		endpoint:            cfg.Endpoint,
		apiKey:              cfg.APIKey,
		authorization:       cfg.Authorization,
		model:               cfg.Model,
		defaultVoice:        cfg.DefaultVoice,
		defaultInstructions: cfg.DefaultInstructions,
		speed:               cfg.Speed,
		streamFormat:        cfg.StreamFormat,
		client:              cfg.Client,
	}, nil
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) Capabilities(context.Context) (*tts.ProviderCapabilities, error) {
	return &tts.ProviderCapabilities{
		Name:                    p.name,
		Transports:              []tts.TransportType{tts.TransportHTTP},
		SupportsStreaming:       true,
		SupportsAppendText:      false,
		SupportsGuidanceText:    true,
		SupportsSpeed:           true,
		SupportsSegmentEndEvent: true,
		SupportsOggOpusOutput:   true,
		OutputCodecs:            []audio.Codec{audio.CodecOpus},
		OutputContainers:        []audio.Container{audio.ContainerOgg},
		OutputSampleRates:       []int{audio.OpusSampleRate},
		OutputChannels:          []int{audio.DefaultChannels},
		Voices: []tts.VoiceInfo{
			{ID: "alloy", Name: "alloy"},
			{ID: "ash", Name: "ash"},
			{ID: "ballad", Name: "ballad"},
			{ID: "coral", Name: "coral"},
			{ID: "echo", Name: "echo"},
			{ID: "fable", Name: "fable"},
			{ID: "onyx", Name: "onyx"},
			{ID: "nova", Name: "nova"},
			{ID: "sage", Name: "sage"},
			{ID: "shimmer", Name: "shimmer"},
			{ID: "verse", Name: "verse"},
			{ID: "marin", Name: "marin"},
			{ID: "cedar", Name: "cedar"},
		},
	}, nil
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
		Message:  "openai tts HTTP provider does not support sessions",
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

	buffer := make([]byte, 4096)
	for {
		n, readErr := resp.RawBody().Read(buffer)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buffer[:n])
			events <- &tts.ProviderEvent{
				Type:      tts.ProviderEventAudio,
				Provider:  p.name,
				RequestID: req.RequestID,
				SegmentID: segmentID,
				Audio: &tts.ProviderAudioChunk{
					Codec:      audio.CodecOpus,
					Container:  audio.ContainerOgg,
					SampleRate: audio.OpusSampleRate,
					Channels:   audio.DefaultChannels,
					Data:       data,
				},
			}
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
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
	body := p.requestBody(req)
	request := p.client.R().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "audio/ogg").
		SetBody(body)
	if auth := p.authHeader(); auth != "" {
		request.SetHeader("Authorization", auth)
	}
	return request.Post(p.endpoint)
}

func (p *Provider) requestBody(req *tts.ProviderSynthesizeRequest) requestBody {
	model := p.modelFor(req)
	body := requestBody{
		Model:          model,
		Input:          req.Text,
		Voice:          valueOrDefault(req.Voice, p.defaultVoice),
		ResponseFormat: defaultResponseFormat,
		StreamFormat:   p.streamFormat,
	}
	if instructions := valueOrDefault(req.GuidanceText, p.defaultInstructions); instructions != "" && supportsInstructions(model) {
		body.Instructions = instructions
	}
	if speed := p.speedFor(req); speed > 0 {
		body.Speed = speed
	}
	return body
}

func (p *Provider) modelFor(req *tts.ProviderSynthesizeRequest) string {
	if req != nil {
		if value, ok := req.Options["model"].(string); ok && value != "" {
			return value
		}
	}
	return p.model
}

func (p *Provider) speedFor(req *tts.ProviderSynthesizeRequest) float64 {
	if req != nil {
		if value, ok := req.Options["speed"].(float64); ok {
			return value
		}
		if value, ok := req.Options["speed"].(int); ok {
			return float64(value)
		}
	}
	return p.speed
}

func (p *Provider) authHeader() string {
	if p.authorization != "" {
		return p.authorization
	}
	if p.apiKey == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(p.apiKey), "bearer ") {
		return p.apiKey
	}
	return "Bearer " + p.apiKey
}

func supportsInstructions(model string) bool {
	return model != "tts-1" && model != "tts-1-hd"
}

type requestBody struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	Instructions   string  `json:"instructions,omitempty"`
	ResponseFormat string  `json:"response_format"`
	Speed          float64 `json:"speed,omitempty"`
	StreamFormat   string  `json:"stream_format,omitempty"`
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

	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status()
	}
	if json.Valid(body) {
		message = compactJSONMessage(body)
	}

	return &tts.Error{
		Code:      code,
		Message:   fmt.Sprintf("openai tts status %d: %s", resp.StatusCode(), message),
		Provider:  provider,
		SegmentID: segmentID,
		Retryable: retryable,
	}
}

func compactJSONMessage(body []byte) string {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return string(body)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return string(body)
	}
	return string(out)
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

func errorToTTSError(err error, provider, segmentID string) *tts.Error {
	if err == nil {
		return nil
	}
	if ttsErr, ok := err.(*tts.Error); ok {
		return ttsErr
	}
	return &tts.Error{
		Code:      tts.ErrProviderUnavailable,
		Message:   err.Error(),
		Provider:  provider,
		SegmentID: segmentID,
		Cause:     err,
		Retryable: true,
	}
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
