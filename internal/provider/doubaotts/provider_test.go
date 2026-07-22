package doubaotts

import (
	"bytes"
	"context"
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
	platformtts "github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

func TestProviderCapabilities(t *testing.T) {
	provider, err := NewProvider(Config{
		Name:         "doubao",
		DefaultVoice: "voice-001",
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
	if !caps.SupportsGuidanceText {
		t.Fatal("SupportsGuidanceText = false, want true")
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
}

func TestProviderRejectsSynthesizeOnce(t *testing.T) {
	provider, err := NewProvider(Config{Name: "doubao"})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = provider.SynthesizeOnce(context.Background(), &platformtts.ProviderSynthesizeRequest{Text: "hello"})
	if err == nil {
		t.Fatal("SynthesizeOnce returned nil error, want unsupported feature")
	}
	ttsErr, ok := err.(*platformtts.Error)
	if !ok {
		t.Fatalf("error type = %T, want *tts.Error", err)
	}
	if ttsErr.Code != platformtts.ErrUnsupportedFeature {
		t.Fatalf("error code = %q, want unsupported feature", ttsErr.Code)
	}
}

func TestServiceSessionAppendsTwoSegments(t *testing.T) {
	server := newDoubaoTestServer(t)
	defer server.close()

	provider, err := NewProvider(Config{
		Name:               "doubao",
		Endpoint:           server.url,
		APIKey:             "test-api-key",
		ResourceID:         "seed-tts-2.0",
		DefaultVoice:       "voice-default",
		DefaultLanguage:    "zh",
		DefaultSectionID:   "section-001",
		SegmentIdleTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := platformtts.NewService("test-doubao", registry)

	session, err := service.OpenSession(context.Background(), &platformtts.OpenSessionRequest{
		SessionID:    "sess_001",
		Provider:     "doubao",
		Voice:        "voice-default",
		Language:     "zh",
		GuidanceText: "温暖自然地说话",
		Output: audio.OutputConfig{
			PreferCodec:        audio.CodecOpus,
			SampleRate:         audio.OpusSampleRate,
			Channels:           audio.DefaultChannels,
			FrameMS:            audio.DefaultFrameMS,
			AllowOggOpusDemux:  true,
			AllowRawOpusOutput: true,
		},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	startReq := server.nextRequest()
	if startReq.event != EventtypeStartconnection {
		t.Fatalf("first event = %v, want StartConnection", startReq.event)
	}
	startSessionReq := server.nextRequest()
	if startSessionReq.event != EventTypeStartSession {
		t.Fatalf("second event = %v, want StartSession", startSessionReq.event)
	}
	if startSessionReq.sessionID != "sess_001" {
		t.Fatalf("start session id = %q, want sess_001", startSessionReq.sessionID)
	}
	if startSessionReq.payload.ReqParams.Speaker != "voice-default" {
		t.Fatalf("speaker = %q, want voice-default", startSessionReq.payload.ReqParams.Speaker)
	}
	additions := decodeAdditions(t, startSessionReq.payload.ReqParams.Additions)
	if got := additions["context_texts"]; fmt.Sprint(got) != "[温暖自然地说话]" {
		t.Fatalf("context_texts = %#v, want guidance text", got)
	}
	if got := additions["section_id"]; got != "section-001" {
		t.Fatalf("section_id = %#v, want section-001", got)
	}

	events := session.Events()
	if err := session.AppendText(context.Background(), &platformtts.SegmentRequest{
		SegmentID: "seg_001",
		Text:      "第一段",
		Voice:     "voice-default",
		Language:  "zh",
	}); err != nil {
		t.Fatalf("AppendText first: %v", err)
	}
	if err := session.AppendText(context.Background(), &platformtts.SegmentRequest{
		SegmentID: "seg_002",
		Text:      "第二段",
		Voice:     "voice-default",
		Language:  "zh",
		IsLast:    true,
	}); err != nil {
		t.Fatalf("AppendText second: %v", err)
	}
	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	firstTask := server.nextRequest()
	if firstTask.event != EventTypeTaskRequest {
		t.Fatalf("third event = %v, want TaskRequest", firstTask.event)
	}
	if firstTask.payload.ReqParams.Text != "第一段" {
		t.Fatalf("first text = %q, want 第一段", firstTask.payload.ReqParams.Text)
	}
	expectServiceEvent(t, events, platformtts.EventSegmentStart, "seg_001")
	expectServiceAudio(t, events, "seg_001", "opus-packet-1-a")
	expectServiceAudio(t, events, "seg_001", "opus-packet-1-b")
	expectServiceEvent(t, events, platformtts.EventSegmentEnd, "seg_001")

	secondTask := server.nextRequest()
	if secondTask.event != EventTypeTaskRequest {
		t.Fatalf("fourth event = %v, want TaskRequest", secondTask.event)
	}
	if secondTask.payload.ReqParams.Text != "第二段" {
		t.Fatalf("second text = %q, want 第二段", secondTask.payload.ReqParams.Text)
	}
	expectServiceEvent(t, events, platformtts.EventSegmentStart, "seg_002")
	expectServiceAudio(t, events, "seg_002", "opus-packet-2-a")
	expectServiceAudio(t, events, "seg_002", "opus-packet-2-b")
	expectServiceEvent(t, events, platformtts.EventSegmentEnd, "seg_002")

	finishReq := server.nextRequest()
	if finishReq.event != EventTypeFinishSession {
		t.Fatalf("finish event = %v, want FinishSession", finishReq.event)
	}
	expectServiceEvent(t, events, platformtts.EventSessionEnd, "")

	finishConnReq := server.nextRequest()
	if finishConnReq.event != EventtypeFinishconnection {
		t.Fatalf("finish connection event = %v, want FinishConnection", finishConnReq.event)
	}

	if got := server.header.Get("X-Api-Key"); got != "test-api-key" {
		t.Fatalf("X-Api-Key = %q, want test-api-key", got)
	}
	if got := server.header.Get("X-Api-Resource-Id"); got != "seed-tts-2.0" {
		t.Fatalf("X-Api-Resource-Id = %q, want seed-tts-2.0", got)
	}
}

func TestNormalizeDoubaoLanguage(t *testing.T) {
	tests := map[string]string{
		"zh":      "zh-cn",
		"zh-cn":   "zh-cn",
		"en":      "en",
		"ja":      "ja",
		"ko":      "ko",
		"es":      "es-mx",
		"id":      "id",
		"pt":      "pt-br",
		"unknown": "",
	}

	for input, want := range tests {
		if got := normalizeDoubaoLanguage(input); got != want {
			t.Fatalf("normalizeDoubaoLanguage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResourceIDForVoice(t *testing.T) {
	provider, err := NewProvider(Config{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if got := provider.resourceIDForVoice("zh_female"); got != defaultResourceID {
		t.Fatalf("seed voice resource = %q, want %s", got, defaultResourceID)
	}
	if got := provider.resourceIDForVoice("S_clone"); got != defaultCloneResourceID {
		t.Fatalf("clone voice resource = %q, want %s", got, defaultCloneResourceID)
	}
}

func expectServiceEvent(t *testing.T, events <-chan *platformtts.Event, eventType platformtts.EventType, segmentID string) {
	t.Helper()
	for i := 0; i < 16; i++ {
		event := nextServiceEvent(t, events)
		if event.Type == platformtts.EventError {
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

func expectServiceAudio(t *testing.T, events <-chan *platformtts.Event, segmentID, payload string) {
	t.Helper()
	for i := 0; i < 16; i++ {
		event := nextServiceEvent(t, events)
		if event.Type == platformtts.EventError {
			t.Fatalf("unexpected error event: %#v", event.Error)
		}
		if event.Type != platformtts.EventAudioFrame || event.SegmentID != segmentID {
			continue
		}
		if event.Audio == nil {
			t.Fatal("audio frame is nil")
		}
		if event.Audio.Codec != audio.CodecOpus {
			t.Fatalf("audio codec = %q, want opus", event.Audio.Codec)
		}
		if string(event.Audio.Data) != payload {
			t.Fatalf("audio data = %q, want %q", string(event.Audio.Data), payload)
		}
		return
	}
	t.Fatalf("did not receive audio segment=%q payload=%q", segmentID, payload)
}

func nextServiceEvent(t *testing.T, events <-chan *platformtts.Event) *platformtts.Event {
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

func decodeAdditions(t *testing.T, additions string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(additions), &got); err != nil {
		t.Fatalf("decode additions: %v", err)
	}
	return got
}

type doubaoTestServer struct {
	server   *httptest.Server
	url      string
	header   http.Header
	requests chan doubaoTestRequest
	errors   chan error
	taskSeq  int
}

type doubaoTestRequest struct {
	event     EventType
	sessionID string
	payload   taskRequestPayload
}

func newDoubaoTestServer(t *testing.T) *doubaoTestServer {
	t.Helper()

	testServer := &doubaoTestServer{
		requests: make(chan doubaoTestRequest, 16),
		errors:   make(chan error, 4),
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testServer.header = r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			testServer.errors <- err
			return
		}
		defer func() {
			_ = conn.Close()
		}()

		for {
			msg, err := ReceiveMessage(conn)
			if err != nil {
				return
			}
			req := doubaoTestRequest{event: msg.EventType, sessionID: msg.SessionID}
			if len(msg.Payload) > 0 && !bytes.Equal(msg.Payload, []byte("{}")) {
				if err := json.Unmarshal(msg.Payload, &req.payload); err != nil {
					testServer.errors <- err
					return
				}
			}
			testServer.requests <- req

			switch msg.EventType {
			case EventtypeStartconnection:
				if err := writeServerEvent(conn, EventTypeConnectionStarted, "", nil); err != nil {
					testServer.errors <- err
					return
				}
			case EventTypeStartSession:
				if err := writeServerEvent(conn, EventTypeSessionStarted, msg.SessionID, nil); err != nil {
					testServer.errors <- err
					return
				}
			case EventTypeTaskRequest:
				testServer.taskSeq++
				firstPacket := fmt.Sprintf("opus-packet-%d-a", testServer.taskSeq)
				if err := writeServerAudio(conn, MsgTypeFlagNoSeq, 0, makeTestOggPage([]byte(firstPacket), 0)); err != nil {
					testServer.errors <- err
					return
				}
				if err := writeServerEvent(conn, EventTypeTTSSentenceEnd, msg.SessionID, nil); err != nil {
					testServer.errors <- err
					return
				}
				time.Sleep(20 * time.Millisecond)
				secondPacket := fmt.Sprintf("opus-packet-%d-b", testServer.taskSeq)
				if err := writeServerAudio(conn, MsgTypeFlagNoSeq, 1, makeTestOggPage([]byte(secondPacket), 1)); err != nil {
					testServer.errors <- err
					return
				}
			case EventTypeFinishSession:
				if err := writeServerEvent(conn, EventTypeSessionFinished, msg.SessionID, nil); err != nil {
					testServer.errors <- err
					return
				}
			case EventtypeFinishconnection:
				if err := writeServerEvent(conn, EventTypeConnectionFinished, "", nil); err != nil {
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

func (s *doubaoTestServer) close() {
	s.server.Close()
}

func (s *doubaoTestServer) nextRequest() doubaoTestRequest {
	select {
	case err := <-s.errors:
		panic(err)
	case req := <-s.requests:
		return req
	case <-time.After(3 * time.Second):
		panic("timeout waiting for doubao request")
	}
}

func writeServerEvent(conn *websocket.Conn, event EventType, sessionID string, payload []byte) error {
	msg, err := NewMessage(MsgTypeFullServerResponse, MsgTypeFlagWithEvent)
	if err != nil {
		return err
	}
	msg.EventType = event
	msg.SessionID = sessionID
	if payload == nil {
		payload = []byte("{}")
	}
	msg.Payload = payload
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

func writeServerAudio(conn *websocket.Conn, flag MsgTypeFlagBits, sequence int32, payload []byte) error {
	msg, err := NewMessage(MsgTypeAudioOnlyServer, flag)
	if err != nil {
		return err
	}
	msg.Sequence = sequence
	msg.Payload = payload
	frame, err := msg.Marshal()
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

func makeTestOggPage(packet []byte, sequence uint32) []byte {
	header := make([]byte, 27)
	copy(header[:4], "OggS")
	binary.LittleEndian.PutUint32(header[18:22], sequence)
	header[26] = 1
	return append(append(header, byte(len(packet))), packet...)
}
