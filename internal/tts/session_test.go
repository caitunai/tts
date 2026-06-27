package tts

import (
	"context"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/audio"
)

func TestSessionAppendTextUsesFIFO(t *testing.T) {
	providerSession := newControlledProviderSession("sess")
	session := newProviderBackedSession("mock", audio.OutputConfig{}, providerSession)

	events := session.Events()

	if err := session.AppendText(context.Background(), &SegmentRequest{SegmentID: "seg_1", Text: "one"}); err != nil {
		t.Fatalf("append seg_1: %v", err)
	}
	if err := session.AppendText(context.Background(), &SegmentRequest{SegmentID: "seg_2", Text: "two"}); err != nil {
		t.Fatalf("append seg_2: %v", err)
	}

	providerSession.requireSent(t, "seg_1")
	providerSession.requireNoExtraSent(t)

	providerSession.emit(&ProviderEvent{Type: ProviderEventSegmentStart, SessionID: "sess", SegmentID: "seg_1"})
	requireEvent(t, events, EventSegmentStart, "seg_1")

	providerSession.emit(&ProviderEvent{Type: ProviderEventSegmentEnd, SessionID: "sess", SegmentID: "seg_1"})
	requireEvent(t, events, EventSegmentEnd, "seg_1")
	providerSession.requireSent(t, "seg_2")

	providerSession.emit(&ProviderEvent{Type: ProviderEventSegmentStart, SessionID: "sess", SegmentID: "seg_2"})
	requireEvent(t, events, EventSegmentStart, "seg_2")
}

func TestSessionFinishWaitsForQueuedSegments(t *testing.T) {
	providerSession := newControlledProviderSession("sess")
	session := newProviderBackedSession("mock", audio.OutputConfig{}, providerSession)
	events := session.Events()

	if err := session.AppendText(context.Background(), &SegmentRequest{SegmentID: "seg_1"}); err != nil {
		t.Fatalf("append seg_1: %v", err)
	}
	if err := session.AppendText(context.Background(), &SegmentRequest{SegmentID: "seg_2"}); err != nil {
		t.Fatalf("append seg_2: %v", err)
	}
	providerSession.requireSent(t, "seg_1")

	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("finish: %v", err)
	}
	providerSession.requireNotFinished(t)

	providerSession.emit(&ProviderEvent{Type: ProviderEventSegmentEnd, SessionID: "sess", SegmentID: "seg_1"})
	requireEvent(t, events, EventSegmentEnd, "seg_1")
	providerSession.requireSent(t, "seg_2")
	providerSession.requireNotFinished(t)

	providerSession.emit(&ProviderEvent{Type: ProviderEventSegmentEnd, SessionID: "sess", SegmentID: "seg_2"})
	requireEvent(t, events, EventSegmentEnd, "seg_2")
	providerSession.requireFinished(t)
}

func TestSessionRejectsAppendAfterFinish(t *testing.T) {
	providerSession := newControlledProviderSession("sess")
	session := newProviderBackedSession("mock", audio.OutputConfig{}, providerSession)

	if err := session.Finish(context.Background()); err != nil {
		t.Fatalf("finish: %v", err)
	}

	err := session.AppendText(context.Background(), &SegmentRequest{SegmentID: "seg_1"})
	requireTTSErrorCode(t, err, ErrSessionClosed)
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	providerSession := newControlledProviderSession("sess")
	session := newProviderBackedSession("mock", audio.OutputConfig{}, providerSession)

	if err := session.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if providerSession.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", providerSession.closeCalls)
	}
}

func TestSessionEventsCloseOnSessionEnd(t *testing.T) {
	providerSession := newControlledProviderSession("sess")
	session := newProviderBackedSession("mock", audio.OutputConfig{}, providerSession)
	events := session.Events()

	providerSession.emit(&ProviderEvent{Type: ProviderEventSessionEnd, SessionID: "sess"})
	requireEvent(t, events, EventSessionEnd, "")

	if event, ok := <-events; ok {
		t.Fatalf("event channel still open, got %#v", event)
	}
}

type controlledProviderSession struct {
	id string

	events   chan *ProviderEvent
	sent     chan *ProviderSegmentRequest
	finished chan struct{}

	finishCalls int
	closeCalls  int
}

func newControlledProviderSession(id string) *controlledProviderSession {
	return &controlledProviderSession{
		id:       id,
		events:   make(chan *ProviderEvent, 16),
		sent:     make(chan *ProviderSegmentRequest, 16),
		finished: make(chan struct{}, 1),
	}
}

func (s *controlledProviderSession) ID() string {
	return s.id
}

func (s *controlledProviderSession) AppendText(_ context.Context, segment *ProviderSegmentRequest) error {
	s.sent <- segment
	return nil
}

func (s *controlledProviderSession) Finish(context.Context) error {
	s.finishCalls++
	s.finished <- struct{}{}
	return nil
}

func (s *controlledProviderSession) Events() <-chan *ProviderEvent {
	return s.events
}

func (s *controlledProviderSession) Close() error {
	s.closeCalls++
	close(s.events)
	return nil
}

func (s *controlledProviderSession) emit(event *ProviderEvent) {
	s.events <- event
}

func (s *controlledProviderSession) requireSent(t *testing.T, segmentID string) {
	t.Helper()

	select {
	case segment := <-s.sent:
		if segment.SegmentID != segmentID {
			t.Fatalf("sent segment = %q, want %q", segment.SegmentID, segmentID)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected sent segment %q", segmentID)
	}
}

func (s *controlledProviderSession) requireNoExtraSent(t *testing.T) {
	t.Helper()

	select {
	case segment := <-s.sent:
		t.Fatalf("unexpected sent segment %q", segment.SegmentID)
	default:
	}
}

func (s *controlledProviderSession) requireFinished(t *testing.T) {
	t.Helper()

	select {
	case <-s.finished:
	case <-time.After(time.Second):
		t.Fatal("expected provider session to finish")
	}
	if s.finishCalls != 1 {
		t.Fatalf("finish calls = %d, want 1", s.finishCalls)
	}
}

func (s *controlledProviderSession) requireNotFinished(t *testing.T) {
	t.Helper()

	if s.finishCalls != 0 {
		t.Fatalf("finish calls = %d, want 0", s.finishCalls)
	}
}

func requireEvent(t *testing.T, events <-chan *Event, eventType EventType, segmentID string) {
	t.Helper()

	event, ok := <-events
	if !ok {
		t.Fatalf("event channel closed before %s", eventType)
	}
	if event.Type != eventType {
		t.Fatalf("event type = %q, want %q", event.Type, eventType)
	}
	if event.SegmentID != segmentID {
		t.Fatalf("segment id = %q, want %q", event.SegmentID, segmentID)
	}
}
