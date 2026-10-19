package qwenaudiohttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

func TestHTTPModesThroughService(t *testing.T) {
	for _, tc := range []struct {
		name, model  string
		nonStreaming bool
	}{
		{"3.0 SSE", ModelQwenAudio30TTSFlash, false},
		{"3.1 SSE", ModelQwenAudio31TTSFlash, false},
		{"3.1 JSON", ModelQwenAudio31TTSFlash, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ogg := testOgg(t)
			var downloads atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/audio" {
					downloads.Add(1)
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("X-DashScope-SSE") != "" {
						t.Errorf("audio download inherited synthesis headers: %#v", r.Header)
					}
					_, _ = w.Write(ogg)
					return
				}
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected synthesis method/headers")
				}
				var body struct {
					Model string         `json:"model"`
					Input map[string]any `json:"input"`
				}
				if err := json.UnmarshalRead(r.Body, &body); err != nil {
					t.Error(err)
					return
				}
				if body.Model != tc.model {
					t.Errorf("model = %s, want %s", body.Model, tc.model)
				}
				for key, want := range map[string]any{
					"text": "First. Second.", "voice": "custom-voice", "format": "opus",
					"sample_rate": float64(48000), "bit_rate": float64(48), "volume": float64(0),
					"rate": 1.2, "pitch": 0.9, "seed": float64(123), "instruction": "warm narration",
					"enable_ssml": true, "enable_aigc_tag": true, "aigc_propagator": "app", "aigc_propagate_id": "id",
				} {
					if got := body.Input[key]; got != want {
						t.Errorf("input[%s] = %v, want %v", key, got, want)
					}
				}
				langs, ok := body.Input["language_hints"].([]any)
				if !ok || len(langs) != 1 || langs[0] != "zh" {
					t.Errorf("language_hints = %#v", body.Input["language_hints"])
				}
				if _, ok := body.Input["hot_fix"]; !ok {
					t.Error("missing hot_fix")
				}
				usage := map[string]any{"characters": 14}
				if tc.model == ModelQwenAudio31TTSFlash {
					usage = map[string]any{"input_tokens": 5, "output_tokens": 6, "total_tokens": 11}
				}
				final := map[string]any{
					"request_id": "upstream-id", "usage": usage,
					"output": map[string]any{"finish_reason": "stop", "audio": map[string]any{"url": server.URL + "/audio", "id": "audio-id", "expires_at": 123456}},
				}
				if tc.nonStreaming {
					if r.Header.Get("X-DashScope-SSE") != "" {
						t.Error("JSON request enables SSE")
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.MarshalWrite(w, final)
					return
				}
				if r.Header.Get("X-DashScope-SSE") != "enable" {
					t.Error("SSE header missing")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, ": heartbeat\r\n\r\n")
				writeSSE(t, w, map[string]any{"output": map[string]any{"type": "sentence-begin", "original_text": "First."}})
				for i := range 2 {
					writeSSE(t, w, map[string]any{"output": map[string]any{"type": "sentence-synthesis", "finish_reason": nil,
						"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(ogg[i*10 : (i+1)*10])}}})
				}
				writeSSE(t, w, map[string]any{"output": map[string]any{"type": "sentence-end", "original_text": "First.",
					"sentence": map[string]any{"index": 0, "words": []any{map[string]any{"text": "First", "begin_time": 0, "end_time": 100}}}}})
				final["output"].(map[string]any)["audio"].(map[string]any)["data"] = base64.StdEncoding.EncodeToString(ogg[20:])
				writeSSE(t, w, final)
				w.(http.Flusher).Flush()
				// The client must stop at finish_reason=stop, not wait for EOF.
				<-r.Context().Done()
			}))
			defer server.Close()
			p := mustProvider(t, Config{Endpoint: server.URL + "/tts", APIKey: "secret", Model: tc.model,
				DefaultVoice: "default", DefaultInstructions: "default style", NonStreaming: tc.nonStreaming,
				Client: resty.New().SetHeader("Authorization", "Bearer client-default")})
			caps, _ := p.Capabilities(t.Context())
			if caps.SupportsStreaming == tc.nonStreaming || !caps.SupportsGuidanceText || caps.SupportsAppendText {
				t.Fatalf("capabilities = %#v", caps)
			}
			registry := registryprovider.NewRegistry()
			if err := registry.Register(p); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			events, err := tts.NewService("test", registry).SynthesizeOnce(ctx, &tts.SynthesizeRequest{
				RequestID: "request", Provider: p.Name(), Text: "First. Second.", Voice: "custom-voice", Language: "cmn-CN", GuidanceText: "warm narration",
				Options: map[string]any{"volume": 0, "bit_rate": 48, "rate": 1.2, "pitch": 0.9, "enable_ssml": true,
					"seed": 123, "word_timestamp_enabled": !tc.nonStreaming, "enable_aigc_tag": true,
					"aigc_propagator": "app", "aigc_propagate_id": "id", "hot_fix": map[string]any{"replace": []map[string]string{{"x": "y"}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []tts.EventType{tts.EventSegmentStart, tts.EventAudioFrame, tts.EventAudioFrame, tts.EventSegmentEnd}
			var last *tts.Event
			for _, typ := range want {
				select {
				case event, ok := <-events:
					if !ok || event.Type != typ {
						t.Fatalf("event = %#v, want %s", event, typ)
					}
					if event.RequestID != "request" || event.SegmentID != "request" {
						t.Fatalf("event IDs = %#v", event)
					}
					if typ == tts.EventAudioFrame && (event.Audio.SampleRate != 48000 || event.Audio.Container != audio.ContainerRaw || !bytes.Equal(event.Audio.Data, []byte{0xf8, 0xff, 0xfe})) {
						t.Fatalf("audio = %#v", event.Audio)
					}
					last = event
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if last.Meta["request_id"] != "upstream-id" {
				t.Fatalf("meta = %#v", last.Meta)
			}
			gotUsage := last.Meta["usage"].(map[string]any)
			if tc.model == ModelQwenAudio31TTSFlash && gotUsage["total_tokens"] != float64(11) {
				t.Fatalf("usage = %#v", gotUsage)
			}
			if tc.model == ModelQwenAudio30TTSFlash && gotUsage["characters"] != float64(14) {
				t.Fatalf("usage = %#v", gotUsage)
			}
			if !tc.nonStreaming && len(last.Meta["sentences"].([]map[string]any)) != 1 {
				t.Fatalf("sentence metadata = %#v", last.Meta)
			}
			if _, ok := <-events; ok {
				t.Fatal("unexpected extra event")
			}
			wantDownloads := int32(0)
			if tc.nonStreaming {
				wantDownloads = 1
			}
			if downloads.Load() != wantDownloads {
				t.Fatalf("downloads = %d, want %d", downloads.Load(), wantDownloads)
			}
		})
	}
}

func TestSSEFinalEventWithoutNewline(t *testing.T) {
	ogg := testOgg(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "\ufeffdata: {\"output\":{\"finish_reason\":\"stop\",\"audio\":{\"data\":%q}}}", base64.StdEncoding.EncodeToString(ogg))
	}))
	defer server.Close()
	events := synthesize(t, mustProvider(t, Config{Endpoint: server.URL, DefaultVoice: "voice"}))
	if len(events) != 3 || events[1].Type != tts.ProviderEventAudio || events[2].Type != tts.ProviderEventSegmentEnd {
		t.Fatalf("events = %#v", events)
	}
}

func TestSynthesisErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		code       tts.ErrorCode
	}{
		{"auth", `{"message":"bad key"}`, 401, tts.ErrProviderAuthFailed},
		{"rate limit", `{"message":"busy"}`, 429, tts.ErrProviderRateLimited},
		{"server", `{"message":"busy"}`, 503, tts.ErrProviderUnavailable},
		{"SSE error", "event: error\ndata: {\"code\":\"InvalidParameter\",\"message\":\"bad voice\"}\n\n", 200, tts.ErrProviderUnavailable},
		{"invalid JSON", "data: broken\n\n", 200, tts.ErrProviderUnavailable},
		{"invalid base64", "data: {\"output\":{\"audio\":{\"data\":\"!\"}}}\n\n", 200, tts.ErrAudioDecodeFailed},
		{"truncated stream", "data: {\"output\":{\"type\":\"sentence-end\"}}\n\n", 200, tts.ErrProviderUnavailable},
		{"no audio", "data: {\"output\":{\"finish_reason\":\"stop\"}}\n\n", 200, tts.ErrProviderUnavailable},
		{"unsafe URL", "data: {\"output\":{\"finish_reason\":\"stop\",\"audio\":{\"url\":\"file:///tmp/audio\"}}}\n\n", 200, tts.ErrProviderUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			events := synthesize(t, mustProvider(t, Config{Endpoint: server.URL, DefaultVoice: "voice"}))
			last := events[len(events)-1]
			if last.Type != tts.ProviderEventError || last.Error.Code != tc.code {
				t.Fatalf("last event = %#v, want %s", last, tc.code)
			}
			for _, event := range events {
				if event.Type == tts.ProviderEventSegmentEnd {
					t.Fatal("error synthesis emitted segment_end")
				}
			}
		})
	}
}

