package qwenrealtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
		Name:         "qwen_realtime",
		Endpoint:     "wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model=",
		DefaultVoice: "Cherry",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.Voices) != 1 || caps.Voices[0].ID != "Cherry" {
		t.Fatalf("voices = %#v, want Cherry", caps.Voices)
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != audio.OpusSampleRate {
		t.Fatalf("output sample rates = %#v, want %d", caps.OutputSampleRates, audio.OpusSampleRate)
	}
	if len(caps.Languages) != 11 {
		t.Fatalf("languages length = %d, want 11", len(caps.Languages))
	}
	if !caps.SupportsAppendText {
		t.Fatal("SupportsAppendText = false, want true")
	}
	if !caps.SupportsGuidanceText {
		t.Fatal("SupportsGuidanceText = false, want true")
	}
	if !caps.SupportsSegmentLevelGuidance {
		t.Fatal("SupportsSegmentLevelGuidance = false, want true")
	}
	if !caps.SupportsOggOpusOutput {
		t.Fatal("SupportsOggOpusOutput = false, want true")
	}
	if containsTransport(caps.Transports, tts.TransportHTTP) {
		t.Fatalf("transports = %#v, should not include http", caps.Transports)
	}
	if !containsTransport(caps.Transports, tts.TransportWebSocket) {
		t.Fatalf("transports = %#v, want websocket", caps.Transports)
	}
	if !containsCodec(caps.OutputCodecs, audio.CodecOpus) {
		t.Fatalf("output codecs = %#v, want opus", caps.OutputCodecs)
	}
}

