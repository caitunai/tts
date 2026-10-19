package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

type fakeSession struct {
	events   chan *tts.ProviderEvent
	done     chan struct{}
	once     sync.Once
	appends  atomic.Int32
	finishes atomic.Int32
}

func (*fakeSession) ID() string                          { return "session" }
func (*fakeSession) SupportsPipelinedText() bool         { return true }
func (s *fakeSession) Events() <-chan *tts.ProviderEvent { return s.events }
func (s *fakeSession) AppendText(_ context.Context, req *tts.ProviderSegmentRequest) error {
	s.appends.Add(1)
	s.events <- &tts.ProviderEvent{Type: tts.ProviderEventSegmentStart, SegmentID: req.SegmentID}
	return nil
}
func (s *fakeSession) Finish(context.Context) error { s.finishes.Add(1); return nil }
func (s *fakeSession) Close() error                 { s.once.Do(func() { close(s.done) }); return nil }

func testSession(t *testing.T, ctx context.Context, cfg tts.TimeoutConfig) (*guardedSession, *fakeSession) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	cfg, err := Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	retry, _ := ResolveReconnect(tts.ReconnectConfig{MaxAttempts: 1})
	conn, _, err := Dial(ctx, nil, "ws"+strings.TrimPrefix(server.URL, "http"), nil, cfg, retry)
	if err != nil {
		t.Fatal(err)
	}
	inner := &fakeSession{events: make(chan *tts.ProviderEvent, 64), done: make(chan struct{})}
	session := GuardSession(ctx, inner, conn, "provider", cfg).(*guardedSession)
	t.Cleanup(func() { _ = session.Close() })
	return session, inner
}

