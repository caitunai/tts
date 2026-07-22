package minimaxtts

import (
	"context"
	"encoding/base64"
	"encoding/hex"
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
		Name:            "minimax",
		Endpoint:        "wss://example.test/ws",
		DefaultVoice:    "male-qn-qingse",
		DefaultLanguage: "zh",
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
	if !caps.SupportsPCMOutput {
		t.Fatal("SupportsPCMOutput = false, want true")
	}
	if !containsCodec(caps.OutputCodecs, audio.CodecMP3) {
		t.Fatalf("output codecs = %#v, want mp3", caps.OutputCodecs)
	}
	if !containsCodec(caps.OutputCodecs, audio.CodecPCM) {
		t.Fatalf("output codecs = %#v, want pcm", caps.OutputCodecs)
	}
	if len(caps.OutputSampleRates) != 1 || caps.OutputSampleRates[0] != defaultSampleRate {
		t.Fatalf("output sample rates = %#v, want %d", caps.OutputSampleRates, defaultSampleRate)
	}
	if len(caps.OutputChannels) != 1 || caps.OutputChannels[0] != audio.DefaultChannels {
		t.Fatalf("output channels = %#v, want %d", caps.OutputChannels, audio.DefaultChannels)
	}
	if len(caps.Languages) != 0 {
		t.Fatalf("languages = %#v, want no platform language restriction", caps.Languages)
	}
}

