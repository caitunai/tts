package deepgramtts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

const (
	defaultProviderName = "deepgram_tts"
	defaultEndpoint     = "https://api.deepgram.com/v1/speak"
	defaultModel        = "aura-asteria-en"
	defaultEncoding     = "opus"
	defaultContainer    = "ogg"
	defaultBitRate      = 48000
	defaultSegmentID    = "seg_001"
)

// Config configures Deepgram's HTTP TTS provider.
type Config struct {
	Name string

	Endpoint      string
	APIKey        string
	Authorization string

	Model     string
	BitRate   int
	Speed     float64
	Tag       string
	MIPOptOut bool

	Client *resty.Client
}

// Provider adapts Deepgram's HTTP TTS API to the TTS Provider interface.
type Provider struct {
	name string

	endpoint      string
	apiKey        string
	authorization string

	model     string
	bitRate   int
	speed     float64
	tag       string
	mipOptOut bool

	client *resty.Client
}

// NewProvider creates a Deepgram TTS provider.
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
			Message:  "deepgram tts bit rate must be positive",
			Provider: cfg.Name,
		}
	}
	if cfg.Speed < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "deepgram tts speed must be positive",
			Provider: cfg.Name,
		}
	}
	if cfg.Client == nil {
		cfg.Client = resty.New()
	}

	return &Provider{
		name:          cfg.Name,
		endpoint:      cfg.Endpoint,
		apiKey:        cfg.APIKey,
		authorization: cfg.Authorization,
		model:         cfg.Model,
		bitRate:       cfg.BitRate,
		speed:         cfg.Speed,
		tag:           cfg.Tag,
		mipOptOut:     cfg.MIPOptOut,
		client:        cfg.Client,
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
		SupportsSegmentEndEvent: true,
		SupportsOggOpusOutput:   true,
		OutputCodecs:            []audio.Codec{audio.CodecOpus},
		OutputContainers:        []audio.Container{audio.ContainerOgg},
		OutputSampleRates:       []int{audio.OpusSampleRate},
		OutputChannels:          []int{audio.DefaultChannels},
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
		Message:  "deepgram tts HTTP provider does not support sessions",
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
	request := p.client.R().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "audio/ogg").
		SetQueryParam("model", p.modelFor(req)).
		SetQueryParam("encoding", defaultEncoding).
		SetQueryParam("container", defaultContainer).
		SetBody(requestBody{Text: req.Text})

	if p.bitRate > 0 {
		request.SetQueryParam("bit_rate", strconv.Itoa(p.bitRate))
	}
	if p.speed > 0 {
		request.SetQueryParam("speed", strconv.FormatFloat(p.speed, 'f', -1, 64))
	}
	if p.tag != "" {
		request.SetQueryParam("tag", p.tag)
	}
	if p.mipOptOut {
		request.SetQueryParam("mip_opt_out", "true")
	}
	if auth := p.authHeader(); auth != "" {
		request.SetHeader("Authorization", auth)
	}

	return request.Post(p.endpoint)
}

func (p *Provider) modelFor(req *tts.ProviderSynthesizeRequest) string {
	if req != nil {
		if value, ok := req.Options["model"].(string); ok && value != "" {
			return value
		}
		if req.Voice != "" {
			return req.Voice
		}
	}
	return p.model
}

func (p *Provider) authHeader() string {
	if p.authorization != "" {
		return p.authorization
	}
	if p.apiKey == "" {
		return ""
	}
	lower := strings.ToLower(p.apiKey)
	if strings.HasPrefix(lower, "token ") || strings.HasPrefix(lower, "bearer ") {
		return p.apiKey
	}
	return "Token " + p.apiKey
}

type requestBody struct {
	Text string `json:"text"`
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
		Message:   fmt.Sprintf("deepgram tts status %d: %s", resp.StatusCode(), message),
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
	var ttsErr *tts.Error
	if errors.As(err, &ttsErr) {
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