func TestOptionsAndLanguage(t *testing.T) {
	p := mustProvider(t, Config{Endpoint: "https://example.test/tts", DefaultVoice: "voice"})
	for _, input := range []string{"eng-US", "en-US", "en"} {
		if got := normalizeLanguage(input); got != "en" {
			t.Errorf("language %s = %s", input, got)
		}
	}
	for _, input := range []string{"", "auto", "automatic"} {
		body, err := p.requestBody(&tts.ProviderSynthesizeRequest{Text: "hello", Language: input})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := body["input"].(map[string]any)["language_hints"]; ok {
			t.Errorf("language %q should omit language_hints", input)
		}
	}
	for _, options := range []map[string]any{
		{"rate": 0.1}, {"volume": 101}, {"volume": nil}, {"seed": 1.5}, {"bit_rate": 511},
		{"enable_ssml": "true"}, {"aigc_propagator": 1}, {"hot_fix": "invalid"},
		{"format": "mp3"}, {"sample_rate": 16000}, {"unknown": true},
	} {
		if _, err := p.SynthesizeOnce(t.Context(), &tts.ProviderSynthesizeRequest{Text: "text", Options: options}); err == nil {
			t.Errorf("options accepted: %#v", options)
		}
	}
	if _, err := p.SynthesizeOnce(t.Context(), nil); err == nil {
		t.Error("nil request accepted")
	}
	if _, err := p.OpenSession(t.Context(), nil); err == nil {
		t.Error("OpenSession accepted")
	}
	p.cfg.NonStreaming = true
	if _, err := p.SynthesizeOnce(t.Context(), &tts.ProviderSynthesizeRequest{Text: "text", Options: map[string]any{"word_timestamp_enabled": true}}); err == nil {
		t.Error("non-streaming word timestamps accepted")
	}
	for _, endpoint := range []string{"", "ws://example.test", "https://user:pass@example.test"} {
		if _, err := NewProvider(Config{Endpoint: endpoint}); err == nil {
			t.Errorf("invalid endpoint accepted: %s", endpoint)
		}
	}
}

