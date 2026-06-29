package httptts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
)

const (
	defaultProviderName   = "http_tts"
	defaultResponseFormat = "pcm"
	defaultChunkSize      = 4096
	defaultSegmentID      = "seg_001"
)

// Config configures an HTTP TTS provider.
type Config struct {
	Name string

	Endpoint string
	Token    string

	DefaultVoice    string
	DefaultLanguage string
	ResponseFormat  string

	ChunkSize int
	Client    *http.Client
}

// Provider adapts an HTTP chunked PCM TTS endpoint to the TTS Provider
// interface.
type Provider struct {
	name string

	endpoint string
	token    string

	defaultVoice    string
	defaultLanguage string
	responseFormat  string

	chunkSize int
	client    *http.Client
}

// NewProvider creates an HTTP TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "http tts endpoint is required",
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
			Message:  "http tts chunk size must be positive",
			Provider: cfg.Name,
		}
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
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
		OutputSampleRates:       []int{audio.DefaultSampleRate},
		OutputChannels:          []int{audio.DefaultChannels},
	}
	if p.defaultVoice != "" {
		caps.Voices = []tts.VoiceInfo{{ID: p.defaultVoice, Name: p.defaultVoice}}
	}
	if p.defaultLanguage != "" {
		caps.Languages = []tts.LanguageInfo{{Code: p.defaultLanguage, Name: p.defaultLanguage}}
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
	reqCopy := *req
	go p.stream(ctx, &reqCopy, events)
	return events, nil
}

func (p *Provider) OpenSession(context.Context, *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
	return nil, &tts.Error{
		Code:     tts.ErrUnsupportedFeature,
		Message:  "http tts provider does not support sessions",
		Provider: p.name,
	}
}

func (p *Provider) stream(ctx context.Context, req *tts.ProviderSynthesizeRequest, events chan<- *tts.ProviderEvent) {
	defer close(events)

	segmentID := defaultSegmentID
	if req.RequestID != "" {
		segmentID = req.RequestID
	}

	httpReq, err := p.buildRequest(ctx, req)
	if err != nil {
		events <- p.errorEvent(req, segmentID, err)
		return
	}

	resp, err := p.client.Do(httpReq)
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
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
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
		n, readErr := resp.Body.Read(buf)
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
					SampleRate: audio.DefaultSampleRate,
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

func (p *Provider) buildRequest(ctx context.Context, req *tts.ProviderSynthesizeRequest) (*http.Request, error) {
	body := requestBody{
		Input:          req.Text,
		Voice:          valueOrDefault(req.Voice, p.defaultVoice),
		Stream:         true,
		ResponseFormat: p.responseFormat,
		Language:       valueOrDefault(req.Language, p.defaultLanguage),
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/octet-stream")
	if p.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.token)
	}
	return httpReq, nil
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

func statusError(provider, segmentID string, resp *http.Response) *tts.Error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	code := tts.ErrProviderUnavailable
	retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = tts.ErrProviderAuthFailed
	case http.StatusTooManyRequests:
		code = tts.ErrProviderRateLimited
	}

	return &tts.Error{
		Code:      code,
		Message:   fmt.Sprintf("http tts status %d: %s", resp.StatusCode, string(body)),
		Provider:  provider,
		SegmentID: segmentID,
		Retryable: retryable,
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
