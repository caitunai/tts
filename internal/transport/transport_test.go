package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
	"github.com/gorilla/websocket"
)

func TestDialHandshakeRetriesOnlyTransientFailures(t *testing.T) {
	for _, tc := range []struct{ status, attempts int }{{503, 2}, {429, 2}, {401, 1}, {403, 1}, {400, 1}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var attempts atomic.Int32
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					http.Error(w, "failed handshake", tc.status)
					return
				}
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
			cfg, _ := Resolve(tts.TimeoutConfig{})
			retry, _ := ResolveReconnect(tts.ReconnectConfig{MaxAttempts: 2, InitialBackoff: time.Millisecond})
			original := *websocket.DefaultDialer
			conn, resp, err := Dial(t.Context(), &original, "ws"+strings.TrimPrefix(server.URL, "http"), nil, cfg, retry)
			if tc.attempts == 2 {
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
			} else {
				if err == nil {
					t.Fatal("permanent handshake error was retried")
				}
				failure := OpenError(err, resp, "test")
				if failure.Retryable || failure.Reconnectable {
					t.Fatalf("permanent failure = %#v", failure)
				}
			}
			if attempts.Load() != int32(tc.attempts) {
				t.Fatalf("attempts = %d, want %d", attempts.Load(), tc.attempts)
			}
			if original.HandshakeTimeout != websocket.DefaultDialer.HandshakeTimeout {
				t.Fatal("caller dialer was mutated")
			}
		})
	}
}

func TestDialBackoffHonorsCancellation(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "busy", 503)
	}))
	defer server.Close()
	cfg, _ := Resolve(tts.TimeoutConfig{})
	retry, _ := ResolveReconnect(tts.ReconnectConfig{InitialBackoff: time.Second})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, _, err := Dial(ctx, nil, "ws"+strings.TrimPrefix(server.URL, "http"), nil, cfg, retry)
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts.Load())
	}
}

func TestSocketDeadlinesBoundReadAndWrite(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "read"
		if write {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			client, server := net.Pipe()
			defer server.Close()
			conn := &deadlineConn{Conn: client, readTimeout: 20 * time.Millisecond, writeTimeout: 20 * time.Millisecond}
			defer conn.Close()
			var err error
			if write {
				_, err = conn.Write([]byte("blocked"))
			} else {
				_, err = conn.Read(make([]byte, 1))
			}
			failure, ok := errors.AsType[net.Error](err)
			if !ok || !failure.Timeout() {
				t.Fatalf("error = %v, want network timeout", err)
			}
		})
	}
}

func TestIdleHTTPBodyClosesOnTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	body := &idleBody{ReadCloser: reader, timeout: 20 * time.Millisecond}
	if _, err := body.Read(make([]byte, 1)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	_ = body.Close()
}

func TestCustomHTTPClientIsNotMutated(t *testing.T) {
	client := resty.New().SetTimeout(5 * time.Second)
	cfg, _ := Resolve(tts.TimeoutConfig{RequestTimeout: time.Second})
	if got := HTTPClient(client, cfg); got != client {
		t.Fatal("supplied client was replaced")
	}
	if client.GetClient().Timeout != 5*time.Second {
		t.Fatal("supplied client timeout was changed")
	}
	ctx, cancel := RequestContext(t.Context(), cfg)
	defer cancel()
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Second {
		t.Fatal("missing per-request deadline")
	}
}

func TestResolveConfig(t *testing.T) {
	if _, err := Resolve(tts.TimeoutConfig{ReadTimeout: -1}); err == nil {
		t.Fatal("negative read timeout accepted")
	}
	if _, err := ResolveReconnect(tts.ReconnectConfig{MaxAttempts: -1}); err == nil {
		t.Fatal("negative attempts accepted")
	}
	if _, err := ResolveReconnect(tts.ReconnectConfig{InitialBackoff: 3 * time.Second, MaxBackoff: time.Second}); err == nil {
		t.Fatal("invalid backoff accepted")
	}
	cfg, err := Resolve(tts.TimeoutConfig{})
	if err != nil || cfg != tts.DefaultTimeoutConfig() {
		t.Fatalf("defaults=%#v error=%v", cfg, err)
	}
}
