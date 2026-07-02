package fishaudiotts

import (
	"bytes"
	"context"
	"encoding/binary"
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
	provider, err := NewProvider(Config{Name: "fish", DefaultVoice: "voice-id"})
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
	if len(caps.OutputContainers) != 1 || caps.OutputContainers[0] != audio.ContainerOgg {
		t.Fatalf("OutputContainers = %#v, want ogg", caps.OutputContainers)
	}
	if len(caps.Voices) != 1 || caps.Voices[0].ID != "voice-id" {
		t.Fatalf("Voices = %#v, want voice-id", caps.Voices)
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
	server := newFishAudioTestServer(t)
	defer server.close()

	provider, err := NewProvider(Config{
		Name:               "fish",
		Endpoint:           server.url,
		APIKey:             "test-key",
		Model:              "s1",
		DefaultVoice:       "voice-123",
		DefaultLanguage:    "en",
		SegmentIdleTimeout: 20 * time.Millisecond,
		ChunkLength:        200,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := tts.NewService("test-fish", registry)

	session, err := service.OpenSession(context.Background(), &tts.OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "fish",
		Voice:     "voice-123",
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	start := server.nextMessage()
	assertStartMessage(t, start, "voice-123")

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{
		SegmentID: "seg_001",
		Text:      "Hello first. ",
		Voice:     "voice-123",
		Language:  "en",
	}); err != nil {
		t.Fatalf("AppendText first: %v", err)
	}
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{
		SegmentID: "seg_002",
		Text:      "Hello second. ",
		Voice:     "voice-123",
		Language:  "en",
		IsLast:    true,
	}); err != nil {
		t.Fatalf("AppendText second: %v", err)
	}
	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	assertEventMessage(t, server.nextMessage(), "text", "Hello first. ")
	assertEventMessage(t, server.nextMessage(), "flush", "")
	expectServiceEvent(t, events, tts.EventSegmentStart, "seg_001")
	expectServiceAudio(t, events, "seg_001", "opus-packet-1")
	expectServiceEvent(t, events, tts.EventSegmentEnd, "seg_001")

	assertEventMessage(t, server.nextMessage(), "text", "Hello second. ")
	assertEventMessage(t, server.nextMessage(), "flush", "")
	expectServiceEvent(t, events, tts.EventSegmentStart, "seg_002")
	expectServiceAudio(t, events, "seg_002", "opus-packet-2")
	expectServiceEvent(t, events, tts.EventSegmentEnd, "seg_002")

	assertEventMessage(t, server.nextMessage(), "stop", "")
	expectServiceEvent(t, events, tts.EventSessionEnd, "")

	if got := server.authorization; got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want Bearer test-key", got)
	}
	if got := server.model; got != "s1" {
		t.Fatalf("model = %q, want s1", got)
	}
}

func assertStartMessage(t *testing.T, data []byte, voice string) {
	t.Helper()
	msg := mustUnpack(t, data)
	if got := stringValue(msg["event"]); got != "start" {
		t.Fatalf("event = %q, want start", got)
	}
	req, ok := msg["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %T, want map", msg["request"])
	}
	if got := stringValue(req["format"]); got != "opus" {
		t.Fatalf("format = %q, want opus", got)
	}
	if got := intValue(req["sample_rate"]); got != audio.OpusSampleRate {
		t.Fatalf("sample_rate = %d, want %d", got, audio.OpusSampleRate)
	}
	if got := stringValue(req["reference_id"]); got != voice {
		t.Fatalf("reference_id = %q, want %q", got, voice)
	}
	if got := intValue(req["chunk_length"]); got != 200 {
		t.Fatalf("chunk_length = %d, want 200", got)
	}
}

func assertEventMessage(t *testing.T, data []byte, event, text string) {
	t.Helper()
	msg := mustUnpack(t, data)
	if got := stringValue(msg["event"]); got != event {
		t.Fatalf("event = %q, want %q", got, event)
	}
	if text != "" {
		if got := stringValue(msg["text"]); got != text {
			t.Fatalf("text = %q, want %q", got, text)
		}
	}
}

func mustUnpack(t *testing.T, data []byte) map[string]any {
	t.Helper()
	msg, err := unmarshalMsgpackMap(data)
	if err != nil {
		t.Fatalf("unmarshal msgpack: %v", err)
	}
	return msg
}

func intValue(value any) int {
	switch v := value.(type) {
	case int64:
		return int(v)
	case uint64:
		return int(v)
	default:
		return 0
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
		if event.Audio.Container != audio.ContainerRaw {
			t.Fatalf("audio container = %q, want raw", event.Audio.Container)
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

type fishAudioTestServer struct {
	server        *httptest.Server
	url           string
	messages      chan []byte
	errors        chan error
	authorization string
	model         string
	audioSeq      int
}

func newFishAudioTestServer(t *testing.T) *fishAudioTestServer {
	t.Helper()

	testServer := &fishAudioTestServer{
		messages: make(chan []byte, 16),
		errors:   make(chan error, 4),
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testServer.authorization = r.Header.Get("Authorization")
		testServer.model = r.Header.Get("model")

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

			msg, err := unmarshalMsgpackMap(data)
			if err != nil {
				testServer.errors <- err
				return
			}
			switch stringValue(msg["event"]) {
			case "flush":
				testServer.audioSeq++
				payload := []byte(fmt.Sprintf("opus-packet-%d", testServer.audioSeq))
				ogg := makeFishAudioTestOggPage(payload, uint32(testServer.audioSeq-1))
				if err := writeMsgpack(conn, map[string]any{
					"event": "audio",
					"audio": ogg,
				}); err != nil {
					testServer.errors <- err
					return
				}
			case "stop":
				if err := writeMsgpack(conn, map[string]any{
					"event":  "finish",
					"reason": "stop",
				}); err != nil {
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

func (s *fishAudioTestServer) close() {
	s.server.Close()
}

func (s *fishAudioTestServer) nextMessage() []byte {
	select {
	case err := <-s.errors:
		panic(err)
	case msg := <-s.messages:
		return msg
	case <-time.After(3 * time.Second):
		panic("timeout waiting for websocket message")
	}
}

func makeFishAudioTestOggPage(packet []byte, sequence uint32) []byte {
	header := make([]byte, 27)
	copy(header[:4], "OggS")
	binary.LittleEndian.PutUint32(header[18:22], sequence)
	header[26] = 1
	return append(append(header, byte(len(packet))), packet...)
}

func writeMsgpack(conn *websocket.Conn, value map[string]any) error {
	data, err := marshalMsgpack(value)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func TestMsgpackRoundTrip(t *testing.T) {
	input := map[string]any{
		"event": "audio",
		"audio": []byte("packet"),
		"nested": map[string]any{
			"flag": true,
			"num":  int64(7),
		},
	}
	data, err := marshalMsgpack(input)
	if err != nil {
		t.Fatalf("marshalMsgpack: %v", err)
	}
	got, err := unmarshalMsgpackMap(data)
	if err != nil {
		t.Fatalf("unmarshalMsgpackMap: %v", err)
	}
	if stringValue(got["event"]) != "audio" {
		t.Fatalf("event = %q", stringValue(got["event"]))
	}
	if !bytes.Equal(bytesValue(got["audio"]), []byte("packet")) {
		t.Fatalf("audio = %v", bytesValue(got["audio"]))
	}
}
