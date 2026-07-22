package inworldtts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

func TestProviderCapabilities(t *testing.T) {
	provider, err := NewProvider(Config{
		Name:            "inworld",
		DefaultVoice:    "Dennis",
		DefaultLanguage: "en-US",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.SupportsAppendText {
		t.Fatal("SupportsAppendText = false, want true")
	}
	if !caps.SupportsOggOpusOutput {
		t.Fatal("SupportsOggOpusOutput = false, want true")
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != audio.OpusSampleRate {
		t.Fatalf("OutputSampleRates = %#v, want %d", caps.OutputSampleRates, audio.OpusSampleRate)
	}
	if len(caps.Voices) != 0 {
		t.Fatalf("Voices = %#v, want no platform voice restriction", caps.Voices)
	}
	if len(caps.Languages) != 0 {
		t.Fatalf("Languages = %#v, want no platform language restriction", caps.Languages)
	}
}

func TestProviderRejectsSynthesizeOnce(t *testing.T) {
	provider, err := NewProvider(Config{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = provider.SynthesizeOnce(context.Background(), &tts.ProviderSynthesizeRequest{Text: "hello"})
	if err == nil {
		t.Fatal("SynthesizeOnce returned nil error, want unsupported feature")
	}
	ttsErr, ok := err.(*tts.Error)
	if !ok {
		t.Fatalf("error type = %T, want *tts.Error", err)
	}
	if ttsErr.Code != tts.ErrUnsupportedFeature {
		t.Fatalf("error code = %q, want unsupported feature", ttsErr.Code)
	}
}

func TestServiceSessionAppendsTwoSegments(t *testing.T) {
	server := newInworldTestServer(t)
	defer server.close()

	provider, err := NewProvider(Config{
		Name:            "inworld",
		Endpoint:        server.url,
		APIKey:          "test-api-key",
		Model:           "inworld-tts-2",
		DefaultVoice:    "Dennis",
		DefaultLanguage: "en-US",
		AutoMode:        true,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := tts.NewService("test-inworld", registry)

	session, err := service.OpenSession(context.Background(), &tts.OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "inworld",
		Voice:     "Dennis",
		Language:  "en-US",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	createMsg := server.nextMessage()
	var create createContextMessage
	if err := json.Unmarshal(createMsg, &create); err != nil {
		t.Fatalf("decode create context: %v", err)
	}
	if create.ContextID != "sess_001" {
		t.Fatalf("context id = %q, want sess_001", create.ContextID)
	}
	if create.Create.VoiceID != "Dennis" {
		t.Fatalf("voice id = %q, want Dennis", create.Create.VoiceID)
	}
	if create.Create.ModelID != "inworld-tts-2" {
		t.Fatalf("model id = %q, want inworld-tts-2", create.Create.ModelID)
	}
	if create.Create.AudioConfig.AudioEncoding != defaultAudioEncoding {
		t.Fatalf("audio encoding = %q, want %s", create.Create.AudioConfig.AudioEncoding, defaultAudioEncoding)
	}
	if create.Create.AudioConfig.SampleRateHertz != audio.OpusSampleRate {
		t.Fatalf("sample rate = %d, want %d", create.Create.AudioConfig.SampleRateHertz, audio.OpusSampleRate)
	}
	if !create.Create.AutoMode {
		t.Fatal("auto mode = false, want true")
	}

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{
		SegmentID: "seg_001",
		Text:      "Hello first",
		Voice:     "Dennis",
		Language:  "en-US",
	}); err != nil {
		t.Fatalf("AppendText first: %v", err)
	}
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{
		SegmentID: "seg_002",
		Text:      "Hello second",
		Voice:     "Dennis",
		Language:  "en-US",
		IsLast:    true,
	}); err != nil {
		t.Fatalf("AppendText second: %v", err)
	}
	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	firstText := server.nextMessage()
	assertSendText(t, firstText, "sess_001", "Hello first")
	expectServiceEvent(t, events, tts.EventSegmentStart, "seg_001")
	expectServiceAudio(t, events, "seg_001", "opus-packet-1")
	expectServiceEvent(t, events, tts.EventSegmentEnd, "seg_001")

	secondText := server.nextMessage()
	assertSendText(t, secondText, "sess_001", "Hello second")
	expectServiceEvent(t, events, tts.EventSegmentStart, "seg_002")
	expectServiceAudio(t, events, "seg_002", "opus-packet-2")
	expectServiceEvent(t, events, tts.EventSegmentEnd, "seg_002")

	closeContext := server.nextMessage()
	var closeMsg closeContextMessage
	if err := json.Unmarshal(closeContext, &closeMsg); err != nil {
		t.Fatalf("decode close context: %v", err)
	}
	if closeMsg.ContextID != "sess_001" {
		t.Fatalf("close context id = %q, want sess_001", closeMsg.ContextID)
	}
	expectServiceEvent(t, events, tts.EventSessionEnd, "")

	if got := server.authorization; got != "Basic test-api-key" {
		t.Fatalf("authorization = %q, want Basic test-api-key", got)
	}
}

func assertSendText(t *testing.T, data []byte, contextID, text string) {
	t.Helper()
	var msg sendTextMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode send text: %v", err)
	}
	if msg.ContextID != contextID {
		t.Fatalf("context id = %q, want %q", msg.ContextID, contextID)
	}
	if msg.SendText.Text != text {
		t.Fatalf("text = %q, want %q", msg.SendText.Text, text)
	}
	if msg.SendText.FlushContext == nil {
		t.Fatal("flush_context is nil")
	}
}

func expectServiceEvent(t *testing.T, events <-chan *tts.Event, eventType tts.EventType, segmentID string) {
	t.Helper()
	for i := 0; i < 16; i++ {
		event := nextServiceEvent(t, events)
		if event.Type == tts.EventError {
			t.Fatalf("unexpected error event: %#v", event.Error)
		}
		if event.Type != eventType {
			continue
		}
		if segmentID != "" && event.SegmentID != segmentID {
			continue
		}
		return
	}
	t.Fatalf("did not receive event type=%q segment=%q", eventType, segmentID)
}

func expectServiceAudio(t *testing.T, events <-chan *tts.Event, segmentID, payload string) {
	t.Helper()
	for i := 0; i < 16; i++ {
		event := nextServiceEvent(t, events)
		if event.Type == tts.EventError {
			t.Fatalf("unexpected error event: %#v", event.Error)
		}
		if event.Type != tts.EventAudioFrame || event.SegmentID != segmentID {
			continue
		}
		if event.Audio == nil {
			t.Fatal("audio frame is nil")
		}
		if event.Audio.Codec != audio.CodecOpus {
			t.Fatalf("audio codec = %q, want opus", event.Audio.Codec)
		}
		if event.Audio.SampleRate != audio.OpusSampleRate {
			t.Fatalf("audio sample rate = %d, want %d", event.Audio.SampleRate, audio.OpusSampleRate)
		}
		if string(event.Audio.Data) != payload {
			t.Fatalf("audio data = %q, want %q", string(event.Audio.Data), payload)
		}
		return
	}
	t.Fatalf("did not receive audio segment=%q payload=%q", segmentID, payload)
}

func nextServiceEvent(t *testing.T, events <-chan *tts.Event) *tts.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("events channel closed")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for service event")
	}
	return nil
}

type inworldTestServer struct {
	server        *httptest.Server
	url           string
	messages      chan []byte
	errors        chan error
	authorization string
	audioSeq      uint32
}

func newInworldTestServer(t *testing.T) *inworldTestServer {
	t.Helper()

	testServer := &inworldTestServer{
		messages: make(chan []byte, 16),
		errors:   make(chan error, 4),
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testServer.authorization = r.URL.Query().Get("authorization")

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			testServer.errors <- err
			return
		}
		defer func() {
			_ = conn.Close()
		}()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			testServer.messages <- append([]byte(nil), data...)

			switch {
			case bytes.Contains(data, []byte(`"create"`)):
				if err := conn.WriteJSON(responseMessage{Result: &responseResult{
					ContextID:      "sess_001",
					ContextCreated: &emptyObject{},
					Status:         &statusResult{},
				}}); err != nil {
					testServer.errors <- err
					return
				}
			case bytes.Contains(data, []byte(`"send_text"`)):
				testServer.audioSeq++
				packet := fmt.Sprintf("opus-packet-%d", testServer.audioSeq)
				ogg := makeTestOggPage([]byte(packet), testServer.audioSeq-1)
				if err := conn.WriteJSON(responseMessage{Result: &responseResult{
					ContextID: "sess_001",
					AudioChunk: &audioChunkResult{
						AudioContent: base64.StdEncoding.EncodeToString(ogg),
						Usage:        map[string]any{"modelId": "inworld-tts-2"},
					},
					Status: &statusResult{},
				}}); err != nil {
					testServer.errors <- err
					return
				}
				if err := conn.WriteJSON(responseMessage{Result: &responseResult{
					ContextID:      "sess_001",
					FlushCompleted: &emptyObject{},
					Status:         &statusResult{},
				}}); err != nil {
					testServer.errors <- err
					return
				}
			case bytes.Contains(data, []byte(`"close_context"`)):
				if err := conn.WriteJSON(responseMessage{Result: &responseResult{
					ContextID:     "sess_001",
					ContextClosed: &emptyObject{},
					Status:        &statusResult{},
				}}); err != nil {
					testServer.errors <- err
				}
				return
			}
		}
	}))
	testServer.server = server
	testServer.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return testServer
}

func (s *inworldTestServer) close() {
	s.server.Close()
}

func (s *inworldTestServer) nextMessage() []byte {
	select {
	case err := <-s.errors:
		panic(err)
	case msg := <-s.messages:
		return msg
	case <-time.After(3 * time.Second):
		panic("timeout waiting for websocket message")
	}
}

func makeTestOggPage(packet []byte, sequence uint32) []byte {
	header := make([]byte, 27)
	copy(header[:4], "OggS")
	binary.LittleEndian.PutUint32(header[18:22], sequence)
	header[26] = 1
	return append(append(header, byte(len(packet))), packet...)
}
