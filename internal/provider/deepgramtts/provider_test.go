package deepgramtts

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
		if got := r.Header.Get("Authorization"); got != "Token test-key" {
			t.Fatalf("authorization = %q, want Token test-key", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("content-type = %q, want application/json", got)
		}
		if got := r.Header.Get("Accept"); got != "audio/ogg" {
			t.Fatalf("accept = %q, want audio/ogg", got)
		}
		query := r.URL.Query()
		if got := query.Get("model"); got != "aura-2-thalia-en" {
			t.Fatalf("model = %q, want aura-2-thalia-en", got)
		}
		if got := query.Get("encoding"); got != "opus" {
			t.Fatalf("encoding = %q, want opus", got)
		}
		if got := query.Get("container"); got != "ogg" {
			t.Fatalf("container = %q, want ogg", got)
		}
		if got := query.Get("sample_rate"); got != "" {
			t.Fatalf("sample_rate = %q, want empty for opus", got)
		}
		if got := query.Get("bit_rate"); got != "48000" {
			t.Fatalf("bit_rate = %q, want 48000", got)
		}
		if got := query.Get("speed"); got != "1.2" {
			t.Fatalf("speed = %q, want 1.2", got)
		}
		if got := query.Get("tag"); got != "unit-test" {
			t.Fatalf("tag = %q, want unit-test", got)
		}
		if got := query.Get("mip_opt_out"); got != "true" {
			t.Fatalf("mip_opt_out = %q, want true", got)
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
		Name:      "deepgram",
		Endpoint:  "https://example.test/v1/speak",
		APIKey:    "test-key",
		Model:     "aura-2-asteria-en",
		Speed:     1.2,
		Tag:       "unit-test",
		MIPOptOut: true,
		Client:    client,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_dg",
		Text:      "Hello, welcome to Deepgram!",
		Voice:     "aura-2-thalia-en",
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
	if gotBody.Text != "Hello, welcome to Deepgram!" {
		t.Fatalf("body text = %q", gotBody.Text)
	}
}

func TestServiceDemuxesDeepgramOggOpus(t *testing.T) {
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

	provider, err := NewProvider(Config{Name: "deepgram", Endpoint: "https://example.test/v1/speak", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := tts.NewService("test", registry)

	events, err := service.SynthesizeOnce(context.Background(), &tts.SynthesizeRequest{
		RequestID: "req_dg",
		Provider:  "deepgram",
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

func TestProviderUsesOptionsModelBeforeVoice(t *testing.T) {
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Query().Get("model"); got != "aura-2-orpheus-en" {
			t.Fatalf("model = %q, want aura-2-orpheus-en", got)
		}
		if got := r.URL.Query().Get("speed"); got != "" {
			t.Fatalf("speed = %q, want empty by default", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/ogg"}},
			Body:       io.NopCloser(bytes.NewReader(makeOggStream(t, []byte{0x78, 0x11}))),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "deepgram", Endpoint: "https://example.test/v1/speak", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		Text:    "hello",
		Voice:   "aura-2-thalia-en",
		Options: map[string]any{"model": "aura-2-orpheus-en"},
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
			Body:       io.NopCloser(strings.NewReader(`{"message":"bad token"}`)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "deepgram", Endpoint: "https://example.test/v1/speak", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_dg",
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
	provider, err := NewProvider(Config{Name: "deepgram"})
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
	if caps.SupportsAppendText {
		t.Fatal("SupportsAppendText = true, want false")
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != audio.OpusSampleRate {
		t.Fatalf("output sample rates = %#v, want %d", caps.OutputSampleRates, audio.OpusSampleRate)
	}
}

func TestProviderOpenSessionUnsupported(t *testing.T) {
	provider, err := NewProvider(Config{Name: "deepgram"})
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
