package provider_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tts "github.com/caitunai/tts"
	"github.com/caitunai/tts/providers/cartesia"
	"github.com/caitunai/tts/providers/deepgram"
	"github.com/caitunai/tts/providers/doubao"
	"github.com/caitunai/tts/providers/elevenlabs"
	"github.com/caitunai/tts/providers/fishaudio"
	"github.com/caitunai/tts/providers/gemini"
	"github.com/caitunai/tts/providers/inworld"
	"github.com/caitunai/tts/providers/microsoft"
	"github.com/caitunai/tts/providers/minimax"
	"github.com/caitunai/tts/providers/openai"
	"github.com/caitunai/tts/providers/qwenaudio"
	"github.com/caitunai/tts/providers/qwenaudiohttp"
	"github.com/caitunai/tts/providers/qwenhttp"
	"github.com/caitunai/tts/providers/qwenrealtime"
	"github.com/caitunai/tts/providers/vllm"
	"github.com/go-resty/resty/v2"
	"github.com/gorilla/websocket"
)

type timeoutFactory struct {
	name string
	new  func(string, tts.TimeoutConfig) (tts.Provider, error)
}

func TestHTTPHeartbeatDoesNotExtendRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()
	client := resty.New()
	p, err := qwenhttp.NewProvider(qwenhttp.Config{Endpoint: server.URL, Client: client,
		Timeouts: tts.TimeoutConfig{RequestTimeout: 80 * time.Millisecond, ReadTimeout: 40 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	events, err := p.SynthesizeOnce(ctx, &tts.ProviderSynthesizeRequest{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	var failure *tts.Error
	for event := range events {
		if event.Type == tts.ProviderEventError {
			failure = event.Error
		}
	}
	if failure == nil || failure.Code != tts.ErrProviderTimeout {
		t.Fatalf("failure = %#v", failure)
	}
	if ctx.Err() != nil {
		t.Fatal("request relied on caller deadline")
	}
	if client.GetClient().Timeout != 0 {
		t.Fatal("custom client was mutated")
	}
}

func TestHTTPAudioDownloadHasTimeout(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path != "/audio" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"output":{"finish_reason":"stop","audio":{"url":%q}}}`, server.URL+"/audio")
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	p, err := qwenaudiohttp.NewProvider(qwenaudiohttp.Config{Endpoint: server.URL, NonStreaming: true, DefaultVoice: "voice",
		Timeouts: tts.TimeoutConfig{RequestTimeout: 200 * time.Millisecond, ReadTimeout: 40 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	events, err := p.SynthesizeOnce(ctx, &tts.ProviderSynthesizeRequest{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	var failure *tts.Error
	for event := range events {
		if event.Type == tts.ProviderEventError {
			failure = event.Error
		}
	}
	if failure == nil || failure.Code != tts.ErrProviderTimeout {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestAllHTTPProvidersBoundStalledRequests(t *testing.T) {
	factories := []timeoutFactory{
		{"vllm", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return vllm.NewProvider(vllm.Config{Endpoint: url, Timeouts: cfg})
		}},
		{"qwenhttp", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return qwenhttp.NewProvider(qwenhttp.Config{Endpoint: url, Timeouts: cfg})
		}},
		{"qwenaudiohttp", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return qwenaudiohttp.NewProvider(qwenaudiohttp.Config{Endpoint: url, DefaultVoice: "voice", Timeouts: cfg})
		}},
		{"microsoft", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return microsoft.NewProvider(microsoft.Config{Endpoint: url, Timeouts: cfg})
		}},
		{"deepgram", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return deepgram.NewProvider(deepgram.Config{Endpoint: url, Timeouts: cfg})
		}},
		{"openai", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return openai.NewProvider(openai.Config{Endpoint: url, Timeouts: cfg})
		}},
		{"gemini", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return gemini.NewProvider(gemini.Config{Endpoint: url, Timeouts: cfg})
		}},
	}
	for _, factory := range factories {
		for _, headers := range []bool{false, true} {
			phase := "response headers"
			if headers {
				phase = "response body"
			}
			t.Run(factory.name+"/"+phase, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if headers {
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
				}))
				defer server.Close()
				p, err := factory.new(server.URL, tts.TimeoutConfig{RequestTimeout: 150 * time.Millisecond, ReadTimeout: 40 * time.Millisecond})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				events, err := p.SynthesizeOnce(ctx, &tts.ProviderSynthesizeRequest{RequestID: "stalled", Text: "hello", Voice: "voice"})
				if err != nil {
					t.Fatal(err)
				}
				var failure *tts.Error
				for {
					select {
					case event, ok := <-events:
						if !ok {
							if failure == nil || failure.Code != tts.ErrProviderTimeout {
								t.Fatalf("failure = %#v, want provider_timeout", failure)
							}
							return
						}
						if event.Type == tts.ProviderEventError {
							failure = event.Error
						}
						if event.Type == tts.ProviderEventSegmentEnd {
							t.Fatal("stalled request reported successful completion")
						}
					case <-ctx.Done():
						t.Fatal("provider ignored configured timeout")
					}
				}
			})
		}
	}
}

func TestAllWebSocketProvidersBoundUnresponsiveServer(t *testing.T) {
	retry := tts.ReconnectConfig{MaxAttempts: 1}
	factories := []timeoutFactory{
		{"cartesia", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return cartesia.NewProvider(cartesia.Config{Endpoint: url, DefaultVoice: "voice", Timeouts: cfg, Reconnect: retry})
		}},
		{"doubao", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return doubao.NewProvider(doubao.Config{Endpoint: url, DefaultVoice: "voice", Timeouts: cfg, Reconnect: retry})
		}},
		{"elevenlabs", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return elevenlabs.NewProvider(elevenlabs.Config{Endpoint: url, DefaultVoice: "voice", Timeouts: cfg, Reconnect: retry})
		}},
		{"fishaudio", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return fishaudio.NewProvider(fishaudio.Config{Endpoint: url, DefaultVoice: "voice", Timeouts: cfg, Reconnect: retry})
		}},
		{"inworld", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return inworld.NewProvider(inworld.Config{Endpoint: url, DefaultVoice: "voice", Timeouts: cfg, Reconnect: retry})
		}},
		{"minimax", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return minimax.NewProvider(minimax.Config{Endpoint: url, DefaultVoice: "voice", Timeouts: cfg, Reconnect: retry})
		}},
		{"qwenaudio", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return qwenaudio.NewProvider(qwenaudio.Config{Endpoint: url, DefaultVoice: "voice", Timeouts: cfg, Reconnect: retry})
		}},
		{"qwenrealtime", func(url string, cfg tts.TimeoutConfig) (tts.Provider, error) {
			return qwenrealtime.NewProvider(qwenrealtime.Config{Endpoint: url, Timeouts: cfg, Reconnect: retry})
		}},
	}
	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						return
					}
				}
			}))
			defer server.Close()
			p, err := factory.new("ws"+strings.TrimPrefix(server.URL, "http"), tts.TimeoutConfig{ReadTimeout: 40 * time.Millisecond, OpenTimeout: 80 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			session, err := p.OpenSession(ctx, &tts.ProviderOpenSessionRequest{SessionID: "stalled", Voice: "voice"})
			if err != nil {
				failure, ok := errors.AsType[*tts.Error](err)
				if !ok || failure.Code != tts.ErrProviderTimeout || !failure.Reconnectable {
					t.Fatalf("OpenSession error = %#v", err)
				}
				return
			}
			defer session.Close()
			for {
				select {
				case event, ok := <-session.Events():
					if !ok {
						t.Fatal("session ended without timeout")
					}
					if event.Type != tts.ProviderEventError {
						continue
					}
					if event.Error.Code != tts.ErrProviderTimeout || !event.Error.Reconnectable {
						t.Fatalf("failure = %#v", event.Error)
					}
					return
				case <-ctx.Done():
					t.Fatal("websocket ignored configured read timeout")
				}
			}
		})
	}
}
