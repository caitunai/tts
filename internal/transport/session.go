package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"uuid"

	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

// GuardSession bounds synthesis completion independently of network activity:
// upstream heartbeats must not keep an unfinished segment alive forever.
func GuardSession(ctx context.Context, inner tts.ProviderSession, conn *websocket.Conn, provider string, cfg tts.TimeoutConfig) tts.ProviderSession {
	_ = conn.NetConn().SetReadDeadline(time.Time{})
	s := &guardedSession{inner: inner, conn: conn, provider: provider, cfg: cfg,
		pending: make(map[string]*pendingSegment), events: make(chan *tts.ProviderEvent, 32),
		done: make(chan struct{}), failures: make(chan *tts.Error, 1)}
	go s.forward(ctx)
	return s
}

type pendingSegment struct {
	timer *time.Timer
	audio bool
}

type guardedSession struct {
	inner       tts.ProviderSession
	conn        *websocket.Conn
	provider    string
	cfg         tts.TimeoutConfig
	mu          sync.Mutex
	pending     map[string]*pendingSegment
	order       []string
	finishing   bool
	completed   bool
	finishTimer *time.Timer
	terminal    *tts.Error
	closed      bool
	closeOnce   sync.Once
	events      chan *tts.ProviderEvent
	done        chan struct{}
	failures    chan *tts.Error
}

func (s *guardedSession) ID() string                        { return s.inner.ID() }
func (s *guardedSession) Events() <-chan *tts.ProviderEvent { return s.events }
func (s *guardedSession) SupportsPipelinedText() bool {
	stream, ok := s.inner.(tts.ProviderTextStreamSession)
	return ok && stream.SupportsPipelinedText()
}

func (s *guardedSession) AppendText(ctx context.Context, req *tts.ProviderSegmentRequest) error {
	if req == nil {
		return s.inner.AppendText(ctx, nil)
	}
	copy := *req
	if copy.SegmentID == "" {
		copy.SegmentID = uuid.New().String()
	}
	s.mu.Lock()
	if s.closed || s.terminal != nil || s.finishing {
		err := s.closedErrorLocked()
		s.mu.Unlock()
		return err
	}
	if _, exists := s.pending[copy.SegmentID]; exists {
		s.mu.Unlock()
		return &tts.Error{Code: tts.ErrSegmentFailed, Provider: s.provider, SessionID: s.ID(), SegmentID: copy.SegmentID, Message: "segment is already pending"}
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return err
	}
	pending := &pendingSegment{}
	s.pending[copy.SegmentID] = pending
	s.order = append(s.order, copy.SegmentID)
	s.finishing = copy.IsLast
	pending.timer = time.AfterFunc(s.cfg.SegmentTimeout, func() {
		s.mu.Lock()
		if s.pending[copy.SegmentID] != pending || s.closed || s.terminal != nil {
			s.mu.Unlock()
			return
		}
		err := s.timeoutLocked(copy.SegmentID, "segment completion")
		s.terminal = err
		s.mu.Unlock()
		s.signalFailure(err)
	})
	s.mu.Unlock()
	if err := s.inner.AppendText(ctx, &copy); err != nil {
		failure := s.networkError(err, copy.SegmentID)
		s.fail(failure)
		return failure
	}
	return nil
}

func (s *guardedSession) Finish(ctx context.Context) error {
	s.mu.Lock()
	if s.completed {
		s.mu.Unlock()
		return nil
	}
	if s.closed || s.terminal != nil {
		err := s.closedErrorLocked()
		s.mu.Unlock()
		return err
	}
	s.finishing = true
	s.armFinishLocked()
	s.mu.Unlock()
	if err := s.inner.Finish(ctx); err != nil {
		failure := s.networkError(err, "")
		s.fail(failure)
		return failure
	}
	return nil
}

func (s *guardedSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		for _, segment := range s.pending {
			segment.timer.Stop()
		}
		clear(s.pending)
		s.order = nil
		if s.finishTimer != nil {
			s.finishTimer.Stop()
		}
		s.mu.Unlock()
		close(s.done)
		// Close the socket before provider cleanup: a blocked writer/reader
		// must not prevent cleanup from acquiring the provider's locks.
		_ = s.conn.Close()
		err = s.inner.Close()
	})
	return err
}

func (s *guardedSession) fail(err *tts.Error) {
	s.mu.Lock()
	if s.closed || s.terminal != nil {
		s.mu.Unlock()
		return
	}
	s.terminal = err
	s.mu.Unlock()
	s.signalFailure(err)
}

func (s *guardedSession) signalFailure(err *tts.Error) {
	s.failures <- err
	_ = s.conn.Close()
}

