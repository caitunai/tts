package elevenlabstts

import (
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
		Name:         "elevenlabs",
		Endpoint:     "wss://api.elevenlabs.io/v1/text-to-speech/:voice_id/stream-input",
		DefaultVoice: "voice-001",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.Voices) != 0 {
		t.Fatalf("voices = %#v, want no platform voice restriction", caps.Voices)
	}
	if !caps.SupportsAppendText {
		t.Fatal("SupportsAppendText = false, want true")
	}
	if !caps.SupportsOggOpusOutput {
		t.Fatal("SupportsOggOpusOutput = false, want true")
	}
	if caps.SupportsGuidanceText {
		t.Fatal("SupportsGuidanceText = true, want false")
	}
	if !containsTransport(caps.Transports, tts.TransportWebSocket) {
		t.Fatalf("transports = %#v, want websocket", caps.Transports)
	}
	if !containsCodec(caps.OutputCodecs, audio.CodecOpus) {
		t.Fatalf("output codecs = %#v, want opus", caps.OutputCodecs)
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != audio.OpusSampleRate {
		t.Fatalf("output sample rates = %#v, want %d", caps.OutputSampleRates, audio.OpusSampleRate)
	}
}

func TestProviderRejectsSynthesizeOnce(t *testing.T) {
	provider, err := NewProvider(Config{
		Name:     "elevenlabs",
		Endpoint: "wss://api.elevenlabs.io/v1/text-to-speech/:voice_id/stream-input",
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
		Name:         "elevenlabs",
		Endpoint:     server.url + "/v1/text-to-speech/:voice_id/stream-input",
		APIKey:       "test-key",
		Model:        "eleven-test-model",
		DefaultVoice: "voice-default",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	session, err := provider.OpenSession(context.Background(), &tts.ProviderOpenSessionRequest{
		SessionID: "sess_001",
		Voice:     "voice-request",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	if server.path != "/v1/text-to-speech/voice-request/stream-input" {
		t.Fatalf("path = %q, want voice path", server.path)
	}
	if server.query.Get("output_format") != defaultOutputFormat {
		t.Fatalf("output_format = %q, want %s", server.query.Get("output_format"), defaultOutputFormat)
	}
	if server.query.Get("model_id") != "eleven-test-model" {
		t.Fatalf("model_id = %q, want eleven-test-model", server.query.Get("model_id"))
	}

	startMsg := server.nextMessage()
	var start startRequest
	if err := json.Unmarshal(startMsg, &start); err != nil {
		t.Fatalf("decode start request: %v", err)
	}
	if start.Text != " " {
		t.Fatalf("start text = %q, want single space", start.Text)
	}
	if start.XiAPIKey != "test-key" {
		t.Fatalf("xi_api_key = %q, want test-key", start.XiAPIKey)
	}
	if start.VoiceSettings == nil {
		t.Fatal("voice_settings is nil")
	}
	if start.VoiceSettings.Stability != defaultStability {
		t.Fatalf("stability = %v, want %v", start.VoiceSettings.Stability, defaultStability)
	}

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.ProviderSegmentRequest{
		SegmentID: "seg_001",
		Text:      "hello",
		IsLast:    true,
	}); err != nil {
		t.Fatalf("AppendText: %v", err)
	}

	appendMsg := server.nextMessage()
	var appendText speakRequest
	if err := json.Unmarshal(appendMsg, &appendText); err != nil {
		t.Fatalf("decode append request: %v", err)
	}
	if appendText.Text != "hello " {
		t.Fatalf("append text = %q, want hello with trailing space", appendText.Text)
	}
	if !appendText.TryTriggerGeneration {
		t.Fatal("try_trigger_generation = false, want true")
	}
	if !appendText.Flush {
		t.Fatal("flush = false, want true")
	}

	finishMsg := server.nextMessage()
	var finish speakRequest
	if err := json.Unmarshal(finishMsg, &finish); err != nil {
		t.Fatalf("decode finish request: %v", err)
	}
	if finish.Text != "" {
		t.Fatalf("finish text = %q, want empty", finish.Text)
	}

	startEvent := nextProviderEvent(t, events)
	if startEvent.Type != tts.ProviderEventSegmentStart || startEvent.SegmentID != "seg_001" {
		t.Fatalf("start event = %#v, want seg_001 segment start", startEvent)
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

	endEvent := nextProviderEvent(t, events)
	if endEvent.Type != tts.ProviderEventSegmentEnd || endEvent.SegmentID != "seg_001" {
		t.Fatalf("end event = %#v, want seg_001 segment end", endEvent)
	}

	sessionEnd := nextProviderEvent(t, events)
	if sessionEnd.Type != tts.ProviderEventSessionEnd {
		t.Fatalf("session end = %#v, want session_end", sessionEnd)
	}
}

func TestServiceSessionAppendsTwoSegments(t *testing.T) {
	server := newRealtimeTestServer(t)
	defer server.close()

	provider, err := NewProvider(Config{
		Name:         "elevenlabs",
		Endpoint:     server.url + "/v1/text-to-speech/:voice_id/stream-input",
		APIKey:       "test-key",
		Model:        "eleven-test-model",
		DefaultVoice: "voice-default",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := tts.NewService("test-elevenlabs", registry)

	session, err := service.OpenSession(context.Background(), &tts.OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "elevenlabs",
		Voice:     "voice-default",
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

	_ = server.nextMessage()
	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{
		SegmentID: "seg_001",
		Text:      "first",
		Voice:     "voice-default",
		IsLast:    false,
	}); err != nil {
		t.Fatalf("AppendText first: %v", err)
	}
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{
		SegmentID: "seg_002",
		Text:      "second",
		Voice:     "voice-default",
		IsLast:    true,
	}); err != nil {
		t.Fatalf("AppendText second: %v", err)
	}
	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	firstMsg := server.nextMessage()
	assertSpeakText(t, firstMsg, "first ")

	expectServiceEvent(t, events, tts.EventSegmentStart, "seg_001")
	expectServiceEvent(t, events, tts.EventSegmentEnd, "seg_001")

	secondMsg := server.nextMessage()
	assertSpeakText(t, secondMsg, "second ")
	finishMsg := server.nextMessage()
	assertSpeakText(t, finishMsg, "")

	expectServiceEvent(t, events, tts.EventSegmentStart, "seg_002")
	expectServiceEvent(t, events, tts.EventSegmentEnd, "seg_002")
	expectServiceEvent(t, events, tts.EventSessionEnd, "")
}

func TestProviderRealtimeSessionMapsInvalidAudio(t *testing.T) {
	server := newRealtimeTestServer(t)
	server.invalidAudio = true
	defer server.close()

	provider, err := NewProvider(Config{
		Name:         "elevenlabs",
		Endpoint:     server.url + "/v1/text-to-speech/:voice_id/stream-input",
		APIKey:       "test-key",
		DefaultVoice: "voice-default",
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
		IsLast:    true,
	}); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	_ = server.nextMessage()

	start := nextProviderEvent(t, events)
	if start.Type != tts.ProviderEventSegmentStart {
		t.Fatalf("start event = %#v, want segment start", start)
	}
	errEvent := nextProviderEvent(t, events)
	if errEvent.Type != tts.ProviderEventError {
		t.Fatalf("event type = %q, want error", errEvent.Type)
	}
	if errEvent.Error == nil || errEvent.Error.Code != tts.ErrAudioDecodeFailed {
		t.Fatalf("error = %#v, want audio decode failed", errEvent.Error)
	}
}

func TestRealtimeURL(t *testing.T) {
	provider, err := NewProvider(Config{
		Endpoint:     "wss://example.test/v1/text-to-speech/:voice_id/stream-input?enable_logging=false",
		Model:        "model-one",
		OutputFormat: "opus_48000_32",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	got, err := provider.realtimeURL("voice/id")
	if err != nil {
		t.Fatalf("realtimeURL: %v", err)
	}
	if !strings.Contains(got, "/voice%2Fid/") {
		t.Fatalf("url = %q, want escaped voice id", got)
	}
	if !strings.Contains(got, "enable_logging=false") {
		t.Fatalf("url = %q, want existing query", got)
	}
	if !strings.Contains(got, "model_id=model-one") {
		t.Fatalf("url = %q, want model_id", got)
	}
	if !strings.Contains(got, "output_format=opus_48000_32") {
		t.Fatalf("url = %q, want output_format", got)
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
	oggSeq       uint32
	invalidAudio bool
	path         string
	query        urlValues
}

type urlValues interface {
	Get(string) string
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
		if got := r.Header.Get("xi-api-key"); got != "test-key" {
			testServer.errors <- fmt.Errorf("xi-api-key = %q", got)
			return
		}
		testServer.path = r.URL.Path
		testServer.query = r.URL.Query()

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

			var msg speakRequest
			if err := json.Unmarshal(data, &msg); err != nil {
				testServer.errors <- err
				continue
			}
			switch {
			case msg.Text == "":
				if err := conn.WriteJSON(speakResponse{IsFinal: true}); err != nil {
					testServer.errors <- err
				}
				return
			case msg.Text != " ":
				payload := base64.StdEncoding.EncodeToString(testServer.nextOgg())
				if testServer.invalidAudio {
					payload = "not-base64"
				}
				if err := conn.WriteJSON(speakResponse{Audio: payload}); err != nil {
					testServer.errors <- err
					return
				}
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

func (s *realtimeTestServer) nextOgg() []byte {
	ogg := makeTestOggPageWithSeq([]byte("opus-packet"), s.oggSeq)
	s.oggSeq++
	return ogg
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

func expectServiceAudio(t *testing.T, events <-chan *tts.Event, segmentID string) {
	t.Helper()
	event := nextServiceEvent(t, events)
	if event.Type != tts.EventAudioFrame {
		t.Fatalf("event type = %q, want audio_frame", event.Type)
	}
	if event.SegmentID != segmentID {
		t.Fatalf("audio segment = %q, want %q", event.SegmentID, segmentID)
	}
	if event.Audio == nil {
		t.Fatal("audio frame is nil")
	}
	if event.Audio.Codec != audio.CodecOpus {
		t.Fatalf("audio codec = %q, want opus", event.Audio.Codec)
	}
	if string(event.Audio.Data) != "opus-packet" {
		t.Fatalf("audio data = %q, want opus-packet", string(event.Audio.Data))
	}
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

func assertSpeakText(t *testing.T, msg []byte, want string) {
	t.Helper()
	var req speakRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		t.Fatalf("decode speak request: %v", err)
	}
	if req.Text != want {
		t.Fatalf("speak text = %q, want %q", req.Text, want)
	}
}

func makeTestOggPage(packet []byte) []byte {
	return makeTestOggPageWithSeq(packet, 0)
}

func makeTestOggPageWithSeq(packet []byte, seq uint32) []byte {
	header := make([]byte, 27)
	copy(header[:4], "OggS")
	binary.LittleEndian.PutUint32(header[18:22], seq)
	header[26] = 1
	return append(append(header, byte(len(packet))), packet...)
}
