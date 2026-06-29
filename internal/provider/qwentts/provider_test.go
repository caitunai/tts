package qwentts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
)

func TestProviderStreamsSSEPCM(t *testing.T) {
	var gotBody requestBody
	firstAudio := []byte{1, 2, 3, 4}
	secondAudio := []byte{5, 6}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("X-DashScope-SSE"); got != "enable" {
			t.Fatalf("X-DashScope-SSE = %q, want enable", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q, want text/event-stream", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, `{"output":{"audio":{}}}`)
		writeSSE(t, w, `{"output":{"audio":{"data":"`+base64.StdEncoding.EncodeToString(firstAudio)+`"}}}`)
		writeSSE(t, w, `{"output":{"audio":{"data":"`+base64.StdEncoding.EncodeToString(secondAudio)+`"}}}`)
	}))
	defer server.Close()

	provider, err := NewProvider(Config{
		Name:            "qwen",
		Endpoint:        server.URL,
		Token:           "test-token",
		Model:           "qwen-tts-test",
		DefaultVoice:    "Cherry",
		DefaultLanguage: "zh",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_qwen",
		Text:      "你好",
		Voice:     "Dylan",
		Language:  "en",
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
	if string(got[1].Audio.Data) != string(firstAudio) {
		t.Fatalf("first audio = %v, want %v", got[1].Audio.Data, firstAudio)
	}
	if string(got[2].Audio.Data) != string(secondAudio) {
		t.Fatalf("second audio = %v, want %v", got[2].Audio.Data, secondAudio)
	}

	if gotBody.Model != "qwen-tts-test" {
		t.Fatalf("model = %q, want qwen-tts-test", gotBody.Model)
	}
	if gotBody.Input.Text != "你好" {
		t.Fatalf("text = %q, want 你好", gotBody.Input.Text)
	}
	if gotBody.Input.Voice != "Dylan" {
		t.Fatalf("voice = %q, want Dylan", gotBody.Input.Voice)
	}
	if gotBody.Input.LanguageType != English {
		t.Fatalf("language_type = %q, want English", gotBody.Input.LanguageType)
	}
}

func TestProviderUsesConfiguredDefaults(t *testing.T) {
	var gotBody requestBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, `{"output":{"audio":{}}}`)
	}))
	defer server.Close()

	provider, err := NewProvider(Config{
		Endpoint:        server.URL,
		DefaultVoice:    "Cherry",
		DefaultLanguage: "zh",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_defaults",
		Text:      "你好",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}
	_ = collectProviderEvents(events)

	if gotBody.Model != defaultModel {
		t.Fatalf("model = %q, want %s", gotBody.Model, defaultModel)
	}
	if gotBody.Input.Voice != "Cherry" {
		t.Fatalf("voice = %q, want Cherry", gotBody.Input.Voice)
	}
	if gotBody.Input.LanguageType != Chinese {
		t.Fatalf("language_type = %q, want Chinese", gotBody.Input.LanguageType)
	}
}

func TestProviderMapsHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer server.Close()

	provider, err := NewProvider(Config{Name: "qwen", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_qwen",
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

func TestProviderMapsInvalidBase64Audio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, `{"output":{"audio":{"data":"not-base64"}}}`)
	}))
	defer server.Close()

	provider, err := NewProvider(Config{Name: "qwen", Endpoint: server.URL})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_qwen",
		Text:      "你好",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectProviderEvents(events)
	if len(got) != 2 {
		t.Fatalf("event count = %d, want segment_start and error", len(got))
	}
	if got[0].Type != tts.ProviderEventSegmentStart {
		t.Fatalf("event[0] = %q, want segment_start", got[0].Type)
	}
	if got[1].Type != tts.ProviderEventError {
		t.Fatalf("event[1] = %q, want error", got[1].Type)
	}
	if got[1].Error == nil || got[1].Error.Code != tts.ErrAudioDecodeFailed {
		t.Fatalf("error = %#v, want audio decode failed", got[1].Error)
	}
}

func TestCapabilitiesIncludeConfiguredVoiceAndLanguages(t *testing.T) {
	provider, err := NewProvider(Config{
		Name:         "qwen",
		Endpoint:     "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
		DefaultVoice: "Cherry",
		SampleRate:   48000,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.Voices) != 1 || caps.Voices[0].ID != "Cherry" {
		t.Fatalf("voices = %#v, want Cherry", caps.Voices)
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != 48000 {
		t.Fatalf("output sample rates = %#v, want 48000", caps.OutputSampleRates)
	}
	if len(caps.Languages) != 11 {
		t.Fatalf("languages length = %d, want 11", len(caps.Languages))
	}
	if caps.SupportsAppendText {
		t.Fatal("SupportsAppendText = true, want false")
	}
	if caps.SupportsOggOpusOutput {
		t.Fatal("SupportsOggOpusOutput = true, want false")
	}
	if containsTransport(caps.Transports, tts.TransportWebSocket) {
		t.Fatalf("transports = %#v, should not include websocket", caps.Transports)
	}
}

func TestProviderOpenSessionUnsupported(t *testing.T) {
	provider, err := NewProvider(Config{Name: "qwen", Endpoint: "http://127.0.0.1/qwen"})
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

func TestRewriteLang(t *testing.T) {
	tests := map[string]string{
		"zh":      Chinese,
		"en":      English,
		"de":      German,
		"it":      Italian,
		"pt":      Portuguese,
		"es":      Spanish,
		"ja":      Japanese,
		"ko":      Korean,
		"fr":      French,
		"ru":      Russian,
		"Chinese": Chinese,
		"unknown": Auto,
		"":        Auto,
	}

	for input, want := range tests {
		if got := rewriteLang(input); got != want {
			t.Fatalf("rewriteLang(%q) = %q, want %q", input, got, want)
		}
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, data string) {
	t.Helper()
	if _, err := w.Write([]byte("data: " + data + "\n\n")); err != nil {
		t.Fatalf("write SSE: %v", err)
	}
}

func collectProviderEvents(events <-chan *tts.ProviderEvent) []*tts.ProviderEvent {
	var got []*tts.ProviderEvent
	for event := range events {
		got = append(got, event)
	}
	return got
}

func containsTransport(values []tts.TransportType, target tts.TransportType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