func TestProviderRejectsNon48KOpusSampleRate(t *testing.T) {
	_, err := NewProvider(Config{
		Name:       "qwen_realtime",
		Endpoint:   "wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model=",
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
	provider, err := NewProvider(Config{
		Name:     "qwen_realtime",
		Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model=",
	})
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

func TestProviderRealtimeSessionStreamsOggOpus(t *testing.T) {
	server := newRealtimeTestServer(t)
	defer server.close()

	provider, err := NewProvider(Config{
		Name:            "qwen_realtime",
		Endpoint:        server.url + "/",
		Token:           "test-token",
		Model:           "qwen-realtime-test",
		DefaultVoice:    "Cherry",
		DefaultLanguage: "zh",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	session, err := provider.OpenSession(context.Background(), &tts.ProviderOpenSessionRequest{
		SessionID:    "sess_001",
		Voice:        "Dylan",
		Language:     "en",
		GuidanceText: "warm narration",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	updateMsg := server.nextMessage()
	var update sessionUpdate
	if err := json.Unmarshal(updateMsg, &update); err != nil {
		t.Fatalf("decode session.update: %v", err)
	}
	if update.Type != "session.update" {
		t.Fatalf("update type = %q, want session.update", update.Type)
	}
	if update.Session.Voice != "Dylan" {
		t.Fatalf("voice = %q, want Dylan", update.Session.Voice)
	}
	if update.Session.LanguageType != English {
		t.Fatalf("language_type = %q, want English", update.Session.LanguageType)
	}
	if update.Session.ResponseFormat != "opus" {
		t.Fatalf("response_format = %q, want opus", update.Session.ResponseFormat)
	}
	if update.Session.SampleRate != audio.OpusSampleRate {
		t.Fatalf("sample_rate = %d, want %d", update.Session.SampleRate, audio.OpusSampleRate)
	}
	if update.Session.Instructions != "warm narration" {
		t.Fatalf("instructions = %q, want warm narration", update.Session.Instructions)
	}

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.ProviderSegmentRequest{
		SegmentID: "seg_001",
		Text:      "hello",
	}); err != nil {
		t.Fatalf("AppendText: %v", err)
	}

	appendMsg := server.nextMessage()
	var appendText textMessage
	if err := json.Unmarshal(appendMsg, &appendText); err != nil {
		t.Fatalf("decode append message: %v", err)
	}
	if appendText.Type != "input_text_buffer.append" {
		t.Fatalf("append type = %q, want input_text_buffer.append", appendText.Type)
	}
	if appendText.Text != "hello" {
		t.Fatalf("append text = %q, want hello", appendText.Text)
	}

	commitMsg := server.nextMessage()
	var commit textMessage
	if err := json.Unmarshal(commitMsg, &commit); err != nil {
		t.Fatalf("decode commit message: %v", err)
	}
	if commit.Type != "input_text_buffer.commit" {
		t.Fatalf("commit type = %q, want input_text_buffer.commit", commit.Type)
	}

	start := nextProviderEvent(t, events)
	if start.Type != tts.ProviderEventSegmentStart || start.SegmentID != "seg_001" {
		t.Fatalf("start event = %#v, want seg_001 segment start", start)
	}

	audioEvent := nextProviderEvent(t, events)
	if audioEvent.Type != tts.ProviderEventAudio {
		t.Fatalf("audio event type = %q, want audio", audioEvent.Type)
	}
	if audioEvent.Audio == nil {
		t.Fatal("audio chunk is nil")
	}
	if audioEvent.Audio.Codec != audio.CodecOpus {
		t.Fatalf("audio codec = %q, want opus", audioEvent.Audio.Codec)
	}
	if audioEvent.Audio.Container != audio.ContainerOgg {
		t.Fatalf("audio container = %q, want ogg", audioEvent.Audio.Container)
	}
	if audioEvent.Audio.SampleRate != audio.OpusSampleRate {
		t.Fatalf("audio sample rate = %d, want %d", audioEvent.Audio.SampleRate, audio.OpusSampleRate)
	}
	if string(audioEvent.Audio.Data) != string(server.ogg) {
		t.Fatalf("audio data = %v, want %v", audioEvent.Audio.Data, server.ogg)
	}

	end := nextProviderEvent(t, events)
	if end.Type != tts.ProviderEventSegmentEnd || end.SegmentID != "seg_001" {
		t.Fatalf("end event = %#v, want seg_001 segment end", end)
	}

	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	finishMsg := server.nextMessage()
	var finish textMessage
	if err := json.Unmarshal(finishMsg, &finish); err != nil {
		t.Fatalf("decode finish message: %v", err)
	}
	if finish.Type != "session.finish" {
		t.Fatalf("finish type = %q, want session.finish", finish.Type)
	}

	sessionEnd := nextProviderEvent(t, events)
	if sessionEnd.Type != tts.ProviderEventSessionEnd {
		t.Fatalf("session end event = %#v, want session_end", sessionEnd)
	}
}

func TestProviderRealtimeSessionMapsInvalidDelta(t *testing.T) {
	server := newRealtimeTestServer(t)
	server.invalidDelta = true
	defer server.close()

	provider, err := NewProvider(Config{
		Name:     "qwen_realtime",
		Endpoint: server.url + "/",
		Model:    "qwen-realtime-test",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	session, err := provider.OpenSession(context.Background(), &tts.ProviderOpenSessionRequest{SessionID: "sess_001"})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()
	_ = server.nextMessage()

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.ProviderSegmentRequest{
		SegmentID: "seg_bad",
		Text:      "hello",
	}); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	_ = server.nextMessage()
	_ = server.nextMessage()

	start := nextProviderEvent(t, events)
	if start.Type != tts.ProviderEventSegmentStart {
		t.Fatalf("start event = %#v, want segment start", start)
	}
	errEvent := nextProviderEvent(t, events)
	if errEvent.Type != tts.ProviderEventError {
		t.Fatalf("error event type = %q, want error", errEvent.Type)
	}
	if errEvent.Error == nil || errEvent.Error.Code != tts.ErrAudioDecodeFailed {
		t.Fatalf("error = %#v, want audio decode failed", errEvent.Error)
	}
}

func TestRewriteLang(t *testing.T) {
	tests := map[string]string{
		"zh":      Chinese,
		"en":      English,
		"de":      German,
		"it":      Italian,
		"pt":      Portuguese,
		"es":      Spanish,
		"ja":      Japanese,
		"ko":      Korean,
		"fr":      French,
		"ru":      Russian,
		"Chinese": Chinese,
		"unknown": Auto,
		"":        Auto,
	}

	for input, want := range tests {
		if got := rewriteLang(input); got != want {
			t.Fatalf("rewriteLang(%q) = %q, want %q", input, got, want)
		}
	}
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

type realtimeTestServer struct {
	server       *httptest.Server
	url          string
	messages     chan []byte
	errors       chan error
	ogg          []byte
	invalidDelta bool
}

func newRealtimeTestServer(t *testing.T) *realtimeTestServer {
	t.Helper()

	testServer := &realtimeTestServer{
		messages: make(chan []byte, 16),
		errors:   make(chan error, 4),
		ogg:      makeTestOggPage([]byte("opus-packet")),
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" && got != "Bearer test-token" {
			testServer.errors <- fmt.Errorf("authorization = %q", got)
			return
		}
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
			copied := append([]byte(nil), data...)
			testServer.messages <- copied

			var msg textMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				testServer.errors <- err
				continue
			}

			switch msg.Type {
			case "input_text_buffer.commit":
				delta := base64.StdEncoding.EncodeToString(testServer.ogg)
				if testServer.invalidDelta {
					delta = "not-base64"
				}
				if err := conn.WriteJSON(realtimeMessage{Type: "response.audio.delta", Delta: delta}); err != nil {
					testServer.errors <- err
					return
				}
				if err := conn.WriteJSON(realtimeMessage{Type: "response.done"}); err != nil {
					testServer.errors <- err
					return
				}
			case "session.finish":
				if err := conn.WriteJSON(realtimeMessage{Type: "session.finished"}); err != nil {
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

func (s *realtimeTestServer) close() {
	s.server.Close()
}

func (s *realtimeTestServer) nextMessage() []byte {
	select {
	case err := <-s.errors:
		panic(err)
	case msg := <-s.messages:
		return msg
	case <-time.After(3 * time.Second):
		panic("timeout waiting for websocket message")
	}
}

func nextProviderEvent(t *testing.T, events <-chan *tts.ProviderEvent) *tts.ProviderEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("events channel closed")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for provider event")
	}
	return nil
}

func makeTestOggPage(packet []byte) []byte {
	header := make([]byte, 27)
	copy(header[:4], "OggS")
	header[26] = 1
	return append(append(header, byte(len(packet))), packet...)
}