func nextEvent(t *testing.T, session *guardedSession) *tts.ProviderEvent {
	t.Helper()
	select {
	case event, ok := <-session.Events():
		if !ok {
			t.Fatal("events closed unexpectedly")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return nil
	}
}

func requireClosed(t *testing.T, session *guardedSession) {
	t.Helper()
	select {
	case event, ok := <-session.Events():
		if ok {
			t.Fatalf("unexpected event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("event producer did not close")
	}
}

func TestSegmentTimeoutDoesNotReplayText(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "before audio"
		if partial {
			name = "partial audio"
		}
		t.Run(name, func(t *testing.T) {
			session, inner := testSession(t, t.Context(), tts.TimeoutConfig{SegmentTimeout: 40 * time.Millisecond})
			if !session.SupportsPipelinedText() {
				t.Fatal("pipelined text capability lost")
			}
			if err := session.AppendText(t.Context(), &tts.ProviderSegmentRequest{SegmentID: "segment", Text: "hello"}); err != nil {
				t.Fatal(err)
			}
			_ = nextEvent(t, session)
			if partial {
				inner.events <- &tts.ProviderEvent{Type: tts.ProviderEventAudio, SegmentID: "segment", Audio: &tts.ProviderAudioChunk{Data: []byte{1}}}
				_ = nextEvent(t, session)
			}
			event := nextEvent(t, session)
			if event.Type != tts.ProviderEventError || event.Error.Code != tts.ErrProviderTimeout {
				t.Fatalf("event = %#v", event)
			}
			if event.Error.PartialAudio != partial || !event.Error.Reconnectable || event.Error.Retryable == partial {
				t.Fatalf("failure = %#v", event.Error)
			}
			requireClosed(t, session)
			if err := session.AppendText(t.Context(), &tts.ProviderSegmentRequest{Text: "do not resend"}); err == nil {
				t.Fatal("closed session accepted text")
			}
			if inner.appends.Load() != 1 {
				t.Fatalf("append calls = %d, want 1", inner.appends.Load())
			}
			select {
			case <-inner.done:
			default:
				t.Fatal("underlying session was not closed")
			}
		})
	}
}

func TestFinishTimeoutWaitsUntilPendingSegmentEnds(t *testing.T) {
	session, inner := testSession(t, t.Context(), tts.TimeoutConfig{SegmentTimeout: 300 * time.Millisecond, FinishTimeout: 20 * time.Millisecond})
	if err := session.AppendText(t.Context(), &tts.ProviderSegmentRequest{SegmentID: "segment", Text: "hello", IsLast: true}); err != nil {
		t.Fatal(err)
	}
	_ = nextEvent(t, session)
	if err := session.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	select {
	case event := <-session.Events():
		t.Fatalf("finished active synthesis early: %#v", event)
	default:
	}
	inner.events <- &tts.ProviderEvent{Type: tts.ProviderEventSegmentEnd, SegmentID: "segment"}
	if got := nextEvent(t, session); got.Type != tts.ProviderEventSegmentEnd {
		t.Fatalf("event = %#v", got)
	}
	if got := nextEvent(t, session); got.Type != tts.ProviderEventError || got.Error.Code != tts.ErrProviderTimeout {
		t.Fatalf("event = %#v", got)
	}
	requireClosed(t, session)
}

func TestSegmentTimeoutIsNotResetByAudio(t *testing.T) {
	session, inner := testSession(t, t.Context(), tts.TimeoutConfig{SegmentTimeout: 60 * time.Millisecond})
	if err := session.AppendText(t.Context(), &tts.ProviderSegmentRequest{SegmentID: "segment", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	_ = nextEvent(t, session)
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-inner.done:
				return
			case <-ticker.C:
				select {
				case <-inner.done:
					return
				case inner.events <- &tts.ProviderEvent{Type: tts.ProviderEventAudio, SegmentID: "segment", Audio: &tts.ProviderAudioChunk{Data: []byte{1}}}:
				}
			}
		}
	}()
	for {
		event := nextEvent(t, session)
		if event.Type == tts.ProviderEventError {
			if event.Error.Code != tts.ErrProviderTimeout || !event.Error.PartialAudio {
				t.Fatalf("failure = %#v", event.Error)
			}
			requireClosed(t, session)
			return
		}
	}
}

func TestCompletedSessionFinishIsIdempotent(t *testing.T) {
	session, inner := testSession(t, t.Context(), tts.TimeoutConfig{SegmentTimeout: 30 * time.Millisecond})
	if err := session.AppendText(t.Context(), &tts.ProviderSegmentRequest{SegmentID: "segment", Text: "hello", IsLast: true}); err != nil {
		t.Fatal(err)
	}
	_ = nextEvent(t, session)
	inner.events <- &tts.ProviderEvent{Type: tts.ProviderEventSegmentEnd, SegmentID: "segment"}
	inner.events <- &tts.ProviderEvent{Type: tts.ProviderEventSessionEnd}
	_ = nextEvent(t, session)
	if got := nextEvent(t, session); got.Type != tts.ProviderEventSessionEnd {
		t.Fatalf("event = %#v", got)
	}
	requireClosed(t, session)
	if err := session.Finish(t.Context()); err != nil {
		t.Fatalf("Finish after success: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.terminal != nil || len(session.pending) != 0 {
		t.Fatal("completion timers were not cleared")
	}
}

func TestPolicyViolationDoesNotRecommendReconnect(t *testing.T) {
	session, inner := testSession(t, t.Context(), tts.TimeoutConfig{})
	inner.events <- &tts.ProviderEvent{Type: tts.ProviderEventError, Error: &tts.Error{
		Code: tts.ErrProviderUnavailable, Retryable: true, Cause: &websocket.CloseError{Code: websocket.ClosePolicyViolation},
	}}
	if got := nextEvent(t, session); got.Error.Reconnectable {
		t.Fatalf("policy error = %#v", got.Error)
	}
	requireClosed(t, session)
}

func TestSessionCancellationInterruptsBlockedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session, inner := testSession(t, ctx, tts.TimeoutConfig{})
	for range 40 {
		inner.events <- &tts.ProviderEvent{Type: tts.ProviderEventSessionStart}
	}
	deadline := time.Now().Add(time.Second)
	for len(session.events) != cap(session.events) {
		if time.Now().After(deadline) {
			t.Fatal("output did not fill")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-inner.done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close underlying session")
	}
	for range len(session.events) {
		<-session.events
	}
	requireClosed(t, session)
}
