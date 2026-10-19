package qwenaudiotts

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

func TestOpenTimeoutIsNotExtendedByServerMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if err := conn.WriteJSON(protocolMessage{Header: protocolHeader{Event: "heartbeat"}}); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	p, err := NewProvider(Config{Endpoint: wsURL(server.URL),
		Timeouts: tts.TimeoutConfig{OpenTimeout: 40 * time.Millisecond, ReadTimeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err = p.OpenSession(ctx, &tts.ProviderOpenSessionRequest{})
	failure, ok := err.(*tts.Error)
	if !ok || failure.Code != tts.ErrProviderTimeout || ctx.Err() != nil {
		t.Fatalf("OpenSession error = %#v", err)
	}
}

func TestApplicationReopensSessionWithoutReplayingText(t *testing.T) {
	ogg := oggChunk(t, []byte{0xf8, 0xff, 0xfe})
	var connections atomic.Int32
	texts := make(chan string, 4)
	tasks := make(chan string, 4)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		attempt := connections.Add(1)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			var msg protocolMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			switch msg.Header.Action {
			case "run-task":
				tasks <- msg.Header.TaskID
				writeProtocol(t, conn, "task-started", "")
			case "continue-task":
				text, _ := msg.Payload.Input["text"].(string)
				texts <- text
				writeProtocol(t, conn, "result-generated", "sentence-begin", text)
				writeProtocol(t, conn, "result-generated", "sentence-synthesis")
				if err := conn.WriteMessage(websocket.BinaryMessage, ogg); err != nil {
					return
				}
				if attempt == 1 {
					return
				} // Disconnect after audio, before completion.
				writeProtocol(t, conn, "result-generated", "sentence-end", text)
			case "finish-task":
				writeProtocol(t, conn, "task-finished", "")
				return
			}
		}
	}))
	defer server.Close()
	p, err := NewProvider(Config{Endpoint: wsURL(server.URL)})
	if err != nil {
		t.Fatal(err)
	}
	registry := registryprovider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	service := tts.NewService("test", registry)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	first, err := service.OpenSession(ctx, &tts.OpenSessionRequest{Provider: p.Name(), SessionID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	events := first.Events()
	if err := first.AppendText(ctx, &tts.SegmentRequest{SegmentID: "old", Text: "old text"}); err != nil {
		t.Fatal(err)
	}
	var failure *tts.Error
	for event := range events {
		if event.Type == tts.EventError {
			failure = event.Error
		}
	}
	if failure == nil || !failure.Reconnectable || !failure.PartialAudio || failure.Retryable {
		t.Fatalf("failure = %#v", failure)
	}
	_ = first.Close()
	second, err := service.OpenSession(ctx, &tts.OpenSessionRequest{Provider: p.Name(), SessionID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	events = second.Events()
	if err := second.AppendText(ctx, &tts.SegmentRequest{SegmentID: "new", Text: "new text", IsLast: true}); err != nil {
		t.Fatal(err)
	}
	ended := false
	for event := range events {
		if event.Type == tts.EventError {
			t.Fatal(event.Error)
		}
		if event.Type == tts.EventSessionEnd {
			ended = true
		}
	}
	if !ended || connections.Load() != 2 {
		t.Fatalf("ended=%v connections=%d", ended, connections.Load())
	}
	for _, want := range []string{"old text", "new text"} {
		select {
		case got := <-texts:
			if got != want {
				t.Fatalf("text=%q, want %q", got, want)
			}
		default:
			t.Fatalf("missing %q", want)
		}
	}
	if len(texts) != 0 {
		t.Fatal("previously sent text was replayed")
	}
	if firstID, secondID := <-tasks, <-tasks; firstID == secondID {
		t.Fatal("new session reused a failed provider task")
	}
}

func TestServiceSendsBufferedTextBeforeSegmentEnd(t *testing.T) {
	for _, isLast := range []bool{false, true} {
		name := "explicit Finish"
		if isLast {
			name = "IsLast"
		}
		t.Run(name, func(t *testing.T) {
			ogg := oggChunk(t, []byte{0xf8, 0xff, 0xfe})
			var buf bytes.Buffer
			muxer := audio.NewOggOpusMuxer()
			if err := muxer.WritePacket(&buf, []byte{0xf8, 0xff, 0xfe}); err != nil {
				t.Fatal(err)
			}
			second := bytes.Clone(buf.Bytes())
			buf.Reset()
			if err := muxer.WritePacket(&buf, []byte{0xf8, 0xff, 0xfe}); err != nil {
				t.Fatal(err)
			}
			tail := bytes.Clone(buf.Bytes())
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("Upgrade: %v", err)
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var inputs []string
				for _, action := range []string{"run-task", "continue-task", "continue-task", "finish-task"} {
					var msg protocolMessage
					if err := conn.ReadJSON(&msg); err != nil {
						t.Errorf("ReadJSON waiting for %s: %v", action, err)
						return
					}
					if msg.Header.Action != action {
						t.Errorf("action = %s, want %s", msg.Header.Action, action)
						return
					}
					if action == "run-task" {
						writeProtocol(t, conn, "task-started", "")
					}
					if action == "continue-task" {
						text, _ := msg.Payload.Input["text"].(string)
						inputs = append(inputs, text)
					}
				}
				// The provider deliberately buffers all audio until finish-task.
				for i, text := range inputs {
					chunk := ogg
					if i == 1 {
						chunk = second
					}
					writeProtocol(t, conn, "result-generated", "sentence-begin", text)
					writeProtocol(t, conn, "result-generated", "sentence-synthesis")
					if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
						t.Errorf("WriteMessage: %v", err)
						return
					}
					writeProtocol(t, conn, "result-generated", "sentence-end", text)
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, tail); err != nil {
					t.Errorf("WriteMessage trailing audio: %v", err)
					return
				}
				writeProtocol(t, conn, "task-finished", "")
			}))
			defer server.Close()
			provider, err := NewProvider(Config{Endpoint: wsURL(server.URL)})
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			registry := registryprovider.NewRegistry()
			if err := registry.Register(provider); err != nil {
				t.Fatalf("Register: %v", err)
			}
			service := tts.NewService("test", registry)
			s, err := service.OpenSession(t.Context(), &tts.OpenSessionRequest{Provider: provider.Name()})
			if err != nil {
				t.Fatalf("OpenSession: %v", err)
			}
			defer s.Close()
			events := s.Events()
			for i, text := range []string{"First.", "Second."} {
				id := []string{"one", "two"}[i]
				if err := s.AppendText(t.Context(), &tts.SegmentRequest{
					SegmentID: id, Text: text, IsLast: i == 1 && isLast,
				}); err != nil {
					t.Fatalf("AppendText %s: %v", id, err)
				}
			}
			if !isLast {
				if err := s.Finish(t.Context()); err != nil {
					t.Fatalf("Finish: %v", err)
				}
			}
			want := []string{
				"session_start:",
				"segment_start:one", "audio_frame:one", "segment_end:one",
				"segment_start:two", "audio_frame:two", "audio_frame:two", "segment_end:two",
				"session_end:",
			}
			for _, want := range want {
				select {
				case event, ok := <-events:
					if !ok {
						t.Fatalf("events closed, want %s", want)
					}
					if got := string(event.Type) + ":" + event.SegmentID; got != want {
						t.Fatalf("event = %s (%v), want %s", got, event.Error, want)
					}
				case <-time.After(3 * time.Second):
					t.Fatalf("timed out waiting for %s", want)
				}
			}
			if event, ok := <-events; ok {
				t.Fatalf("unexpected event after session_end: %#v", event)
			}
		})
	}
}
