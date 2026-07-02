package geminitts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

func TestProviderStreamsPCMFromSSE(t *testing.T) {
	pcm := makePCM(960)
	encoded := base64.StdEncoding.EncodeToString(pcm)
	body := "event: step.delta\n" +
		"data: {\"event_type\":\"step.delta\",\"delta\":{\"type\":\"audio\",\"data\":\"" + encoded + "\"}}\n\n" +
		"event: step.completed\n" +
		"data: {\"event_type\":\"step.completed\"}\n\n" +
		"event: step.delta\n" +
		"data: not-json-after-completion\n\n"
	var gotBody requestBody

	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.String() != "https://example.test/v1beta/interactions" {
			t.Fatalf("url = %s", r.URL.String())
		}
		if got := r.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Fatalf("x-goog-api-key = %q, want test-key", got)
		}
		if got := r.Header.Get("Api-Revision"); got != "2026-05-20" {
			t.Fatalf("api revision = %q, want 2026-05-20", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("accept = %q, want text/event-stream", got)
		}

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{
		Name:                "gemini",
		Endpoint:            "https://example.test/v1beta/interactions",
		APIKey:              "test-key",
		Model:               "gemini-3.1-flash-tts-preview",
		DefaultVoice:        "Kore",
		DefaultInstructions: "Say cheerfully:",
		Client:              client,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_gemini",
		Text:      "Have a wonderful day!",
		Voice:     "Puck",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectProviderEvents(events)
	if len(got) != 3 {
		t.Fatalf("event count = %d, want 3", len(got))
	}
	if got[0].Type != tts.ProviderEventSegmentStart {
		t.Fatalf("event[0] = %q, want segment_start", got[0].Type)
	}
	if got[1].Type != tts.ProviderEventAudio {
		t.Fatalf("event[1] = %q, want audio", got[1].Type)
	}
	if got[2].Type != tts.ProviderEventSegmentEnd {
		t.Fatalf("event[2] = %q, want segment_end", got[2].Type)
	}
	if got[1].Audio.Codec != audio.CodecPCM {
		t.Fatalf("audio codec = %q, want pcm", got[1].Audio.Codec)
	}
	if got[1].Audio.SampleRate != defaultSampleRate {
		t.Fatalf("audio sample rate = %d, want %d", got[1].Audio.SampleRate, defaultSampleRate)
	}
	if !bytes.Equal(got[1].Audio.Data, pcm) {
		t.Fatalf("audio data length = %d, want %d", len(got[1].Audio.Data), len(pcm))
	}
	if gotBody.Model != "gemini-3.1-flash-tts-preview" {
		t.Fatalf("model = %q", gotBody.Model)
	}
	if gotBody.Input != "Say cheerfully:\n\nHave a wonderful day!" {
		t.Fatalf("input = %q", gotBody.Input)
	}
	if gotBody.ResponseFormat.Type != "audio" {
		t.Fatalf("response format = %q", gotBody.ResponseFormat.Type)
	}
	if len(gotBody.GenerationConfig.SpeechConfig) != 1 || gotBody.GenerationConfig.SpeechConfig[0].Voice != "Puck" {
		t.Fatalf("speech config = %#v", gotBody.GenerationConfig.SpeechConfig)
	}
	if !gotBody.Stream {
		t.Fatal("stream = false, want true")
	}
}

func TestProviderStreamsPCMFromJSONLines(t *testing.T) {
	pcm := makePCM(960)
	encoded := base64.StdEncoding.EncodeToString(pcm)
	body := "{\"event_type\":\"step.delta\",\"delta\":{\"type\":\"audio\",\"data\":\"" + encoded + "\"}}\n"

	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "gemini", Endpoint: "https://example.test/v1beta/interactions", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{Text: "hello"})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectProviderEvents(events)
	var audioEvents int
	for _, event := range got {
		if event.Type == tts.ProviderEventAudio {
			audioEvents++
			if !bytes.Equal(event.Audio.Data, pcm) {
				t.Fatalf("audio data length = %d, want %d", len(event.Audio.Data), len(pcm))
			}
		}
		if event.Type == tts.ProviderEventError {
			t.Fatalf("unexpected error event: %#v", event.Error)
		}
	}
	if audioEvents != 1 {
		t.Fatalf("audio events = %d, want 1", audioEvents)
	}
}

func TestProviderEndsAfterAudioIdleWhenBodyDoesNotClose(t *testing.T) {
	pcm := makePCM(960)
	encoded := base64.StdEncoding.EncodeToString(pcm)
	body := "data: {\"event_type\":\"step.delta\",\"delta\":{\"type\":\"audio\",\"data\":\"" + encoded + "\"}}\n\n"

	blockingBody := newBlockingReadCloser(body)
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       blockingBody,
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{
		Name:             "gemini",
		Endpoint:         "https://example.test/v1beta/interactions",
		AudioIdleTimeout: 20 * time.Millisecond,
		Client:           client,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{Text: "hello"})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectProviderEvents(events)
	if len(got) != 3 {
		t.Fatalf("event count = %d, want 3", len(got))
	}
	if got[1].Type != tts.ProviderEventAudio {
		t.Fatalf("event[1] = %q, want audio", got[1].Type)
	}
	if got[2].Type != tts.ProviderEventSegmentEnd {
		t.Fatalf("event[2] = %q, want segment_end", got[2].Type)
	}
}