func TestCancellationClosesSSEAndDownload(t *testing.T) {
	for _, nonStreaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("nonStreaming=%v", nonStreaming), func(t *testing.T) {
			closed := make(chan struct{})
			ready := make(chan struct{})
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if nonStreaming && r.URL.Path != "/audio" {
					fmt.Fprintf(w, `{"output":{"finish_reason":"stop","audio":{"url":%q}}}`, server.URL+"/audio")
					return
				}
				if nonStreaming {
					fmt.Fprint(w, "OggS")
				} else {
					fmt.Fprint(w, ": ready\n\n")
				}
				w.(http.Flusher).Flush()
				close(ready)
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			p := mustProvider(t, Config{Endpoint: server.URL, DefaultVoice: "voice", NonStreaming: nonStreaming})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			events, err := p.SynthesizeOnce(ctx, &tts.ProviderSynthesizeRequest{Text: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ready:
			case <-time.After(3 * time.Second):
				t.Fatal("request not ready")
			}
			cancel()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP body was not closed")
			}
			for {
				select {
				case _, ok := <-events:
					if !ok {
						return
					}
				case <-time.After(3 * time.Second):
					t.Fatal("event producer did not exit")
				}
			}
		})
	}
}

func mustProvider(t *testing.T, cfg Config) *Provider {
	t.Helper()
	p, err := NewProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testOgg(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	muxer := audio.NewOggOpusMuxer()
	for range 2 {
		if err := muxer.WritePacket(&buf, []byte{0xf8, 0xff, 0xfe}); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func writeSSE(t *testing.T, w io.Writer, response map[string]any) {
	t.Helper()
	data, err := json.Marshal(response)
	if err != nil {
		t.Error(err)
		return
	}
	if _, err := fmt.Fprintf(w, "data: %s\r\n\r\n", data); err != nil {
		t.Error(err)
	}
}

func synthesize(t *testing.T, p *Provider) []*tts.ProviderEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	events, err := p.SynthesizeOnce(ctx, &tts.ProviderSynthesizeRequest{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	var result []*tts.ProviderEvent
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return result
			}
			result = append(result, event)
		case <-ctx.Done():
			t.Fatal("timed out collecting events")
		}
	}
}
