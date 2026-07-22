package microsofttts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

func TestProviderStreamsOggOpus(t *testing.T) {
	firstAudio := []byte("ogg-page-1")
	secondAudio := []byte("ogg-page-2")
	wantAudio := append(append([]byte(nil), firstAudio...), secondAudio...)
	var gotBody string

	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Ocp-Apim-Subscription-Key"); got != "test-key" {
			t.Fatalf("subscription key = %q, want test-key", got)
		}
		if got := r.Header.Get("X-Microsoft-OutputFormat"); got != defaultOutputFormat {
			t.Fatalf("output format = %q, want %s", got, defaultOutputFormat)
		}
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/ssml+xml") {
			t.Fatalf("content-type = %q, want application/ssml+xml", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/ogg"}},
			Body:       io.NopCloser(bytes.NewReader(wantAudio)),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{
		Name:            "microsoft",
		Endpoint:        "https://example.test/cognitiveservices/v1",
		SubscriptionKey: "test-key",
		DefaultVoice:    "zh-CN-XiaoxiaoNeural",
		DefaultLanguage: "zh-CN",
		Client:          client,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_ms",
		Text:      "你好 <world>",
		Voice:     "en-US-JennyNeural",
		Language:  "en",
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
	if string(audioData) != string(wantAudio) {
		t.Fatalf("audio data = %q, want %q", string(audioData), string(wantAudio))
	}

	if !strings.Contains(gotBody, "xml:lang='en-US'") {
		t.Fatalf("body = %q, want normalized en-US language", gotBody)
	}
	if !strings.Contains(gotBody, "voice name='en-US-JennyNeural'") {
		t.Fatalf("body = %q, want request voice", gotBody)
	}
	if !strings.Contains(gotBody, "你好 &lt;world&gt;") {
		t.Fatalf("body = %q, want XML-escaped text", gotBody)
	}
}

func TestProviderMapsHTTPStatusError(t *testing.T) {
	client := resty.NewWithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("nope")),
			Request:    r,
		}, nil
	})})

	provider, err := NewProvider(Config{Name: "microsoft", Endpoint: "https://example.test/cognitiveservices/v1", Client: client})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	events, err := provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{
		RequestID: "req_ms",
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

func TestCapabilitiesWithoutVoiceRestriction(t *testing.T) {
	provider, err := NewProvider(Config{
		Name:         "microsoft",
		Endpoint:     "https://example.test/cognitiveservices/v1",
		DefaultVoice: "zh-CN-XiaoxiaoNeural",
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
	provider, err := NewProvider(Config{Name: "microsoft", Endpoint: "https://example.test"})
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

func TestResolveLanguage(t *testing.T) {
	tests := []struct {
		name              string
		requestLanguage   string
		voice             string
		configuredDefault string
		want              string
	}{
		{"request locale wins", "en-GB", "en-US-JennyNeural", "fr-FR", "en-GB"},
		{"voice completes request language", "eng", "en-US-JennyNeural", "fr-FR", "en-US"},
		{"different request language stays unchanged", "fr", "en-US-AvaMultilingualNeural", "", "fr"},
		{"voice locale when request omitted", "", "zh-CN-XiaoxiaoNeural", "en-US", "zh-CN"},
		{"voice locale with script", "", "sr-Latn-RS-TestNeural", "", "sr-Latn-RS"},
		{"configured default for custom voice", "", "my-custom-voice", "pt-BR", "pt-BR"},
		{"platform fallback", "", "my-custom-voice", "", defaultLanguage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLanguage(tt.requestLanguage, tt.voice, tt.configuredDefault); got != tt.want {
				t.Fatalf("resolveLanguage(%q, %q, %q) = %q, want %q", tt.requestLanguage, tt.voice, tt.configuredDefault, got, tt.want)
			}
		})
	}
}

func TestLanguageFromVoiceRejectsNonstandardVoiceNames(t *testing.T) {
	for _, voice := range []string{"", "custom-voice", "Microsoft Server Speech Voice"} {
		if got := languageFromVoice(voice); got != "" {
			t.Fatalf("languageFromVoice(%q) = %q, want empty", voice, got)
		}
	}
}

func collectProviderEvents(events <-chan *tts.ProviderEvent) []*tts.ProviderEvent {
	var got []*tts.ProviderEvent
	for event := range events {
		got = append(got, event)
	}
	return got
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
