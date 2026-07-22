package vllmtts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
)

func TestProviderStreamsChunkedPCM(t *testing.T) {
	var gotBody requestBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{1, 2})
		_, _ = w.Write([]byte{3, 4})
	}))
	defer server.Close()

	provider, err := NewProvider(Config{
		Name:            "local",
		Endpoint:        server.URL,
		Token:           "test-token",
		DefaultVoice:    "serena",
		DefaultLanguage: "Chinese",
		ChunkSize:       2,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_http",
		Text:      "你好",
		Voice:     "serena",
		Language:  "Chinese",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectProviderEvents(events)
	if len(got) != 4 {
		t.Fatalf("event count = %d, want 4", len(got))
	}
	if got[0].Type != tts.ProviderEventSegmentStart {
		t.Fatalf("event[0] = %q, want segment_start", got[0].Type)
	}
	if got[1].Type != tts.ProviderEventAudio || got[2].Type != tts.ProviderEventAudio {
		t.Fatalf("events[1:3] should be audio events")
	}
	if got[3].Type != tts.ProviderEventSegmentEnd {
		t.Fatalf("event[3] = %q, want segment_end", got[3].Type)
	}
	if got[1].Audio.Codec != audio.CodecPCM {
		t.Fatalf("audio codec = %q, want pcm", got[1].Audio.Codec)
	}
	if got[1].Audio.SampleRate != 24000 {
		t.Fatalf("audio sample rate = %d, want 24000", got[1].Audio.SampleRate)
	}
	if gotBody.Input != "你好" {
		t.Fatalf("input = %q, want 你好", gotBody.Input)
	}
	if gotBody.Voice != "serena" {
		t.Fatalf("voice = %q, want serena", gotBody.Voice)
	}
	if !gotBody.Stream {
		t.Fatal("stream = false, want true")
	}
	if gotBody.ResponseFormat != "pcm" {
		t.Fatalf("response_format = %q, want pcm", gotBody.ResponseFormat)
	}
	if gotBody.Language != "Chinese" {
		t.Fatalf("language = %q, want Chinese", gotBody.Language)
	}
}

func TestProviderDefaultChunkSizeIsPCM20msFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(bytes.Repeat([]byte{1}, defaultChunkSize+60))
	}))
	defer server.Close()

	provider, err := NewProvider(Config{
		Name:     "local",
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_http",
		Text:      "你好",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectProviderEvents(events)
	if len(got) != 4 {
		t.Fatalf("event count = %d, want 4", len(got))
	}
	if len(got[1].Audio.Data) != defaultChunkSize {
		t.Fatalf("first chunk length = %d, want %d", len(got[1].Audio.Data), defaultChunkSize)
	}
	if len(got[2].Audio.Data) != 60 {
		t.Fatalf("second chunk length = %d, want 60", len(got[2].Audio.Data))
	}
	if defaultChunkSize != 960 {
		t.Fatalf("defaultChunkSize = %d, want 960", defaultChunkSize)
	}
}

func TestProviderMapsHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer server.Close()

	provider, err := NewProvider(Config{Name: "local", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_http",
		Text:      "你好",
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

func TestCapabilitiesWithoutVoiceOrLanguageRestriction(t *testing.T) {
	provider, err := NewProvider(Config{
		Name:            "local",
		Endpoint:        "http://127.0.0.1:9012/v1/audio/speech",
		DefaultVoice:    "serena",
		DefaultLanguage: "Chinese",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.Voices) != 0 {
		t.Fatalf("voices = %#v, want no platform voice restriction", caps.Voices)
	}
	if len(caps.Languages) != 0 {
		t.Fatalf("languages = %#v, want no platform language restriction", caps.Languages)
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != 24000 {
		t.Fatalf("output sample rates = %#v, want 24000", caps.OutputSampleRates)
	}
}

func collectProviderEvents(events <-chan *tts.ProviderEvent) []*tts.ProviderEvent {
	var got []*tts.ProviderEvent
	for event := range events {
		got = append(got, event)
	}
	return got
}
