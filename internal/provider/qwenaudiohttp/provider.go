// Package qwenaudiohttp implements Qwen Audio's non-realtime HTTP protocol.
package qwenaudiohttp

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/caitunai/tts/internal/audio"
	langnorm "github.com/caitunai/tts/internal/language"
	"github.com/caitunai/tts/internal/transport"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

const (
	ProviderName             = "qwen_audio_http_tts"
	ModelQwenAudio30TTSFlash = "qwen-audio-3.0-tts-flash"
	ModelQwenAudio30TTSPlus  = "qwen-audio-3.0-tts-plus"
	ModelQwenAudio31TTSFlash = "qwen-audio-3.1-tts-flash"
)

// Config selects HTTP SSE (the default) or non-streaming JSON synthesis.
// Both modes request Ogg Opus at 48 kHz; the platform emits raw Opus packets.
type Config struct {
	Timeouts            tts.TimeoutConfig
	Name                string
	Endpoint            string
	APIKey              string
	Authorization       string
	Model               string
	DefaultVoice        string
	DefaultLanguage     string
	DefaultInstructions string
	BitRate             int
	NonStreaming        bool
	Client              *resty.Client
	// AudioClient downloads signed audio URLs. It is separate from Client so
	// synthesis credentials and request headers are not sent to storage hosts.
	AudioClient *resty.Client
}

type Provider struct {
	cfg Config
}

func NewProvider(cfg Config) (*Provider, error) {
	timeouts, err := transport.Resolve(cfg.Timeouts)
	if err != nil {
		return nil, err
	}
	cfg.Timeouts = timeouts
	cfg.Name = cmp.Or(cfg.Name, ProviderName)
	if err := validateURL(cfg.Endpoint); err != nil {
		return nil, &tts.Error{Code: tts.ErrUnsupportedProvider, Provider: cfg.Name,
			Message: "qwen audio HTTP endpoint: " + err.Error(), Cause: err}
	}
	cfg.Model = cmp.Or(cfg.Model, ModelQwenAudio31TTSFlash)
	cfg.BitRate = cmp.Or(cfg.BitRate, 32)
	if cfg.BitRate < 6 || cfg.BitRate > 510 {
		return nil, &tts.Error{Code: tts.ErrUnsupportedProvider, Provider: cfg.Name,
			Message: "qwen audio HTTP bit rate must be between 6 and 510 kbps"}
	}
	cfg.Client = transport.HTTPClient(cfg.Client, timeouts)
	cfg.AudioClient = transport.HTTPClient(cfg.AudioClient, timeouts)
	return &Provider{cfg: cfg}, nil
}

func (p *Provider) Name() string { return p.cfg.Name }

func (p *Provider) Capabilities(context.Context) (*tts.ProviderCapabilities, error) {
	return &tts.ProviderCapabilities{
		Name: p.Name(), Transports: []tts.TransportType{tts.TransportHTTP},
		SupportsStreaming: !p.cfg.NonStreaming, SupportsSegmentEndEvent: true,
		SupportsGuidanceText: true, SupportsSSML: true,
		SupportsSpeed: true, SupportsPitch: true, SupportsVolume: true,
		SupportsOggOpusOutput: true,
		OutputCodecs:          []audio.Codec{audio.CodecOpus},
		OutputContainers:      []audio.Container{audio.ContainerOgg},
		OutputSampleRates:     []int{audio.OpusSampleRate}, OutputChannels: []int{1},
	}, nil
}

func (p *Provider) OpenSession(context.Context, *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
	return nil, p.failure(tts.ErrUnsupportedFeature, "qwen audio HTTP does not support append text sessions", nil)
}