func TestProviderRejectsSynthesizeOnce(t *testing.T) {
	provider, err := NewProvider(Config{
		Name:     "minimax",
		Endpoint: "wss://example.test/ws",
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

func TestServiceSessionAppendsTwoSegments(t *testing.T) {
	server := newMinimaxTestServer(t)
	defer server.close()

	provider, err := NewProvider(Config{
		Name:            "minimax",
		Endpoint:        server.url,
		Token:           "test-token",
		Model:           "speech-test",
		DefaultVoice:    "female-shaonv",
		DefaultLanguage: "zh",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register: %v", err)
	}
	service := tts.NewService("test", registry)

	session, err := service.OpenSession(context.Background(), &tts.OpenSessionRequest{
		SessionID:    "sess_001",
		Provider:     "minimax",
		Voice:        "female-shaonv",
		Language:     "en",
		GuidanceText: string(EmotionHappy),
		Output: audio.OutputConfig{
			PreferCodec: audio.CodecPCM,
			SampleRate:  defaultSampleRate,
			Channels:    audio.DefaultChannels,
			FrameMS:     audio.DefaultFrameMS,
		},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() {
		_ = session.Close()
	}()

	startMsg := server.nextMessage()
	var start taskStartRequest
	if err := json.Unmarshal(startMsg, &start); err != nil {
		t.Fatalf("decode task_start: %v", err)
	}
	if start.Event != "task_start" {
		t.Fatalf("start event = %q, want task_start", start.Event)
	}
	if start.Model != "speech-test" {
		t.Fatalf("model = %q, want speech-test", start.Model)
	}
	if start.LanguageBoost != English {
		t.Fatalf("language_boost = %q, want English", start.LanguageBoost)
	}
	if start.VoiceSetting == nil || start.VoiceSetting.VoiceID != "female-shaonv" {
		t.Fatalf("voice_setting = %#v, want female-shaonv", start.VoiceSetting)
	}
	if start.VoiceSetting.Emotion != EmotionHappy {
		t.Fatalf("emotion = %q, want happy", start.VoiceSetting.Emotion)
	}
	if start.AudioSetting == nil || start.AudioSetting.Format != "mp3" {
		t.Fatalf("audio_setting = %#v, want mp3", start.AudioSetting)
	}

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{SegmentID: "seg_001", Text: "one"}); err != nil {
		t.Fatalf("AppendText seg_001: %v", err)
	}
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{SegmentID: "seg_002", Text: "two"}); err != nil {
		t.Fatalf("AppendText seg_002: %v", err)
	}
	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	firstContinue := server.nextMessage()
	var first taskEventRequest
	if err := json.Unmarshal(firstContinue, &first); err != nil {
		t.Fatalf("decode first task_continue: %v", err)
	}
	if first.Event != "task_continue" || first.Text != "one" {
		t.Fatalf("first continue = %#v, want one", first)
	}

	requireEvent(t, events, tts.EventSegmentStart, "seg_001")
	firstAudio := requireEvent(t, events, tts.EventAudioFrame, "seg_001")
	if firstAudio.Audio == nil {
		t.Fatal("first audio frame is nil")
	}
	if firstAudio.Audio.Codec != audio.CodecPCM {
		t.Fatalf("first codec = %q, want pcm", firstAudio.Audio.Codec)
	}
	if firstAudio.Audio.SampleRate != defaultSampleRate {
		t.Fatalf("first sample rate = %d, want %d", firstAudio.Audio.SampleRate, defaultSampleRate)
	}
	if firstAudio.Audio.Channels != audio.DefaultChannels {
		t.Fatalf("first channels = %d, want %d", firstAudio.Audio.Channels, audio.DefaultChannels)
	}
	if firstAudio.Audio.FrameMS != audio.DefaultFrameMS {
		t.Fatalf("first frame ms = %d, want %d", firstAudio.Audio.FrameMS, audio.DefaultFrameMS)
	}
	if len(firstAudio.Audio.Data) != 640 {
		t.Fatalf("first data length = %d, want 640", len(firstAudio.Audio.Data))
	}
	requireSegmentEndAfterAudio(t, events, "seg_001")

	secondContinue := server.nextMessage()
	var second taskEventRequest
	if err := json.Unmarshal(secondContinue, &second); err != nil {
		t.Fatalf("decode second task_continue: %v", err)
	}
	if second.Event != "task_continue" || second.Text != "two" {
		t.Fatalf("second continue = %#v, want two", second)
	}

	requireEvent(t, events, tts.EventSegmentStart, "seg_002")
	secondAudio := requireEvent(t, events, tts.EventAudioFrame, "seg_002")
	if secondAudio.Audio == nil {
		t.Fatal("second audio frame is nil")
	}
	if secondAudio.Audio.Codec != audio.CodecPCM {
		t.Fatalf("second codec = %q, want pcm", secondAudio.Audio.Codec)
	}
	if len(secondAudio.Audio.Data) != 640 {
		t.Fatalf("second data length = %d, want 640", len(secondAudio.Audio.Data))
	}
	requireSegmentEndAfterAudio(t, events, "seg_002")

	finishMsg := server.nextMessage()
	var finish taskEventRequest
	if err := json.Unmarshal(finishMsg, &finish); err != nil {
		t.Fatalf("decode task_finish: %v", err)
	}
	if finish.Event != "task_finish" {
		t.Fatalf("finish event = %q, want task_finish", finish.Event)
	}
	requireEvent(t, events, tts.EventSessionEnd, "")
}

func TestProviderRealtimeSessionMapsInvalidHexAudio(t *testing.T) {
	server := newMinimaxTestServer(t)
	server.invalidAudio = true
	defer server.close()

	provider, err := NewProvider(Config{
		Name:     "minimax",
		Endpoint: server.url,
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
		"yue":     Yue,
		"en":      English,
		"ar":      Arabic,
		"arz":     Arabic,
		"ru":      Russian,
		"Spanish": Spanish,
		"eng":     English,
		"en-US":   English,
		"zho":     Chinese,
		"cmn-CN":  Chinese,
		"yue-HK":  Yue,
		"spa":     Spanish,
		"tl":      Filipino,
		"tgl":     Filipino,
		"unknown": Auto,
		"":        Auto,
	}

	for input, want := range tests {
		if got := rewriteLang(input); got != want {
			t.Fatalf("rewriteLang(%q) = %q, want %q", input, got, want)
		}
	}
}

func containsCodec(values []audio.Codec, target audio.Codec) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type minimaxTestServer struct {
	server       *httptest.Server
	url          string
	messages     chan []byte
	errors       chan error
	invalidAudio bool
	continueN    int
}

func newMinimaxTestServer(t *testing.T) *minimaxTestServer {
	t.Helper()

	testServer := &minimaxTestServer{
		messages: make(chan []byte, 16),
		errors:   make(chan error, 4),
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

		if err := conn.WriteJSON(taskEventResponse{Event: "connected_success"}); err != nil {
			testServer.errors <- err
			return
		}

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			copied := append([]byte(nil), data...)
			testServer.messages <- copied

			var msg taskEventRequest
			if err := json.Unmarshal(data, &msg); err != nil {
				testServer.errors <- err
				continue
			}

			switch msg.Event {
			case "task_start":
				if err := conn.WriteJSON(taskEventResponse{Event: "task_started"}); err != nil {
					testServer.errors <- err
					return
				}
			case "task_continue":
				testServer.continueN++
				audioData, err := base64.StdEncoding.DecodeString(smallMP3Base64)
				if err != nil {
					testServer.errors <- err
					return
				}
				audioHex := hex.EncodeToString(audioData)
				if testServer.invalidAudio {
					audioHex = "not-hex"
				}
				if err := conn.WriteJSON(taskEventResponse{
					Event:   "task_continued",
					Data:    &responseData{Audio: audioHex},
					IsFinal: true,
				}); err != nil {
					testServer.errors <- err
					return
				}
			case "task_finish":
				if err := conn.WriteJSON(taskEventResponse{Event: "task_finished"}); err != nil {
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

func (s *minimaxTestServer) close() {
	s.server.Close()
}

func (s *minimaxTestServer) nextMessage() []byte {
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

func requireEvent(t *testing.T, events <-chan *tts.Event, eventType tts.EventType, segmentID string) *tts.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("events channel closed")
		}
		if event.Type != eventType || event.SegmentID != segmentID {
			t.Fatalf("event = %#v, want type=%s segment_id=%s", event, eventType, segmentID)
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %s event", eventType)
	}
	return nil
}

func requireSegmentEndAfterAudio(t *testing.T, events <-chan *tts.Event, segmentID string) {
	t.Helper()
	for {
		event := requireEventForSegment(t, events, segmentID)
		switch event.Type {
		case tts.EventAudioFrame:
			if event.Audio == nil {
				t.Fatal("extra audio frame is nil")
			}
			if event.Audio.Codec != audio.CodecPCM {
				t.Fatalf("extra audio codec = %q, want pcm", event.Audio.Codec)
			}
		case tts.EventSegmentEnd:
			return
		default:
			t.Fatalf("event = %#v, want audio_frame or segment_end for %s", event, segmentID)
		}
	}
}

func requireEventForSegment(t *testing.T, events <-chan *tts.Event, segmentID string) *tts.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("events channel closed")
		}
		if event.SegmentID != segmentID {
			t.Fatalf("event = %#v, want segment_id=%s", event, segmentID)
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for event for %s", segmentID)
	}
	return nil
}

const smallMP3Base64 = "SUQzBAAAAAAAI1RTU0UAAAAPAAADTGF2ZjU3LjcxLjEwMAAAAAAAAAAAAAAA//NgxAAdI/3kAUMYAAAAKu7uBgAAIREREd3d3dwMAAABOuaAYt+J/+iIhaIiIiJ/u7u5//9cAEJ/6O7u7/u7u5/+7ufEAwN3f0R3d3d3f//9E///93d+u7u7v//ERHf93c/0L9Hd3d3d0LiIiF/7u7l/+iAYGBu7vo7u/9cAEIGJdRkMtpsbBo9D6hoNBqLv8AvDJXXo/zsRNehi//NixBol6r7uX5iRIv+EFoA4bcpBaYG6ga2BL2SIo+AVYlMcZOMp1IGgYnGTL4nwvldMsp9qAYkFwIsmeZjRO2wXMCdDwgGKQHgn16Rmmh/z6CBTPidyDkTLRw7oOm57/+QMiZ43UggmYl9yDl9lM1fqTf//zcvl963LjKOKBILmjDU3f/Wb/9xQwmq28GRTlt2zWsJJBugJoak/BP/zYsQSJHNW2j/PWALsLp9JKVJlM25CqLiqfiEy6tQMD7eB4TdFplR6HFY7TpajY2rE1EdBci2qfLbuHOduci2WnWy6LbJq1rVWu3Q69zG1C0tb/CZ2aYrTrzznf///3DnLmzk4QQtKF1N0HFta+recr+pmnXbuIdMV////+yrZWxlzT7WmJpb/9QGfrorHZUFqOW6qYbUDJos="
