package geminitts

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
	"sync/atomic"
	"time"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

const (
	defaultProviderName = "gemini_tts"
	defaultEndpoint     = "https://generativelanguage.googleapis.com/v1beta/interactions"
	defaultModel        = "gemini-3.1-flash-tts-preview"
	defaultVoice        = "Kore"
	defaultSampleRate   = 24000
	defaultAPIRevision  = "2026-05-20"
	defaultSegmentID    = "seg_001"
	defaultAudioIdle    = 500 * time.Millisecond
)

// Config configures Gemini's HTTP streaming TTS provider.
type Config struct {
	Name string

	Endpoint    string
	APIKey      string
	APIRevision string

	Model               string
	DefaultVoice        string
	DefaultInstructions string
	SampleRate          int
	AudioIdleTimeout    time.Duration

	Client *resty.Client
}

// Provider adapts Gemini's Interactions TTS API to the TTS Provider interface.
type Provider struct {
	name string

	endpoint    string
	apiKey      string
	apiRevision string

	model               string
	defaultVoice        string
	defaultInstructions string
	sampleRate          int
	audioIdle           time.Duration

	client *resty.Client
}

// NewProvider creates a Gemini TTS provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = defaultProviderName
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = defaultEndpoint
	}
	if cfg.APIRevision == "" {
		cfg.APIRevision = defaultAPIRevision
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.DefaultVoice == "" {
		cfg.DefaultVoice = defaultVoice
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = defaultSampleRate
	}
	if cfg.AudioIdleTimeout == 0 {
		cfg.AudioIdleTimeout = defaultAudioIdle
	}
	if cfg.SampleRate < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "gemini tts sample rate must be positive",
			Provider: cfg.Name,
		}
	}
	if cfg.AudioIdleTimeout < 0 {
		return nil, &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  "gemini tts audio idle timeout must be non-negative",
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
		apiRevision:         cfg.APIRevision,
		model:               cfg.Model,
		defaultVoice:        cfg.DefaultVoice,
		defaultInstructions: cfg.DefaultInstructions,
		sampleRate:          cfg.SampleRate,
		audioIdle:           cfg.AudioIdleTimeout,
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
		SupportsSegmentEndEvent: true,
		SupportsPCMOutput:       true,
		OutputCodecs:            []audio.Codec{audio.CodecPCM},
		OutputContainers:        []audio.Container{audio.ContainerRaw},
		OutputSampleRates:       []int{p.sampleRate},
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
		Message:  "gemini tts HTTP provider does not support sessions",
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
	var idleClosed atomic.Bool
	var idleTimer *time.Timer
	defer func() {
		if idleTimer != nil {
			idleTimer.Stop()
		}
	}()
	resetAudioIdleTimer := func() {
		if p.audioIdle <= 0 {
			return
		}
		if idleTimer == nil {
			idleTimer = time.AfterFunc(p.audioIdle, func() {
				idleClosed.Store(true)
				_ = resp.RawBody().Close()
			})
			return
		}
		idleTimer.Reset(p.audioIdle)
	}

	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			result, err := p.handleLine(parser, line, req, segmentID, events)
			if err != nil {
				events <- p.errorEvent(req, segmentID, err)
				return
			}
			if result.audio {
				resetAudioIdleTimer()
			}
			if result.completed {
				break
			}
		}

		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			_, err := p.handleLine(parser, "", req, segmentID, events)
			if err != nil {
				events <- p.errorEvent(req, segmentID, err)
				return
			}
			break
		}
		if idleClosed.Load() {
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
		SetHeader("Accept", "text/event-stream").
		SetHeader("x-goog-api-key", p.apiKey).
		SetBody(p.requestBody(req))
	if p.apiRevision != "" {
		request.SetHeader("Api-Revision", p.apiRevision)
	}
	return request.Post(p.endpoint)
}

func (p *Provider) requestBody(req *tts.ProviderSynthesizeRequest) requestBody {
	return requestBody{
		Model:          p.modelFor(req),
		Input:          inputWithGuidance(req.Text, valueOrDefault(req.GuidanceText, p.defaultInstructions)),
		ResponseFormat: responseFormat{Type: "audio"},
		GenerationConfig: generationConfig{
			SpeechConfig: []speechConfig{{
				Voice: valueOrDefault(req.Voice, p.defaultVoice),
			}},
		},
		Stream: true,
	}
}

