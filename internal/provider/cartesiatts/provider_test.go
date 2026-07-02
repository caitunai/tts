package cartesiatts

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	provider, err := NewProvider(Config{Name: "cartesia", DefaultLanguage: "en"})
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
	if !caps.SupportsPCMOutput {
		t.Fatal("SupportsPCMOutput = false, want true")
	}
	if !caps.SupportsSpeed || !caps.SupportsVolume || !caps.SupportsEmotion {
		t.Fatalf("speed/volume/emotion support = %v/%v/%v", caps.SupportsSpeed, caps.SupportsVolume, caps.SupportsEmotion)
	}
	if len(caps.OutputSampleRates) == 0 || caps.OutputSampleRates[0] != audio.DefaultSampleRate {
		t.Fatalf("OutputSampleRates = %#v, want first %d", caps.OutputSampleRates, audio.DefaultSampleRate)
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
	server := newCartesiaTestServer(t)
	defer server.close()

	provider, err := NewProvider(Config{
		Name:             "cartesia",
		Endpoint:         server.url,
		APIKey:           "test-key",
		Version:          "2026-03-01",
		Model:            "sonic-3.5",
		DefaultVoice:     "voice-default",
		DefaultLanguage:  "en",
		MaxBufferDelayMS: 250,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := tts.NewService("test-cartesia", registry)

	session, err := service.OpenSession(context.Background(), &tts.OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "cartesia",
		Voice:     "voice-123",
		Language:  "en",
		Output: audio.OutputConfig{
			PreferCodec:         audio.CodecPCM,
			SampleRate:          audio.DefaultSampleRate,
			Channels:            audio.DefaultChannels,
			FrameMS:             audio.DefaultFrameMS,
			PCMFormat:           audio.PCMFormatS16LE,
			AllowPCMFrameOutput: true,
		},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{
		SegmentID: "seg_001",
		Text:      "Hello first",
		Voice:     "voice-123",
		Language:  "en",
		Speed:     1.2,
		Volume:    0.8,
		Emotion:   "calm",
	}); err != nil {
		t.Fatalf("AppendText first: %v", err)
	}
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{
		SegmentID: "seg_002",
		Text:      "Hello second",
		Voice:     "voice-123",
		Language:  "en",
		IsLast:    true,
	}); err != nil {
		t.Fatalf("AppendText second: %v", err)
	}
	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	firstMsg := server.nextMessage()
	assertGenerationRequest(t, firstMsg, generationExpectation{
		Model:            "sonic-3.5",
		Transcript:       "Hello first",
		Voice:            "voice-123",
		Language:         "en",
		ContextID:        "sess_001_seg_001",
		SampleRate:       audio.DefaultSampleRate,
		MaxBufferDelayMS: 250,
		Speed:            1.2,
		Volume:           0.8,
		Emotion:          "calm",
	})
	expectServiceEvent(t, events, tts.EventSegmentStart, "seg_001")
	expectServiceAudio(t, events, "seg_001", pcmFrameBytes(audio.DefaultSampleRate))
	expectServiceEvent(t, events, tts.EventSegmentEnd, "seg_001")

	secondMsg := server.nextMessage()
	assertGenerationRequest(t, secondMsg, generationExpectation{
		Model:      "sonic-3.5",
		Transcript: "Hello second",
		Voice:      "voice-123",
		Language:   "en",
		ContextID:  "sess_001_seg_002",
		SampleRate: audio.DefaultSampleRate,
	})
	expectServiceEvent(t, events, tts.EventSegmentStart, "seg_002")
	expectServiceAudio(t, events, "seg_002", pcmFrameBytes(audio.DefaultSampleRate))
	expectServiceEvent(t, events, tts.EventSegmentEnd, "seg_002")
	expectServiceEvent(t, events, tts.EventSessionEnd, "")

	if got := server.apiKey; got != "test-key" {
		t.Fatalf("api key = %q, want test-key", got)
	}
	if got := server.version; got != "2026-03-01" {
		t.Fatalf("cartesia_version = %q, want 2026-03-01", got)
	}
}

func TestProviderMapsErrorResponse(t *testing.T) {
	server := newCartesiaTestServer(t)
	server.errorResponse = true
	defer server.close()

	provider, err := NewProvider(Config{
		Name:         "cartesia",
		Endpoint:     server.url,
		APIKey:       "test-key",
		DefaultVoice: "voice-123",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	session, err := provider.OpenSession(context.Background(), &tts.ProviderOpenSessionRequest{SessionID: "sess_001", Voice: "voice-123"})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.ProviderSegmentRequest{SegmentID: "seg_001", Text: "hello"}); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	_ = server.nextMessage()

	event := nextProviderEvent(t, events)
	if event.Type != tts.ProviderEventSegmentStart {
		t.Fatalf("first event = %q, want segment_start", event.Type)
	}
	event = nextProviderEvent(t, events)
	if event.Type != tts.ProviderEventError {
		t.Fatalf("second event = %q, want error", event.Type)
	}
	if event.Error == nil || event.Error.Code != tts.ErrProviderAuthFailed {
		t.Fatalf("error = %#v, want auth failed", event.Error)
	}
}

func assertGenerationRequest(t *testing.T, data []byte, want generationExpectation) {
	t.Helper()
	var got generationRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode generation request: %v", err)
	}
	if got.ModelID != want.Model {
		t.Fatalf("model = %q, want %q", got.ModelID, want.Model)
	}
	if got.Transcript != want.Transcript {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want.Transcript)
	}
	if got.Voice.ID != want.Voice || got.Voice.Mode != "id" {
		t.Fatalf("voice = %#v, want id/%s", got.Voice, want.Voice)
	}
	if got.Language != want.Language {
		t.Fatalf("language = %q, want %q", got.Language, want.Language)
	}
	if got.ContextID != want.ContextID {
		t.Fatalf("context_id = %q, want %q", got.ContextID, want.ContextID)
	}
	if got.OutputFormat.Container != defaultContainer || got.OutputFormat.Encoding != defaultEncoding {
		t.Fatalf("output_format = %#v", got.OutputFormat)
	}
	if got.OutputFormat.SampleRate != want.SampleRate {
		t.Fatalf("sample_rate = %d, want %d", got.OutputFormat.SampleRate, want.SampleRate)
	}
	if want.MaxBufferDelayMS > 0 {
		if got.MaxBufferDelayMS == nil || *got.MaxBufferDelayMS != want.MaxBufferDelayMS {
			t.Fatalf("max_buffer_delay_ms = %#v, want %d", got.MaxBufferDelayMS, want.MaxBufferDelayMS)
		}
	}
	if want.Speed > 0 || want.Volume > 0 || want.Emotion != "" {
		if got.GenerationConfig == nil {
			t.Fatal("generation_config is nil")
		}
		if got.GenerationConfig.Speed != want.Speed {
			t.Fatalf("speed = %v, want %v", got.GenerationConfig.Speed, want.Speed)
		}
		if got.GenerationConfig.Volume != want.Volume {
			t.Fatalf("volume = %v, want %v", got.GenerationConfig.Volume, want.Volume)
		}
		if got.GenerationConfig.Emotion != want.Emotion {
			t.Fatalf("emotion = %q, want %q", got.GenerationConfig.Emotion, want.Emotion)
		}
	}
}

