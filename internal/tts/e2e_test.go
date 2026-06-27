package tts_test

import (
	"context"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/provider/mock"
	"github.com/caitunai/tts/internal/tts"
)

func TestSynthesizeOncePCMEndToEnd(t *testing.T) {
	registry := registryprovider.NewRegistry()
	if err := registry.Register(mock.NewPCMProvider("pcm", []byte{1, 2, 3})); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	service := tts.NewService("test", registry)

	events, err := service.SynthesizeOnce(context.Background(), &tts.SynthesizeRequest{
		RequestID: "req_pcm",
		Provider:  "pcm",
		Text:      "hello",
		Output:    audio.DefaultOutputConfig(),
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectEvents(t, events)
	requireEventSequence(t, got, []tts.EventType{tts.EventSegmentStart, tts.EventAudioFrame, tts.EventSegmentEnd})
	if got[1].Audio == nil {
		t.Fatal("audio event has nil frame")
	}
	if got[1].Audio.Codec != audio.CodecPCM {
		t.Fatalf("audio codec = %q, want %q", got[1].Audio.Codec, audio.CodecPCM)
	}
	if len(got[1].Audio.Data) != 640 {
		t.Fatalf("audio frame length = %d, want 640", len(got[1].Audio.Data))
	}
	if !got[1].Audio.SegmentFinal {
		t.Fatal("audio frame SegmentFinal = false, want true")
	}
}

func TestSynthesizeOnceOggOpusEndToEnd(t *testing.T) {
	registry := registryprovider.NewRegistry()
	if err := registry.Register(mock.NewOggOpusProvider("opus", []byte("raw-opus-packet"))); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	service := tts.NewService("test", registry)

	events, err := service.SynthesizeOnce(context.Background(), &tts.SynthesizeRequest{
		RequestID: "req_opus",
		Provider:  "opus",
		Text:      "hello",
		Output: audio.OutputConfig{
			PreferCodec: audio.CodecOpus,
			SampleRate:  48000,
			Channels:    1,
			FrameMS:     20,
		},
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectEvents(t, events)
	requireEventSequence(t, got, []tts.EventType{tts.EventSegmentStart, tts.EventAudioFrame, tts.EventSegmentEnd})
	if got[1].Audio == nil {
		t.Fatal("audio event has nil frame")
	}
	if got[1].Audio.Codec != audio.CodecOpus {
		t.Fatalf("audio codec = %q, want %q", got[1].Audio.Codec, audio.CodecOpus)
	}
	if got[1].Audio.Container != audio.ContainerRaw {
		t.Fatalf("audio container = %q, want %q", got[1].Audio.Container, audio.ContainerRaw)
	}
	if string(got[1].Audio.Data) != "raw-opus-packet" {
		t.Fatalf("audio data = %q, want raw-opus-packet", string(got[1].Audio.Data))
	}
}

func TestSessionAppendTextEndToEnd(t *testing.T) {
	registry := registryprovider.NewRegistry()
	if err := registry.Register(mock.NewSessionProvider("session", []byte{1, 2, 3})); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	service := tts.NewService("test", registry)

	session, err := service.OpenSession(context.Background(), &tts.OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "session",
		Output:    audio.DefaultOutputConfig(),
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	events := session.Events()
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{SegmentID: "seg_1", Text: "one"}); err != nil {
		t.Fatalf("append seg_1: %v", err)
	}
	if err := session.AppendText(context.Background(), &tts.SegmentRequest{SegmentID: "seg_2", Text: "two"}); err != nil {
		t.Fatalf("append seg_2: %v", err)
	}
	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("finish: %v", err)
	}

	got := collectEvents(t, events)
	requireEventSequence(t, got, []tts.EventType{
		tts.EventSegmentStart,
		tts.EventAudioFrame,
		tts.EventSegmentEnd,
		tts.EventSegmentStart,
		tts.EventAudioFrame,
		tts.EventSegmentEnd,
		tts.EventSessionEnd,
	})
	if got[0].SegmentID != "seg_1" || got[3].SegmentID != "seg_2" {
		t.Fatalf("segment order = %q then %q, want seg_1 then seg_2", got[0].SegmentID, got[3].SegmentID)
	}
}

func TestAdvancedInputsEndToEnd(t *testing.T) {
	provider := mock.NewAdvancedInputProvider("advanced")
	registry := registryprovider.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	service := tts.NewService("test", registry)

	events, err := service.SynthesizeOnce(context.Background(), &tts.SynthesizeRequest{
		RequestID:    "req_advanced",
		Provider:     "advanced",
		Text:         "hello",
		GuidanceText: "warm narration",
		ReferenceAudio: &tts.ReferenceAudio{
			Codec:     audio.CodecWAV,
			Container: audio.ContainerWAV,
			Data:      []byte("wav"),
		},
		Output: audio.DefaultOutputConfig(),
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}
	_ = collectEvents(t, events)

	req := provider.LastSynthesizeRequest()
	if req == nil {
		t.Fatal("provider did not record synthesize request")
	}
	if req.GuidanceText != "warm narration" {
		t.Fatalf("GuidanceText = %q, want warm narration", req.GuidanceText)
	}
	if req.ReferenceAudio == nil {
		t.Fatal("ReferenceAudio is nil")
	}
}

func TestProviderErrorEndToEnd(t *testing.T) {
	registry := registryprovider.NewRegistry()
	if err := registry.Register(mock.NewErrorProvider("error", &tts.Error{
		Code:     tts.ErrProviderTimeout,
		Provider: "error",
		Message:  "timeout",
	})); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	service := tts.NewService("test", registry)

	_, err := service.SynthesizeOnce(context.Background(), &tts.SynthesizeRequest{
		Provider: "error",
		Text:     "hello",
	})
	if err == nil {
		t.Fatal("expected provider error")
	}
	ttsErr, ok := err.(*tts.Error)
	if !ok {
		t.Fatalf("error = %T, want *tts.Error", err)
	}
	if ttsErr.Code != tts.ErrProviderTimeout {
		t.Fatalf("error code = %q, want %q", ttsErr.Code, tts.ErrProviderTimeout)
	}
}

func collectEvents(t *testing.T, events <-chan *tts.Event) []*tts.Event {
	t.Helper()

	var got []*tts.Event
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, event)
		case <-timeout:
			t.Fatalf("timed out waiting for event channel to close; got %d events", len(got))
		}
	}
}

func requireEventSequence(t *testing.T, events []*tts.Event, want []tts.EventType) {
	t.Helper()

	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(want), events)
	}
	for i := range want {
		if events[i].Type != want[i] {
			t.Fatalf("event[%d] type = %q, want %q", i, events[i].Type, want[i])
		}
	}
}