func (p *Provider) modelFor(req *tts.ProviderSynthesizeRequest) string {
	if req != nil {
		if value, ok := req.Options["model"].(string); ok && value != "" {
			return value
		}
	}
	return p.model
}

func (p *Provider) handleLine(parser *tts.SSEParser, line string, req *tts.ProviderSynthesizeRequest, segmentID string, events chan<- *tts.ProviderEvent) (streamResult, error) {
	if event := parser.FeedLine(line); event != nil {
		return p.handleEventData(event.Event, event.Data, req, segmentID, events)
	}

	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") || strings.HasPrefix(trimmed, "data:") {
		return streamResult{}, nil
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return p.handleEventData("", trimmed, req, segmentID, events)
	}
	return streamResult{}, nil
}

func (p *Provider) handleEventData(eventName, data string, req *tts.ProviderSynthesizeRequest, segmentID string, events chan<- *tts.ProviderEvent) (streamResult, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return streamResult{completed: isCompletionEvent(eventName)}, nil
	}
	if data == "[DONE]" {
		return streamResult{completed: true}, nil
	}

	var resp streamEvent
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return streamResult{}, &tts.Error{
			Code:      tts.ErrProviderUnavailable,
			Message:   fmt.Sprintf("decode gemini tts stream response: %v", err),
			Provider:  p.name,
			SegmentID: segmentID,
			Cause:     err,
		}
	}

	audioData := resp.audioData()
	if audioData == "" {
		return streamResult{completed: isCompletionEvent(eventName) || resp.isCompletion()}, nil
	}
	pcm, err := base64.StdEncoding.DecodeString(audioData)
	if err != nil {
		return streamResult{}, &tts.Error{
			Code:      tts.ErrAudioDecodeFailed,
			Message:   fmt.Sprintf("decode gemini tts audio: %v", err),
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
	return streamResult{audio: true, completed: isCompletionEvent(eventName) || resp.isCompletion()}, nil
}

type streamResult struct {
	audio     bool
	completed bool
}

type requestBody struct {
	Model            string           `json:"model"`
	Input            string           `json:"input"`
	ResponseFormat   responseFormat   `json:"response_format"`
	GenerationConfig generationConfig `json:"generation_config"`
	Stream           bool             `json:"stream"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type generationConfig struct {
	SpeechConfig []speechConfig `json:"speech_config"`
}

type speechConfig struct {
	Voice string `json:"voice"`
}

type streamEvent struct {
	EventType      string       `json:"event_type"`
	EventTypeAlt   string       `json:"eventType"`
	Delta          *audioDelta  `json:"delta,omitempty"`
	OutputAudio    *audioOutput `json:"output_audio,omitempty"`
	OutputAudioAlt *audioOutput `json:"outputAudio,omitempty"`
}

type audioDelta struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type audioOutput struct {
	Data string `json:"data"`
}

func (e *streamEvent) audioData() string {
	if e == nil {
		return ""
	}
	if e.Delta != nil && e.Delta.Type == "audio" {
		return e.Delta.Data
	}
	if e.OutputAudio != nil {
		return e.OutputAudio.Data
	}
	if e.OutputAudioAlt != nil {
		return e.OutputAudioAlt.Data
	}
	return ""
}

func (e *streamEvent) isCompletion() bool {
	if e == nil {
		return false
	}
	return isCompletionEvent(e.EventType) || isCompletionEvent(e.EventTypeAlt)
}

func isCompletionEvent(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	return value == "done" ||
		value == "finish" ||
		value == "finished" ||
		strings.HasSuffix(value, ".done") ||
		strings.HasSuffix(value, ".completed") ||
		strings.Contains(value, "complete") ||
		strings.Contains(value, "finished")
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
		Message:   fmt.Sprintf("gemini tts status %d: %s", resp.StatusCode(), message),
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
	if ttsErr, ok := errors.AsType[*tts.Error](err); ok {
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

func inputWithGuidance(text, guidance string) string {
	if guidance == "" {
		return text
	}
	return guidance + "\n\n" + text
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
