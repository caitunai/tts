package vllmtts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/caitunai/tts/internal/audio"
	langnorm "github.com/caitunai/tts/internal/language"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

const (
	defaultProviderName   = "vllm_tts"
	defaultResponseFormat = "pcm"
	defaultSampleRate     = 24000
	defaultChunkSize      = defaultSampleRate * audio.DefaultFrameMS / 1000 * audio.DefaultChannels * 2
	defaultSegmentID      = "seg_001"
)

// Config configures a vLLM TTS provider.
type Config struct {
	Name string

	Endpoint string
	Token    string

	DefaultVoice    string
	DefaultLanguage string
	ResponseFormat  string

	ChunkSize int
	Client    *resty.Client
}

// Provider adapts a vLLM-compatible chunked PCM TTS endpoint to the TTS Provider
// interface.
type Provider struct {
	name string

	endpoint string
	token    string

	defaultVoice    string
	defaultLanguage string
	responseFormat  string

	chunkSize int
	client    *resty.Client
}

// NewProvider creates a vLLM TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "vllm tts endpoint is required",
			Provider: cfg.Name,
		}
	}
	if cfg.ResponseFormat == "" {
		cfg.ResponseFormat = defaultResponseFormat
	}
	if cfg.ChunkSize == 0 {
		cfg.ChunkSize = defaultChunkSize
	}
	if cfg.ChunkSize < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "vllm tts chunk size must be positive",
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
		defaultVoice:    cfg.DefaultVoice,
		defaultLanguage: cfg.DefaultLanguage,
		responseFormat:  cfg.ResponseFormat,
		chunkSize:       cfg.ChunkSize,
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
		OutputSampleRates:       []int{defaultSampleRate},
		OutputChannels:          []int{audio.DefaultChannels},
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
		Message:  "vllm tts provider does not support sessions",
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

	buf := make([]byte, p.chunkSize)
	for {
		n, readErr := resp.RawBody().Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			events <- &tts.ProviderEvent{
				Type:      tts.ProviderEventAudio,
				Provider:  p.name,
				RequestID: req.RequestID,
				SegmentID: segmentID,
				Audio: &tts.ProviderAudioChunk{
					Codec:      audio.CodecPCM,
					Container:  audio.ContainerRaw,
					SampleRate: defaultSampleRate,
					Channels:   audio.DefaultChannels,
					Format:     audio.PCMFormatS16LE,
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
	body := requestBody{
		Input:          req.Text,
		Voice:          valueOrDefault(req.Voice, p.defaultVoice),
		Stream:         true,
		ResponseFormat: p.responseFormat,
		Language:       normalizeLanguage(valueOrDefault(req.Language, p.defaultLanguage)),
	}

	request := p.client.R().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "application/octet-stream").
		SetBody(body)
	if p.token != "" {
		request.SetAuthToken(p.token)
	}

	return request.Post(p.endpoint)
}

var providerLanguageMapper = langnorm.NewMapper(
	langnorm.Map("Chinese", langnorm.MatchLanguage, "zh", "cmn", "yue"),
	langnorm.Map("English", langnorm.MatchLanguage, "en"),
	langnorm.Map("German", langnorm.MatchLanguage, "de"),
	langnorm.Map("Italian", langnorm.MatchLanguage, "it"),
	langnorm.Map("Portuguese", langnorm.MatchLanguage, "pt"),
	langnorm.Map("Spanish", langnorm.MatchLanguage, "es"),
	langnorm.Map("Japanese", langnorm.MatchLanguage, "ja"),
	langnorm.Map("Korean", langnorm.MatchLanguage, "ko"),
	langnorm.Map("French", langnorm.MatchLanguage, "fr"),
	langnorm.Map("Russian", langnorm.MatchLanguage, "ru"),
)

func normalizeLanguage(lang string) string {
	return providerLanguageMapper.Resolve(lang, langnorm.Normalize(lang))
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
		Message:   fmt.Sprintf("vllm tts status %d: %s", resp.StatusCode(), string(body)),
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

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

type requestBody struct {
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	Stream         bool   `json:"stream"`
	ResponseFormat string `json:"response_format"`
	Language       string `json:"language"`
}