func (p *Provider) SynthesizeOnce(ctx context.Context, req *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	if req == nil {
		return nil, p.failure(tts.ErrInternal, "synthesize request is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := p.requestBody(req)
	if err != nil {
		return nil, p.failure(tts.ErrUnsupportedFeature, err.Error(), err)
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, p.failure(tts.ErrUnsupportedFeature, "encode qwen audio HTTP request: "+err.Error(), err)
	}
	reqCopy := *req
	events := make(chan *tts.ProviderEvent, 16)
	go func() {
		defer close(events)
		s := &synthesis{provider: p, req: &reqCopy, events: events,
			segmentID: cmp.Or(reqCopy.RequestID, "seg_001")}
		if err := s.run(ctx, body); err != nil {
			failure, ok := errors.AsType[*tts.Error](err)
			if !ok {
				failure = p.failure(tts.ErrProviderUnavailable, "qwen audio HTTP: "+err.Error(), err)
			}
			failure = tts.NormalizeError(failure)
			failure.SegmentID = s.segmentID
			s.emit(ctx, &tts.ProviderEvent{Type: tts.ProviderEventError, Error: failure})
		}
	}()
	return events, nil
}

func (p *Provider) requestBody(req *tts.ProviderSynthesizeRequest) (map[string]any, error) {
	voice := cmp.Or(req.Voice, p.cfg.DefaultVoice)
	if strings.TrimSpace(req.Text) == "" || strings.TrimSpace(voice) == "" {
		return nil, fmt.Errorf("qwen audio HTTP requires text and voice")
	}
	if req.ReferenceAudio != nil {
		return nil, fmt.Errorf("qwen audio HTTP does not accept reference audio")
	}
	input := map[string]any{
		"text": req.Text, "voice": voice, "format": "opus",
		"sample_rate": audio.OpusSampleRate, "bit_rate": p.cfg.BitRate,
	}
	if guidance := cmp.Or(req.GuidanceText, p.cfg.DefaultInstructions); guidance != "" {
		input["instruction"] = guidance
	}
	if language := normalizeLanguage(cmp.Or(req.Language, p.cfg.DefaultLanguage)); language != "" {
		input["language_hints"] = []string{language}
	}
	for key, value := range req.Options {
		switch key {
		case "volume", "rate", "pitch", "bit_rate", "enable_ssml", "word_timestamp_enabled", "seed",
			"enable_aigc_tag", "aigc_propagator", "aigc_propagate_id", "hot_fix":
			input[key] = value
		default:
			return nil, fmt.Errorf("unsupported qwen audio HTTP option %q", key)
		}
	}
	if err := validateOptions(input, p.cfg.NonStreaming); err != nil {
		return nil, err
	}
	return map[string]any{"model": p.cfg.Model, "input": input}, nil
}

func validateOptions(input map[string]any, nonStreaming bool) error {
	for key, bounds := range map[string][2]float64{
		"volume": {0, 100}, "rate": {0.5, 2}, "pitch": {0.5, 2}, "bit_rate": {6, 510}, "seed": {0, 65535},
	} {
		value, ok := input[key]
		if !ok {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", key, err)
		}
		var number float64
		if err := json.Unmarshal(encoded, &number); err != nil || bytes.Equal(encoded, []byte("null")) || !(number >= bounds[0] && number <= bounds[1]) {
			return fmt.Errorf("%s must be a number between %g and %g", key, bounds[0], bounds[1])
		}
		if key == "volume" || key == "bit_rate" || key == "seed" {
			if number != float64(int(number)) {
				return fmt.Errorf("%s must be an integer", key)
			}
		}
	}
	for _, key := range []string{"enable_ssml", "word_timestamp_enabled", "enable_aigc_tag"} {
		if value, ok := input[key]; ok {
			flag, valid := value.(bool)
			if !valid {
				return fmt.Errorf("%s must be a boolean", key)
			}
			if key == "word_timestamp_enabled" && flag && nonStreaming {
				return fmt.Errorf("word_timestamp_enabled requires SSE streaming")
			}
		}
	}
	for _, key := range []string{"aigc_propagator", "aigc_propagate_id"} {
		if value, ok := input[key]; ok {
			if _, valid := value.(string); !valid {
				return fmt.Errorf("%s must be a string", key)
			}
		}
	}
	if value, ok := input["hot_fix"]; ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var hotFix struct {
			Pronunciation []map[string]string `json:"pronunciation"`
			Replace       []map[string]string `json:"replace"`
		}
		if bytes.Equal(encoded, []byte("null")) || json.Unmarshal(encoded, &hotFix) != nil {
			return fmt.Errorf("hot_fix must contain pronunciation/replace arrays of string maps")
		}
	}
	return nil
}

type synthesis struct {
	provider  *Provider
	req       *tts.ProviderSynthesizeRequest
	segmentID string
	events    chan<- *tts.ProviderEvent
	audioSeen bool
	usage     map[string]any
	sentences []map[string]any
}

func (s *synthesis) run(ctx context.Context, body []byte) error {
	p := s.provider
	requestCtx, cancel := transport.RequestContext(ctx, p.cfg.Timeouts)
	defer cancel()
	request := p.cfg.Client.R().SetContext(requestCtx).SetDoNotParseResponse(true).
		SetHeader("Content-Type", "application/json").SetBody(body)
	if p.cfg.NonStreaming {
		request.SetHeader("Accept", "application/json").SetHeader("X-DashScope-SSE", "")
	} else {
		request.SetHeader("Accept", "text/event-stream").SetHeader("X-DashScope-SSE", "enable")
	}
	if auth := cmp.Or(p.cfg.Authorization, bearer(p.cfg.APIKey)); auth != "" {
		request.SetHeader("Authorization", auth)
	}
	resp, err := request.Post(p.cfg.Endpoint)
	transport.GuardHTTPBody(resp, p.cfg.Timeouts)
	if resp != nil && resp.RawBody() != nil {
		defer resp.RawBody().Close()
	}
	if err != nil {
		return err
	}
	if resp == nil || resp.RawBody() == nil {
		return fmt.Errorf("empty HTTP response")
	}
	if resp.IsError() || resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return p.statusError(resp)
	}
	if !s.emit(ctx, &tts.ProviderEvent{Type: tts.ProviderEventSegmentStart}) {
		return ctx.Err()
	}
	if p.cfg.NonStreaming || strings.Contains(resp.Header().Get("Content-Type"), "application/json") {
		data, err := io.ReadAll(io.LimitReader(resp.RawBody(), (2<<20)+1))
		if err != nil {
			return err
		}
		if len(data) > 2<<20 {
			return fmt.Errorf("qwen audio JSON response exceeds 2 MiB")
		}
		result, err := s.decode(data)
		if err != nil {
			return err
		}
		if result.Output.FinishReason != "stop" {
			return fmt.Errorf("qwen audio JSON response is not complete")
		}
		return s.complete(ctx, result)
	}
	parser := &tts.SSEParser{}
	scanner := bufio.NewScanner(resp.RawBody())
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		done, err := s.handleEvent(ctx, parser.FeedLine(strings.TrimPrefix(scanner.Text(), "\ufeff")))
		if err != nil || done {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	done, err := s.handleEvent(ctx, parser.FeedLine(""))
	if err != nil || done {
		return err
	}
	return fmt.Errorf("qwen audio SSE ended before finish_reason=stop: %w", io.ErrUnexpectedEOF)
}

func (s *synthesis) handleEvent(ctx context.Context, event *tts.SSEEvent) (bool, error) {
	if event == nil || strings.TrimSpace(event.Data) == "" {
		return false, nil
	}
	result, err := s.decode([]byte(event.Data))
	if err != nil {
		return false, err
	}
	if event.Event == "error" {
		return false, fmt.Errorf("qwen audio SSE error: %s", result.Message)
	}
	if result.Output.FinishReason == "stop" {
		return true, s.complete(ctx, result)
	}
	return false, s.emitAudioData(ctx, result)
}

func (s *synthesis) decode(data []byte) (*responseBody, error) {
	var result responseBody
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode qwen audio HTTP response: %w", err)
	}
	if result.Code != "" {
		return nil, s.provider.failure(tts.ErrProviderUnavailable,
			fmt.Sprintf("qwen audio %s: %s (request_id=%s)", result.Code, result.Message, result.RequestID), nil)
	}
	if result.Usage != nil {
		s.usage = maps.Clone(result.Usage)
	}
	if result.Output.Type == "sentence-end" {
		s.sentences = append(s.sentences, map[string]any{
			"original_text": result.Output.OriginalText, "sentence": result.Output.Sentence,
		})
	}
	return &result, nil
}

