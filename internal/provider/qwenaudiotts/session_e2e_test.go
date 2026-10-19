package qwenaudiotts

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

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