func (s *guardedSession) forward(ctx context.Context) {
	defer close(s.events)
	defer s.Close()
	upstream := s.inner.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case failure := <-s.failures:
			s.emit(ctx, &tts.ProviderEvent{Type: tts.ProviderEventError, SegmentID: failure.SegmentID, Error: failure})
			return
		case event, ok := <-upstream:
			if !ok {
				s.mu.Lock()
				failure := s.terminal
				if failure == nil {
					failure = &tts.Error{Code: tts.ErrProviderUnavailable, Provider: s.provider, SessionID: s.ID(),
						Message: "websocket event stream closed before session_end", Reconnectable: true}
					failure = s.annotateLocked(failure, s.firstPendingLocked())
				}
				s.mu.Unlock()
				s.emit(ctx, &tts.ProviderEvent{Type: tts.ProviderEventError, SegmentID: failure.SegmentID, Error: failure})
				return
			}
			if event == nil {
				continue
			}
			copy := *event
			s.mu.Lock()
			switch copy.Type {
			case tts.ProviderEventAudio:
				id := copy.SegmentID
				if id == "" {
					id = s.firstPendingLocked()
				}
				if segment := s.pending[id]; segment != nil && copy.Audio != nil && len(copy.Audio.Data) > 0 {
					segment.audio = true
				}
			case tts.ProviderEventSegmentEnd:
				if segment := s.pending[copy.SegmentID]; segment != nil {
					segment.timer.Stop()
					delete(s.pending, copy.SegmentID)
					for i, id := range s.order {
						if id == copy.SegmentID {
							s.order = append(s.order[:i], s.order[i+1:]...)
							break
						}
					}
				}
				s.armFinishLocked()
			case tts.ProviderEventError:
				if s.terminal != nil {
					copy.Error = s.terminal
				} else {
					if copy.Error == nil {
						copy.Error = &tts.Error{Code: tts.ErrProviderUnavailable, Message: "websocket provider failed"}
					}
					reconnectable := canReconnect(copy.Error)
					copy.Error = s.annotateLocked(tts.NormalizeError(copy.Error), copy.SegmentID)
					copy.Error.Reconnectable = reconnectable
				}
				copy.SegmentID = copy.Error.SegmentID
			case tts.ProviderEventSessionEnd:
				s.completed = true
			}
			s.mu.Unlock()
			if !s.emit(ctx, &copy) {
				return
			}
			if copy.Type == tts.ProviderEventError || copy.Type == tts.ProviderEventSessionEnd {
				return
			}
		}
	}
}

func (s *guardedSession) emit(ctx context.Context, event *tts.ProviderEvent) bool {
	event.Provider, event.SessionID = s.provider, s.ID()
	select {
	case <-ctx.Done():
		return false
	case <-s.done:
		return false
	case s.events <- event:
		return true
	}
}

func (s *guardedSession) armFinishLocked() {
	if !s.finishing || len(s.pending) != 0 || s.finishTimer != nil {
		return
	}
	s.finishTimer = time.AfterFunc(s.cfg.FinishTimeout, func() {
		s.mu.Lock()
		if s.closed || s.terminal != nil {
			s.mu.Unlock()
			return
		}
		err := s.timeoutLocked("", "session finish")
		s.terminal = err
		s.mu.Unlock()
		s.signalFailure(err)
	})
}

func (s *guardedSession) firstPendingLocked() string {
	if len(s.order) != 0 {
		return s.order[0]
	}
	return ""
}

func (s *guardedSession) annotateLocked(err *tts.Error, segmentID string) *tts.Error {
	copy := *err
	copy.Provider, copy.SessionID = s.provider, s.ID()
	if segmentID == "" {
		segmentID = s.firstPendingLocked()
	}
	copy.SegmentID = segmentID
	if segment := s.pending[segmentID]; segment != nil {
		copy.PartialAudio = segment.audio
	}
	if copy.PartialAudio {
		copy.Retryable = false
	}
	return &copy
}

func (s *guardedSession) timeoutLocked(id, operation string) *tts.Error {
	return s.annotateLocked(&tts.Error{Code: tts.ErrProviderTimeout,
		Message: fmt.Sprintf("%s: websocket %s timed out", s.provider, operation),
		Cause:   context.DeadlineExceeded, Retryable: true, Reconnectable: true}, id)
}

func (s *guardedSession) closedErrorLocked() error {
	if s.terminal != nil {
		return s.terminal
	}
	return &tts.Error{Code: tts.ErrSessionClosed, Provider: s.provider, SessionID: s.ID(), Message: "websocket session is closed; open a new session"}
}

func (s *guardedSession) networkError(err error, segmentID string) *tts.Error {
	failure, ok := errors.AsType[*tts.Error](err)
	if !ok {
		failure = &tts.Error{Code: tts.ErrProviderUnavailable, Message: err.Error(), Cause: err, Retryable: true}
	}
	failure = tts.NormalizeError(failure)
	reconnectable := canReconnect(failure) && !errors.Is(err, context.Canceled)
	s.mu.Lock()
	defer s.mu.Unlock()
	failure = s.annotateLocked(failure, segmentID)
	failure.Reconnectable = reconnectable
	return failure
}

func canReconnect(err *tts.Error) bool {
	if closeErr, ok := errors.AsType[*websocket.CloseError](err); ok {
		switch closeErr.Code {
		case websocket.CloseProtocolError, websocket.CloseUnsupportedData, websocket.CloseInvalidFramePayloadData,
			websocket.ClosePolicyViolation, websocket.CloseMessageTooBig:
			return false
		}
	}
	normalized := tts.NormalizeError(err)
	return normalized.Code == tts.ErrProviderTimeout || normalized.Code == tts.ErrProviderUnavailable && normalized.Retryable
}