type generationExpectation struct {
	Model            string
	Transcript       string
	Voice            string
	Language         string
	ContextID        string
	SampleRate       int
	MaxBufferDelayMS int
	Speed            float64
	Volume           float64
	Emotion          string
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

func expectServiceAudio(t *testing.T, events <-chan *tts.Event, segmentID string, wantBytes int) {
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
		if event.Audio.Codec != audio.CodecPCM {
			t.Fatalf("audio codec = %q, want pcm", event.Audio.Codec)
		}
		if event.Audio.SampleRate != audio.DefaultSampleRate {
			t.Fatalf("audio sample rate = %d, want %d", event.Audio.SampleRate, audio.DefaultSampleRate)
		}
		if len(event.Audio.Data) != wantBytes {
			t.Fatalf("audio bytes = %d, want %d", len(event.Audio.Data), wantBytes)
		}
		return
	}
	t.Fatalf("did not receive audio segment=%q", segmentID)
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

type cartesiaTestServer struct {
	server        *httptest.Server
	url           string
	messages      chan []byte
	errors        chan error
	apiKey        string
	version       string
	errorResponse bool
}

func newCartesiaTestServer(t *testing.T) *cartesiaTestServer {
	t.Helper()

	testServer := &cartesiaTestServer{
		messages: make(chan []byte, 16),
		errors:   make(chan error, 4),
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testServer.apiKey = r.Header.Get("X-API-Key")
		testServer.version = r.URL.Query().Get("cartesia_version")

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

			var req generationRequest
			if err := json.Unmarshal(data, &req); err != nil {
				testServer.errors <- err
				return
			}
			if testServer.errorResponse {
				if err := conn.WriteJSON(responseMessage{
					Type:       "error",
					Done:       true,
					StatusCode: http.StatusUnauthorized,
					ErrorCode:  "unauthorized",
					Title:      "Unauthorized",
					Message:    "bad api key",
					ContextID:  req.ContextID,
				}); err != nil {
					testServer.errors <- err
				}
				continue
			}
			audioData := make([]byte, pcmFrameBytes(req.OutputFormat.SampleRate))
			for i := range audioData {
				audioData[i] = byte(i % 255)
			}
			if err := conn.WriteJSON(responseMessage{
				Type:       "chunk",
				Data:       base64.StdEncoding.EncodeToString(audioData),
				Done:       false,
				StatusCode: http.StatusPartialContent,
				StepTime:   12.3,
				ContextID:  req.ContextID,
			}); err != nil {
				testServer.errors <- err
				return
			}
			if err := conn.WriteJSON(responseMessage{
				Type:       "done",
				Done:       true,
				StatusCode: http.StatusPartialContent,
				ContextID:  req.ContextID,
			}); err != nil {
				testServer.errors <- err
				return
			}
		}
	}))
	testServer.server = server
	testServer.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return testServer
}

func (s *cartesiaTestServer) close() {
	s.server.Close()
}

func (s *cartesiaTestServer) nextMessage() []byte {
	select {
	case err := <-s.errors:
		panic(err)
	case msg := <-s.messages:
		return msg
	case <-time.After(3 * time.Second):
		panic("timeout waiting for websocket message")
	}
}

func pcmFrameBytes(sampleRate int) int {
	return sampleRate * audio.DefaultFrameMS / 1000 * audio.DefaultChannels * 2
}
