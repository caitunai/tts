package openaitts

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

func TestProviderStreamsOggOpus(t *testing.T) {
	oggStream := makeOggStream(t, []byte{0x78, 0x11, 0x22})
	var gotBody requestBody

	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.String() != "https://example.test/v1/audio/speech" {
			t.Fatalf("url = %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want Bearer test-key", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("content-type = %q, want application/json", got)
		}
		if got := r.Header.Get("Accept"); got != "audio/ogg" {
			t.Fatalf("accept = %q, want audio/ogg", got)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/ogg"}},
			Body:       io.NopCloser(bytes.NewReader(oggStream)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{
		Name:         "openai",
		Endpoint:     "https://example.test/v1/audio/speech",
		APIKey:       "test-key",
		Model:        "gpt-4o-mini-tts",
		DefaultVoice: "coral",
		Speed:        1.2,
		Client:       client,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID:    "req_openai",
		Text:         "Hello, welcome to OpenAI TTS!",
		Voice:        "marin",
		GuidanceText: "Speak warmly.",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectProviderEvents(events)
	if len(got) < 3 {
		t.Fatalf("event count = %d, want at least segment_start, audio, segment_end", len(got))
	}
	if got[0].Type != tts.ProviderEventSegmentStart {
		t.Fatalf("event[0] = %q, want segment_start", got[0].Type)
	}
	if got[len(got)-1].Type != tts.ProviderEventSegmentEnd {
		t.Fatalf("last event = %q, want segment_end", got[len(got)-1].Type)
	}

	var audioData []byte
	for _, event := range got[1 : len(got)-1] {
		if event.Type != tts.ProviderEventAudio {
			t.Fatalf("middle event = %q, want audio", event.Type)
		}
		if event.Audio.Codec != audio.CodecOpus {
			t.Fatalf("audio codec = %q, want opus", event.Audio.Codec)
		}
		if event.Audio.Container != audio.ContainerOgg {
			t.Fatalf("audio container = %q, want ogg", event.Audio.Container)
		}
		if event.Audio.SampleRate != audio.OpusSampleRate {
			t.Fatalf("audio sample rate = %d, want %d", event.Audio.SampleRate, audio.OpusSampleRate)
		}
		audioData = append(audioData, event.Audio.Data...)
	}
	if !bytes.Equal(audioData, oggStream) {
		t.Fatalf("audio data length = %d, want %d", len(audioData), len(oggStream))
	}
	if gotBody.Model != "gpt-4o-mini-tts" {
		t.Fatalf("model = %q", gotBody.Model)
	}
	if gotBody.Input != "Hello, welcome to OpenAI TTS!" {
		t.Fatalf("input = %q", gotBody.Input)
	}
	if gotBody.Voice != "marin" {
		t.Fatalf("voice = %q", gotBody.Voice)
	}
	if gotBody.Instructions != "Speak warmly." {
		t.Fatalf("instructions = %q", gotBody.Instructions)
	}
	if gotBody.ResponseFormat != "opus" {
		t.Fatalf("response_format = %q", gotBody.ResponseFormat)
	}
	if gotBody.StreamFormat != "audio" {
		t.Fatalf("stream_format = %q", gotBody.StreamFormat)
	}
	if gotBody.Speed != 1.2 {
		t.Fatalf("speed = %v", gotBody.Speed)
	}
}

func TestServiceDemuxesOpenAIOggOpus(t *testing.T) {
	firstPacket := []byte{0x78, 0x11, 0x22}
	secondPacket := []byte{0x78, 0x33}
	oggStream := makeOggStream(t, firstPacket, secondPacket)

	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/ogg"}},
			Body:       io.NopCloser(bytes.NewReader(oggStream)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "openai", Endpoint: "https://example.test/v1/audio/speech", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := tts.NewService("test", registry)

	events, err := service.SynthesizeOnce(context.Background(), &tts.SynthesizeRequest{
		RequestID: "req_openai",
		Provider:  "openai",
		Text:      "hello",
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
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2; events=%#v", len(frames), got)
	}
	if !bytes.Equal(frames[0].Data, firstPacket) {
		t.Fatalf("first frame = %v, want %v", frames[0].Data, firstPacket)
	}
	if !bytes.Equal(frames[1].Data, secondPacket) {
		t.Fatalf("second frame = %v, want %v", frames[1].Data, secondPacket)
	}
	if frames[0].Container != audio.ContainerRaw {
		t.Fatalf("frame container = %q, want raw", frames[0].Container)
	}
	if frames[0].SampleRate != audio.OpusSampleRate {
		t.Fatalf("frame sample rate = %d, want %d", frames[0].SampleRate, audio.OpusSampleRate)
	}
}

func TestProviderOmitsInstructionsForLegacyModels(t *testing.T) {
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body requestBody
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body.Instructions != "" {
			t.Fatalf("instructions = %q, want omitted for tts-1", body.Instructions)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/ogg"}},
			Body:       io.NopCloser(bytes.NewReader(makeOggStream(t, []byte{0x78, 0x11}))),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "openai", Endpoint: "https://example.test/v1/audio/speech", Model: "tts-1", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		Text:         "hello",
		GuidanceText: "Speak brightly.",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}
	_ = collectProviderEvents(events)
}

func TestProviderUsesOptionsModelAndSpeed(t *testing.T) {
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body requestBody
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body.Model != "gpt-4o-mini-tts-2025-12-15" {
			t.Fatalf("model = %q", body.Model)
		}
		if body.Speed != 1.5 {
			t.Fatalf("speed = %v", body.Speed)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/ogg"}},
			Body:       io.NopCloser(bytes.NewReader(makeOggStream(t, []byte{0x78, 0x11}))),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "openai", Endpoint: "https://example.test/v1/audio/speech", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		Text: "hello",
		Options: map[string]any{
			"model": "gpt-4o-mini-tts-2025-12-15",
			"speed": 1.5,
		},
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
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad token"}}`)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "openai", Endpoint: "https://example.test/v1/audio/speech", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_openai",
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
	provider, err := NewProvider(Config{Name: "openai"})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.SupportsOggOpusOutput {
		t.Fatal("SupportsOggOpusOutput = false, want true")
	}
	if !caps.SupportsGuidanceText {
		t.Fatal("SupportsGuidanceText = false, want true")
	}
	if caps.SupportsAppendText {
		t.Fatal("SupportsAppendText = true, want false")
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != audio.OpusSampleRate {
		t.Fatalf("output sample rates = %#v, want %d", caps.OutputSampleRates, audio.OpusSampleRate)
	}
}

func TestProviderOpenSessionUnsupported(t *testing.T) {
	provider, err := NewProvider(Config{Name: "openai"})
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

func makeOggStream(t *testing.T, packets ...[]byte) []byte {
	t.Helper()

	var out bytes.Buffer
	muxer := audio.NewOggOpusMuxer()
	for _, packet := range packets {
		if err := muxer.WritePacket(&out, packet); err != nil {
			t.Fatalf("WritePacket: %v", err)
		}
	}
	if err := muxer.Finish(&out); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return out.Bytes()
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