func (s *synthesis) emitAudioData(ctx context.Context, result *responseBody) error {
	if result.Output.Audio.Data == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(result.Output.Audio.Data)
	if err != nil {
		return s.provider.failure(tts.ErrAudioDecodeFailed, "decode qwen audio base64: "+err.Error(), err)
	}
	return s.emitAudio(ctx, data, result.meta())
}

func (s *synthesis) complete(ctx context.Context, result *responseBody) error {
	if err := s.emitAudioData(ctx, result); err != nil {
		return err
	}
	// The final URL duplicates streamed audio. Download only when no inline
	// audio was received, including non-streaming synthesis.
	if !s.audioSeen && result.Output.Audio.URL != "" {
		if err := s.download(ctx, result); err != nil {
			return err
		}
	}
	if !s.audioSeen {
		return fmt.Errorf("qwen audio completed without audio data or an audio URL")
	}
	meta := result.meta()
	meta["usage"] = s.usage
	if len(s.sentences) > 0 {
		meta["sentences"] = s.sentences
	}
	if !s.emit(ctx, &tts.ProviderEvent{Type: tts.ProviderEventSegmentEnd, RawMeta: meta}) {
		return ctx.Err()
	}
	return nil
}

func (s *synthesis) download(ctx context.Context, result *responseBody) error {
	if err := validateURL(result.Output.Audio.URL); err != nil {
		return fmt.Errorf("invalid qwen audio download URL: %w", err)
	}
	requestCtx, cancel := transport.RequestContext(ctx, s.provider.cfg.Timeouts)
	defer cancel()
	resp, err := s.provider.cfg.AudioClient.R().SetContext(requestCtx).SetDoNotParseResponse(true).Get(result.Output.Audio.URL)
	transport.GuardHTTPBody(resp, s.provider.cfg.Timeouts)
	if resp != nil && resp.RawBody() != nil {
		defer resp.RawBody().Close()
	}
	if err != nil {
		return fmt.Errorf("download qwen audio: %w", err)
	}
	if resp == nil || resp.RawBody() == nil {
		return fmt.Errorf("empty audio download response")
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return s.provider.statusError(resp)
	}
	buffer := make([]byte, 4096)
	for {
		n, err := resp.RawBody().Read(buffer)
		if n > 0 {
			if emitErr := s.emitAudio(ctx, bytes.Clone(buffer[:n]), result.meta()); emitErr != nil {
				return emitErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *synthesis) emitAudio(ctx context.Context, data []byte, meta map[string]any) error {
	if len(data) == 0 {
		return nil
	}
	if !s.emit(ctx, &tts.ProviderEvent{Type: tts.ProviderEventAudio, RawMeta: meta,
		Audio: &tts.ProviderAudioChunk{Codec: audio.CodecOpus, Container: audio.ContainerOgg,
			SampleRate: audio.OpusSampleRate, Channels: 1, Data: data}}) {
		return ctx.Err()
	}
	s.audioSeen = true
	return nil
}

func (s *synthesis) emit(ctx context.Context, event *tts.ProviderEvent) bool {
	if ctx.Err() != nil {
		return false
	}
	event.Provider, event.RequestID, event.SegmentID = s.provider.Name(), s.req.RequestID, s.segmentID
	select {
	case <-ctx.Done():
		return false
	case s.events <- event:
		return true
	}
}

func (p *Provider) failure(code tts.ErrorCode, message string, cause error) *tts.Error {
	return &tts.Error{Code: code, Provider: p.Name(), Message: message, Cause: cause}
}

func (p *Provider) statusError(resp *resty.Response) *tts.Error {
	body, _ := io.ReadAll(io.LimitReader(resp.RawBody(), 4096))
	code := tts.ErrProviderUnavailable
	switch resp.StatusCode() {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = tts.ErrProviderAuthFailed
	case http.StatusTooManyRequests:
		code = tts.ErrProviderRateLimited
	}
	err := p.failure(code, fmt.Sprintf("qwen audio HTTP status %d: %s", resp.StatusCode(), body), nil)
	err.Retryable = resp.StatusCode() == http.StatusTooManyRequests || resp.StatusCode() >= 500
	return err
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("an absolute HTTP(S) URL without user credentials is required")
	}
	return nil
}

func bearer(key string) string {
	if key == "" {
		return ""
	}
	return "Bearer " + key
}

var languageMapper = langnorm.NewMapper(langnorm.Map("zh", langnorm.MatchLanguage, "zh", "cmn", "yue"))

func normalizeLanguage(language string) string {
	if langnorm.Primary(language) == langnorm.Auto {
		return ""
	}
	return languageMapper.Resolve(language, langnorm.Primary(language))
}

type responseBody struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Output    struct {
		FinishReason string         `json:"finish_reason"`
		Type         string         `json:"type"`
		OriginalText string         `json:"original_text"`
		Sentence     map[string]any `json:"sentence"`
		Audio        struct {
			Data      string `json:"data"`
			URL       string `json:"url"`
			ID        string `json:"id"`
			ExpiresAt int64  `json:"expires_at"`
		} `json:"audio"`
	} `json:"output"`
	Usage map[string]any `json:"usage"`
}

func (r *responseBody) meta() map[string]any {
	return map[string]any{
		"request_id": r.RequestID, "usage": r.Usage,
		"type": r.Output.Type, "original_text": r.Output.OriginalText, "sentence": r.Output.Sentence,
		"audio":         map[string]any{"url": r.Output.Audio.URL, "id": r.Output.Audio.ID, "expires_at": r.Output.Audio.ExpiresAt},
		"finish_reason": r.Output.FinishReason,
	}
}
