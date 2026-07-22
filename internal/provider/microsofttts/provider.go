package microsofttts

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/caitunai/tts/internal/audio"
	langnorm "github.com/caitunai/tts/internal/language"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

const (
	defaultProviderName = "microsoft_tts"
	defaultOutputFormat = "ogg-48khz-16bit-mono-opus"
	defaultLanguage     = "en-US"
	defaultSegmentID    = "seg_001"
)

// Config configures a Microsoft Azure Speech TTS provider.
type Config struct {
	Name string

	Endpoint        string
	SubscriptionKey string

	DefaultVoice string
	// DefaultLanguage is used only when Language is omitted and the voice name
	// does not contain a BCP-47 locale.
	DefaultLanguage string
	OutputFormat    string

	Client *resty.Client
}

// Provider adapts Microsoft Azure Speech's HTTP TTS API to the TTS Provider
// interface.
type Provider struct {
	name string

	endpoint        string
	subscriptionKey string

	defaultVoice    string
	defaultLanguage string
	outputFormat    string

	client *resty.Client
}

// NewProvider creates a Microsoft TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "microsoft tts endpoint is required",
			Provider: cfg.Name,
		}
	}
	if cfg.OutputFormat == "" {
		cfg.OutputFormat = defaultOutputFormat
	}
	if cfg.Client == nil {
		cfg.Client = resty.New()
	}

	return &Provider{
		name:            cfg.Name,
		endpoint:        cfg.Endpoint,
		subscriptionKey: cfg.SubscriptionKey,
		defaultVoice:    cfg.DefaultVoice,
		defaultLanguage: cfg.DefaultLanguage,
		outputFormat:    cfg.OutputFormat,
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
		SupportsOggOpusOutput:   true,
		OutputCodecs:            []audio.Codec{audio.CodecOpus},
		OutputContainers:        []audio.Container{audio.ContainerOgg},
		OutputSampleRates:       []int{audio.OpusSampleRate},
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
		Message:  "microsoft tts HTTP provider does not support sessions",
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
	voice := valueOrDefault(req.Voice, p.defaultVoice)
	language := resolveLanguage(req.Language, voice, p.defaultLanguage)
	body := buildSSML(language, voice, req.Text)

	request := p.client.R().
		SetContext(ctx).
		SetDoNotParseResponse(true).
		SetHeader("Content-Type", "application/ssml+xml").
		SetHeader("X-Microsoft-OutputFormat", p.outputFormat).
		SetBody(body)
	if p.subscriptionKey != "" {
		request.SetHeader("Ocp-Apim-Subscription-Key", p.subscriptionKey)
	}

	return request.Post(p.endpoint)
}

func buildSSML(language, voice, text string) string {
	var out bytes.Buffer
	out.WriteString("<speak version='1.0' xml:lang='")
	escapeXML(&out, language)
	out.WriteString("'><voice name='")
	escapeXML(&out, voice)
	out.WriteString("'>")
	escapeXML(&out, text)
	out.WriteString("</voice></speak>")
	return out.String()
}

func escapeXML(out *bytes.Buffer, value string) {
	_ = xml.EscapeText(out, []byte(value))
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
		Message:   fmt.Sprintf("microsoft tts status %d: %s", resp.StatusCode(), string(body)),
		Provider:  provider,
		SegmentID: segmentID,
		Retryable: retryable,
	}
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

func resolveLanguage(requestLanguage, voice, configuredDefault string) string {
	voiceLanguage := languageFromVoice(voice)
	if strings.TrimSpace(requestLanguage) != "" {
		requested := langnorm.Parse(requestLanguage)
		if requested.Region() == "" && voiceLanguage != "" {
			fromVoice := langnorm.Parse(voiceLanguage)
			if requested.Matches(fromVoice, langnorm.MatchLanguage) {
				return voiceLanguage
			}
		}
		return requested.String()
	}
	if voiceLanguage != "" {
		return voiceLanguage
	}
	if strings.TrimSpace(configuredDefault) != "" {
		return langnorm.Normalize(configuredDefault)
	}
	return defaultLanguage
}

func languageFromVoice(voice string) string {
	parts := strings.Split(strings.TrimSpace(voice), "-")
	if len(parts) < 2 {
		return ""
	}

	for _, end := range []int{2, 3} {
		if len(parts) < end {
			continue
		}
		candidate := langnorm.Parse(strings.Join(parts[:end], "-"))
		if candidate.ISO6393() != "" && candidate.Region() != "" {
			return candidate.String()
		}
	}
	return ""
}