func TestServiceNormalizesGeminiPCM(t *testing.T) {
	pcm := makePCM(24000 * 20 / 1000 * 2)
	encoded := base64.StdEncoding.EncodeToString(pcm)
	body := "data: {\"event_type\":\"step.delta\",\"delta\":{\"type\":\"audio\",\"data\":\"" + encoded + "\"}}\n\n"

	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "gemini", Endpoint: "https://example.test/v1beta/interactions", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := tts.NewService("test", registry)

	events, err := service.SynthesizeOnce(context.Background(), &tts.SynthesizeRequest{
		RequestID: "req_gemini",
		Provider:  "gemini",
		Text:      "hello",
		Output: audio.OutputConfig{
			PreferCodec:         audio.CodecPCM,
			SampleRate:          audio.DefaultSampleRate,
			Channels:            audio.DefaultChannels,
			FrameMS:             audio.DefaultFrameMS,
			PCMFormat:           audio.PCMFormatS16LE,
			AllowPCMFrameOutput: true,
		},
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectEvents(events)
	var frames []*audio.Frame
	for _, event := range got {
		if event.Type == tts.EventAudioFrame {
			frames = append(frames, event.Audio)
		}
		if event.Type == tts.EventError {
			t.Fatalf("unexpected error event: %#v", event.Error)
		}
	}
	if len(frames) == 0 {
		t.Fatalf("frames = 0; events=%#v", got)
	}
	if frames[0].Codec != audio.CodecPCM {
		t.Fatalf("frame codec = %q, want pcm", frames[0].Codec)
	}
	if frames[0].SampleRate != audio.DefaultSampleRate {
		t.Fatalf("frame sample rate = %d, want %d", frames[0].SampleRate, audio.DefaultSampleRate)
	}
	if len(frames[0].Data) != audio.DefaultSampleRate*audio.DefaultFrameMS/1000*2 {
		t.Fatalf("frame bytes = %d", len(frames[0].Data))
	}
}

func TestProviderUsesOptionsModel(t *testing.T) {
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body requestBody
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body.Model != "gemini-2.5-flash-preview-tts" {
			t.Fatalf("model = %q", body.Model)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "gemini", Endpoint: "https://example.test/v1beta/interactions", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		Text:    "hello",
		Options: map[string]any{"model": "gemini-2.5-flash-preview-tts"},
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}
	_ = collectProviderEvents(events)
}

func TestProviderMapsHTTPStatusError(t *testing.T) {
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad key"}}`)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "gemini", Endpoint: "https://example.test/v1beta/interactions", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_gemini",
		Text:      "hello",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectProviderEvents(events)
	if len(got) != 1 {
		t.Fatalf("event count = %d, want 1", len(got))
	}
	if got[0].Type != tts.ProviderEventError {
		t.Fatalf("event type = %q, want error", got[0].Type)
	}
	if got[0].Error == nil || got[0].Error.Code != tts.ErrProviderAuthFailed {
		t.Fatalf("error = %#v, want auth failed", got[0].Error)
	}
}

func TestCapabilities(t *testing.T) {
	provider, err := NewProvider(Config{Name: "gemini"})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.SupportsPCMOutput {
		t.Fatal("SupportsPCMOutput = false, want true")
	}
	if !caps.SupportsGuidanceText {
		t.Fatal("SupportsGuidanceText = false, want true")
	}
	if caps.SupportsAppendText {
		t.Fatal("SupportsAppendText = true, want false")
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != defaultSampleRate {
		t.Fatalf("output sample rates = %#v, want %d", caps.OutputSampleRates, defaultSampleRate)
	}
}

func TestProviderOpenSessionUnsupported(t *testing.T) {
	provider, err := NewProvider(Config{Name: "gemini"})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = provider.OpenSession(context.Background(), &tts.ProviderOpenSessionRequest{SessionID: "sess"})
	if err == nil {
		t.Fatal("OpenSession returned nil error, want unsupported feature")
	}
	ttsErr, ok := err.(*tts.Error)
	if !ok {
		t.Fatalf("error type = %T, want *tts.Error", err)
	}
	if ttsErr.Code != tts.ErrUnsupportedFeature {
		t.Fatalf("error code = %q, want unsupported feature", ttsErr.Code)
	}
}

func makePCM(size int) []byte {
	pcm := make([]byte, size)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	return pcm
}

func collectProviderEvents(events <-chan *tts.ProviderEvent) []*tts.ProviderEvent {
	var got []*tts.ProviderEvent
	for event := range events {
		got = append(got, event)
	}
	return got
}

func collectEvents(events <-chan *tts.Event) []*tts.Event {
	var got []*tts.Event
	for event := range events {
		got = append(got, event)
	}
	return got
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type blockingReadCloser struct {
	reader *strings.Reader
	closed chan struct{}
}

func newBlockingReadCloser(data string) *blockingReadCloser {
	return &blockingReadCloser{
		reader: strings.NewReader(data),
		closed: make(chan struct{}),
	}
}

func (r *blockingReadCloser) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *blockingReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}
