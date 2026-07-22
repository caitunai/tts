package qwenaudiotts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

func TestProviderCapabilities(t *testing.T) {
	provider, err := NewProvider(Config{
		Endpoint:     "wss://example.test/api-ws/v1/inference/",
		DefaultVoice: "longanlingxi",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.Name != defaultProviderName {
		t.Fatalf("provider name = %q, want %q", caps.Name, defaultProviderName)
	}
	if !containsTransport(caps.Transports, tts.TransportWebSocket) {
		t.Fatalf("transports = %#v, want websocket", caps.Transports)
	}
	if !caps.SupportsAppendText || !caps.SupportsStreaming {
		t.Fatalf("append/streaming support = %v/%v, want true/true", caps.SupportsAppendText, caps.SupportsStreaming)
	}
	if !caps.SupportsGuidanceText {
		t.Fatal("SupportsGuidanceText = false, want true")
	}
	if !caps.SupportsOggOpusOutput {
		t.Fatal("SupportsOggOpusOutput = false, want true")
	}
	if !containsCodec(caps.OutputCodecs, audio.CodecOpus) {
		t.Fatalf("output codecs = %#v, want opus", caps.OutputCodecs)
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != audio.OpusSampleRate {
		t.Fatalf("sample rates = %#v, want 48000", caps.OutputSampleRates)
	}
	if len(caps.Voices) != 1 || caps.Voices[0].ID != "longanlingxi" {
		t.Fatalf("voices = %#v, want longanlingxi", caps.Voices)
	}
}

func TestProviderRejectsInvalidOpusSampleRate(t *testing.T) {
	_, err := NewProvider(Config{
		Endpoint:   "wss://example.test/api-ws/v1/inference/",
		SampleRate: 24000,
	})
	if err == nil {
		t.Fatal("NewProvider returned nil error, want sample rate error")
	}
	ttsErr, ok := err.(*tts.Error)
	if !ok {
		t.Fatalf("error type = %T, want *tts.Error", err)
	}
	if ttsErr.Code != tts.ErrUnsupportedProvider {
		t.Fatalf("error code = %q, want unsupported provider", ttsErr.Code)
	}
}

func TestProviderRejectsSynthesizeOnce(t *testing.T) {
	provider, err := NewProvider(Config{Endpoint: "wss://example.test/api-ws/v1/inference/"})
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

func TestProviderFinishDelayConfig(t *testing.T) {
	defaulted, err := NewProvider(Config{
		Endpoint:    "wss://example.test/api-ws/v1/inference/",
		FinishDelay: -1,
	})
	if err != nil {
		t.Fatalf("NewProvider defaulted: %v", err)
	}
	if defaulted.finishDelay != defaultFinishDelay {
		t.Fatalf("finish delay = %s, want default %s", defaulted.finishDelay, defaultFinishDelay)
	}

	immediate, err := NewProvider(Config{
		Endpoint:    "wss://example.test/api-ws/v1/inference/",
		FinishDelay: 0,
	})
	if err != nil {
		t.Fatalf("NewProvider immediate: %v", err)
	}
	if immediate.finishDelay != 0 {
		t.Fatalf("finish delay = %s, want immediate 0", immediate.finishDelay)
	}
}

func TestProviderRealtimeSessionStreamsOggOpus(t *testing.T) {
	server := newQwenAudioTestServer(t)
	defer server.close()

	provider, err := NewProvider(Config{
		Name:                "qwen_audio_test",
		Endpoint:            server.url,
		APIKey:              "test-token",
		Model:               "qwen-audio-test",
		DefaultVoice:        "longanlingxi",
		DefaultLanguage:     "zh",
		DefaultInstructions: "default style",
		BitRate:             48,
		FinishDelay:         time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := provider.OpenSession(ctx, &tts.ProviderOpenSessionRequest{
		SessionID:    "sess_001",
		Voice:        "longanbella",
		Language:     "en",
		GuidanceText: "warm narration",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	if got := server.authHeader(); got != "bearer test-token" {
		t.Fatalf("authorization = %q, want bearer test-token", got)
	}
	if got := server.dataInspectionHeader(); got != "enable" {
		t.Fatalf("data inspection header = %q, want enable", got)
	}

	runTask := server.nextProtocolMessage()
	if runTask.Header.Action != "run-task" {
		t.Fatalf("run action = %q, want run-task", runTask.Header.Action)
	}
	if !server.messageHasInput("run-task") {
		t.Fatal("run-task payload.input is missing from encoded JSON")
	}
	if runTask.Payload.Model != "qwen-audio-test" {
		t.Fatalf("model = %q, want qwen-audio-test", runTask.Payload.Model)
	}
	if runTask.Payload.Parameters == nil {
		t.Fatal("run-task parameters are nil")
	}
	if runTask.Payload.Parameters.Format != defaultFormat {
		t.Fatalf("format = %q, want opus", runTask.Payload.Parameters.Format)
	}
	if runTask.Payload.Parameters.SampleRate != audio.OpusSampleRate {
		t.Fatalf("sample_rate = %d, want 48000", runTask.Payload.Parameters.SampleRate)
	}
	if runTask.Payload.Parameters.BitRate != 48 {
		t.Fatalf("bit_rate = %d, want 48", runTask.Payload.Parameters.BitRate)
	}
	if runTask.Payload.Parameters.Voice != "longanbella" {
		t.Fatalf("voice = %q, want longanbella", runTask.Payload.Parameters.Voice)
	}
	if runTask.Payload.Parameters.Instruction != "warm narration" {
		t.Fatalf("instruction = %q, want warm narration", runTask.Payload.Parameters.Instruction)
	}
	if len(runTask.Payload.Parameters.LanguageHints) != 1 || runTask.Payload.Parameters.LanguageHints[0] != "en" {
		t.Fatalf("language_hints = %#v, want [en]", runTask.Payload.Parameters.LanguageHints)
	}

	events := session.Events()
	start := nextProviderEvent(t, events)
	if start.Type != tts.ProviderEventSessionStart {
		t.Fatalf("first event = %q, want session_start", start.Type)
	}

	if err := session.AppendText(ctx, &tts.ProviderSegmentRequest{
		SegmentID: "seg_001",
		Text:      "hello",
	}); err != nil {
		t.Fatalf("AppendText seg_001: %v", err)
	}
	continueTask := server.nextProtocolMessage()
	if continueTask.Header.Action != "continue-task" {
		t.Fatalf("continue action = %q, want continue-task", continueTask.Header.Action)
	}
	if continueTask.Payload.Input["text"] != "hello" {
		t.Fatalf("continue text = %#v, want hello", continueTask.Payload.Input["text"])
	}

	segStart := nextProviderEvent(t, events)
	if segStart.Type != tts.ProviderEventSegmentStart || segStart.SegmentID != "seg_001" {
		t.Fatalf("segment start = %#v, want seg_001", segStart)
	}
	audioEvent := nextProviderAudioEvent(t, events, "seg_001")
	if audioEvent.Type != tts.ProviderEventAudio || audioEvent.SegmentID != "seg_001" {
		t.Fatalf("audio event = %#v, want seg_001 audio", audioEvent)
	}
	if audioEvent.Audio == nil {
		t.Fatal("audio chunk is nil")
	}
	if audioEvent.Audio.Codec != audio.CodecOpus || audioEvent.Audio.Container != audio.ContainerOgg {
		t.Fatalf("audio codec/container = %s/%s, want opus/ogg", audioEvent.Audio.Codec, audioEvent.Audio.Container)
	}
	if !bytes.Equal(audioEvent.Audio.Data, server.ogg) {
		t.Fatalf("audio data = %v, want %v", audioEvent.Audio.Data, server.ogg)
	}
	if err := session.AppendText(ctx, &tts.ProviderSegmentRequest{
		SegmentID: "seg_002",
		Text:      "world",
		IsLast:    true,
	}); err != nil {
		t.Fatalf("AppendText seg_002: %v", err)
	}
	continueTask = server.nextProtocolMessage()
	if continueTask.Header.Action != "continue-task" {
		t.Fatalf("second continue action = %q, want continue-task", continueTask.Header.Action)
	}
	finishTask := server.nextProtocolMessage()
	if finishTask.Header.Action != "finish-task" {
		t.Fatalf("finish action = %q, want finish-task", finishTask.Header.Action)
	}
	if !server.messageHasInput("finish-task") {
		t.Fatal("finish-task payload.input is missing from encoded JSON")
	}

	_ = nextProviderEvent(t, events) // seg_002 start
	_ = nextProviderAudioEvent(t, events, "seg_002")
	segEnd := nextProviderEvent(t, events)
	if segEnd.Type != tts.ProviderEventSegmentEnd || segEnd.SegmentID != "seg_002" {
		t.Fatalf("second segment end = %#v, want seg_002", segEnd)
	}
	sessionEnd := nextProviderEvent(t, events)
	if sessionEnd.Type != tts.ProviderEventSessionEnd {
		t.Fatalf("session end = %#v, want session_end", sessionEnd)
	}
}

func TestProviderOpenSessionReturnsTaskFailed(t *testing.T) {
	server := newQwenAudioFailingTestServer(t)
	defer server.Close()

	provider, err := NewProvider(Config{Endpoint: wsURL(server.URL)})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = provider.OpenSession(ctx, &tts.ProviderOpenSessionRequest{SessionID: "sess_001"})
	if err == nil {
		t.Fatal("OpenSession returned nil error, want task failed")
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("error = %q, want bad request", err.Error())
	}
}

func TestProviderOpenSessionReturnsCloseReasonBeforeTaskStarted(t *testing.T) {
	server := newQwenAudioClosingTestServer(t)
	defer server.Close()

	provider, err := NewProvider(Config{Endpoint: wsURL(server.URL)})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = provider.OpenSession(ctx, &tts.ProviderOpenSessionRequest{SessionID: "sess_001"})
	if err == nil {
		t.Fatal("OpenSession returned nil error, want websocket close error")
	}
	if strings.Contains(err.Error(), "session is closed before task started") {
		t.Fatalf("error = %q, should expose the websocket close reason", err.Error())
	}
	if !strings.Contains(err.Error(), "policy violation") || !strings.Contains(err.Error(), "missing required parameter") {
		t.Fatalf("error = %q, want websocket close reason", err.Error())
	}
}

type qwenAudioTestServer struct {
	server *httptest.Server
	url    string
	ogg    []byte

	authCh           chan string
	dataInspectionCh chan string
	messages         chan protocolMessage
	rawMessages      chan []byte
}

func newQwenAudioTestServer(t *testing.T) *qwenAudioTestServer {
	t.Helper()
	s := &qwenAudioTestServer{
		ogg:              oggChunk(t, []byte("opus-packet")),
		authCh:           make(chan string, 1),
		dataInspectionCh: make(chan string, 1),
		messages:         make(chan protocolMessage, 8),
		rawMessages:      make(chan []byte, 8),
	}
	upgrader := websocket.Upgrader{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.authCh <- r.Header.Get("Authorization")
		s.dataInspectionCh <- r.Header.Get("X-DashScope-DataInspection")

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer func() {
			_ = conn.Close()
		}()

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg protocolMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Errorf("Unmarshal request: %v", err)
				return
			}
			s.messages <- msg
			s.rawMessages <- append([]byte(nil), raw...)
			switch msg.Header.Action {
			case "run-task":
				writeProtocol(t, conn, "task-started", "")
			case "continue-task":
				writeProtocol(t, conn, "result-generated", "sentence-begin")
				writeProtocol(t, conn, "result-generated", "sentence-synthesis")
				if err := conn.WriteMessage(websocket.BinaryMessage, s.ogg); err != nil {
					t.Errorf("WriteMessage audio: %v", err)
					return
				}
				writeProtocol(t, conn, "result-generated", "sentence-end")
			case "finish-task":
				writeProtocol(t, conn, "task-finished", "")
				return
			}
		}
	}))
	s.url = wsURL(s.server.URL)
	return s
}

func (s *qwenAudioTestServer) messageHasInput(action string) bool {
	for {
		select {
		case raw := <-s.rawMessages:
			var msg map[string]any
			if err := json.Unmarshal(raw, &msg); err != nil {
				return false
			}
			header, _ := msg["header"].(map[string]any)
			if header["action"] != action {
				continue
			}
			payload, _ := msg["payload"].(map[string]any)
			_, ok := payload["input"]
			return ok
		case <-time.After(2 * time.Second):
			panic("timed out waiting for raw websocket request")
		}
	}
}

func (s *qwenAudioTestServer) close() {
	s.server.Close()
}

func (s *qwenAudioTestServer) nextProtocolMessage() protocolMessage {
	select {
	case msg := <-s.messages:
		return msg
	case <-time.After(2 * time.Second):
		panic("timed out waiting for websocket request")
	}
}

func (s *qwenAudioTestServer) authHeader() string {
	select {
	case value := <-s.authCh:
		return value
	case <-time.After(2 * time.Second):
		panic("timed out waiting for authorization header")
	}
}

func (s *qwenAudioTestServer) dataInspectionHeader() string {
	select {
	case value := <-s.dataInspectionCh:
		return value
	case <-time.After(2 * time.Second):
		panic("timed out waiting for data inspection header")
	}
}

func newQwenAudioFailingTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		var msg protocolMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Errorf("ReadJSON: %v", err)
			return
		}
		if msg.Header.Action != "run-task" {
			t.Errorf("action = %q, want run-task", msg.Header.Action)
		}
		resp := protocolMessage{
			Header: protocolHeader{
				Event:        "task-failed",
				TaskID:       msg.Header.TaskID,
				ErrorCode:    "InvalidParameter",
				ErrorMessage: "bad request",
			},
		}
		if err := conn.WriteJSON(resp); err != nil {
			t.Errorf("WriteJSON: %v", err)
		}
	}))
}

func newQwenAudioClosingTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("ReadMessage: %v", err)
			return
		}
		message := websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "missing required parameter")
		if err := conn.WriteControl(websocket.CloseMessage, message, time.Now().Add(time.Second)); err != nil {
			t.Errorf("WriteControl close: %v", err)
		}
	}))
}

func writeProtocol(t *testing.T, conn *websocket.Conn, event, outputType string) {
	t.Helper()
	msg := protocolMessage{
		Header: protocolHeader{
			Event: event,
		},
	}
	if outputType != "" {
		msg.Payload.Output = &protocolOutput{Type: outputType}
	}
	if err := conn.WriteJSON(msg); err != nil {
		t.Errorf("WriteJSON %s/%s: %v", event, outputType, err)
	}
}

func oggChunk(t *testing.T, packet []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	muxer := audio.NewOggOpusMuxer()
	if err := muxer.WritePacket(&buf, packet); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}
	return buf.Bytes()
}

func nextProviderEvent(t *testing.T, events <-chan *tts.ProviderEvent) *tts.ProviderEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("provider events closed")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provider event")
		return nil
	}
}

func nextProviderAudioEvent(t *testing.T, events <-chan *tts.ProviderEvent, segmentID string) *tts.ProviderEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("provider events closed")
			}
			if event.Type == tts.ProviderEventAudio && event.SegmentID == segmentID {
				return event
			}
			if event.SegmentID != "" && event.SegmentID != segmentID {
				t.Fatalf("event = %#v, want audio for %s", event, segmentID)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for audio event for %s", segmentID)
		}
	}
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func containsTransport(values []tts.TransportType, target tts.TransportType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsCodec(values []audio.Codec, target audio.Codec) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
