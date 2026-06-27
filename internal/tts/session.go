package tts

import (
	"context"
	"sync"

	"github.com/caitunai/tts/internal/audio"
)

const defaultEventBufferSize = 16

type providerBackedSession struct {
	providerName string
	output       audio.OutputConfig
	provider     ProviderSession

	mu        sync.Mutex
	state     SessionState
	segments  map[string]SegmentState
	activeID  string
	queue     []*SegmentRequest
	finishing bool
	closed    bool

	eventsOnce sync.Once
	events     chan *Event

	closeOnce sync.Once
	done      chan struct{}
}

func newProviderBackedSession(providerName string, output audio.OutputConfig, provider ProviderSession) Session {
	return &providerBackedSession{
		providerName: providerName,
		output:       output,
		provider:     provider,
		state:        SessionStateReady,
		segments:     make(map[string]SegmentState),
		done:         make(chan struct{}),
	}
}

func (s *providerBackedSession) ID() string {
	if s.provider == nil {
		return ""
	}
	return s.provider.ID()
}

func (s *providerBackedSession) ProviderName() string {
	return s.providerName
}

func (s *providerBackedSession) Output() audio.OutputConfig {
	return s.output
}

func (s *providerBackedSession) AppendText(ctx context.Context, segment *SegmentRequest) error {
	if s.provider == nil {
		return internalError("provider session is nil")
	}
	if segment == nil {
		return internalError("segment request is nil")
	}

	segmentCopy := copySegmentRequest(segment)

	s.mu.Lock()
	if s.closed || s.finishing {
		s.mu.Unlock()
		return sessionClosedError(s.providerName, s.ID())
	}

	if s.activeID == "" && len(s.queue) == 0 {
		s.activeID = segmentCopy.SegmentID
		s.state = SessionStateSynthesizing
		s.segments[segmentCopy.SegmentID] = SegmentStateSentToProvider
		s.mu.Unlock()

		if err := s.provider.AppendText(ctx, providerSegmentFromSegment(segmentCopy)); err != nil {
			s.markActiveFailed(segmentCopy.SegmentID)
			return err
		}
		return nil
	}

	s.queue = append(s.queue, segmentCopy)
	s.segments[segmentCopy.SegmentID] = SegmentStatePending
	s.mu.Unlock()
	return nil
}

func (s *providerBackedSession) Finish(ctx context.Context) error {
	if s.provider == nil {
		return internalError("provider session is nil")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return sessionClosedError(s.providerName, s.ID())
	}
	if s.finishing {
		s.mu.Unlock()
		return nil
	}

	s.finishing = true
	shouldFinish := s.activeID == "" && len(s.queue) == 0
	if shouldFinish {
		s.state = SessionStateFinishing
	}
	s.mu.Unlock()

	if shouldFinish {
		return s.provider.Finish(ctx)
	}
	return nil
}

func (s *providerBackedSession) Events() <-chan *Event {
	s.eventsOnce.Do(func() {
		s.events = make(chan *Event, defaultEventBufferSize)
		go s.forwardEvents()
	})
	return s.events
}

func (s *providerBackedSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.state = SessionStateClosed
		s.mu.Unlock()

		close(s.done)
		if s.provider != nil {
			err = s.provider.Close()
		}
	})
	return err
}

func (s *providerBackedSession) forwardEvents() {
	defer close(s.events)

	if s.provider == nil {
		_ = s.emit(&Event{
			Type:  EventError,
			Error: internalError("provider session is nil"),
		})
		return
	}

	providerEvents := s.provider.Events()
	for {
		select {
		case <-s.done:
			return
		case event, ok := <-providerEvents:
			if !ok {
				s.markClosed()
				return
			}

			next, shouldFinish := s.applyProviderEvent(event)
			if !s.emit(providerEventToEvent(event)) {
				return
			}

			if next != nil {
				if err := s.sendQueuedSegment(context.Background(), next); err != nil {
					if !s.emit(&Event{
						Type:      EventError,
						SessionID: s.ID(),
						SegmentID: next.SegmentID,
						Error:     err,
					}) {
						return
					}
					s.markActiveFailed(next.SegmentID)
				}
				continue
			}

			if shouldFinish {
				if err := s.provider.Finish(context.Background()); err != nil {
					if !s.emit(&Event{
						Type:      EventError,
						SessionID: s.ID(),
						Error:     errorToTTSError(err, s.providerName, s.ID(), ""),
					}) {
						return
					}
				}
			}

			if event != nil && event.Type == ProviderEventSessionEnd {
				s.markClosed()
				return
			}
		}
	}
}

func (s *providerBackedSession) emit(event *Event) bool {
	select {
	case <-s.done:
		return false
	case s.events <- event:
		return true
	}
}

func (s *providerBackedSession) applyProviderEvent(event *ProviderEvent) (*SegmentRequest, bool) {
	if event == nil {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch event.Type {
	case ProviderEventSegmentStart:
		if event.SegmentID != "" {
			s.activeID = event.SegmentID
			s.segments[event.SegmentID] = SegmentStateSentToProvider
		}
		s.state = SessionStateSynthesizing
	case ProviderEventAudio:
		if event.SegmentID != "" {
			s.segments[event.SegmentID] = SegmentStateReceivingAudio
		} else if s.activeID != "" {
			s.segments[s.activeID] = SegmentStateReceivingAudio
		}
	case ProviderEventSegmentEnd:
		return s.completeActiveLocked(event.SegmentID, SegmentStateEnded)
	case ProviderEventError:
		if event.SegmentID != "" || s.activeID != "" {
			return s.completeActiveLocked(event.SegmentID, SegmentStateFailed)
		}
		s.state = SessionStateFailed
	case ProviderEventSessionEnd:
		s.state = SessionStateClosed
		s.closed = true
	}

	return nil, false
}

func (s *providerBackedSession) completeActiveLocked(segmentID string, state SegmentState) (*SegmentRequest, bool) {
	if segmentID == "" {
		segmentID = s.activeID
	}
	if segmentID != "" {
		s.segments[segmentID] = state
	}
	if segmentID == "" || segmentID == s.activeID {
		s.activeID = ""
	}

	if len(s.queue) > 0 {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.activeID = next.SegmentID
		s.segments[next.SegmentID] = SegmentStateSentToProvider
		s.state = SessionStateSynthesizing
		return next, false
	}

	if s.finishing {
		s.state = SessionStateFinishing
		return nil, true
	}

	s.state = SessionStateReady
	return nil, false
}

func (s *providerBackedSession) sendQueuedSegment(ctx context.Context, segment *SegmentRequest) *Error {
	if s.provider == nil {
		return internalError("provider session is nil")
	}
	if err := s.provider.AppendText(ctx, providerSegmentFromSegment(segment)); err != nil {
		if ttsErr, ok := err.(*Error); ok {
			return ttsErr
		}
		return &Error{
			Code:      ErrSegmentFailed,
			Message:   err.Error(),
			Provider:  s.providerName,
			SessionID: s.ID(),
			SegmentID: segment.SegmentID,
			Cause:     err,
		}
	}
	return nil
}

func (s *providerBackedSession) markActiveFailed(segmentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if segmentID != "" {
		s.segments[segmentID] = SegmentStateFailed
	}
	if segmentID == "" || segmentID == s.activeID {
		s.activeID = ""
	}
	s.state = SessionStateFailed
}

func (s *providerBackedSession) markClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	s.state = SessionStateClosed
}

func providerEventsToEvents(providerEvents <-chan *ProviderEvent) <-chan *Event {
	events := make(chan *Event, defaultEventBufferSize)
	go func() {
		defer close(events)
		if providerEvents == nil {
			return
		}
		for event := range providerEvents {
			events <- providerEventToEvent(event)
			if event != nil && event.Type == ProviderEventSessionEnd {
				return
			}
		}
	}()
	return events
}

func providerEventToEvent(event *ProviderEvent) *Event {
	if event == nil {
		return &Event{
			Type:  EventError,
			Error: internalError("provider event is nil"),
		}
	}

	ttsEvent := &Event{
		RequestID: event.RequestID,
		SessionID: event.SessionID,
		SegmentID: event.SegmentID,
		Meta:      event.RawMeta,
		Error:     event.Error,
	}

	switch event.Type {
	case ProviderEventSessionStart:
		ttsEvent.Type = EventSessionStart
	case ProviderEventSegmentStart:
		ttsEvent.Type = EventSegmentStart
	case ProviderEventAudio:
		ttsEvent.Type = EventAudioFrame
	case ProviderEventSegmentEnd:
		ttsEvent.Type = EventSegmentEnd
	case ProviderEventSessionEnd:
		ttsEvent.Type = EventSessionEnd
	case ProviderEventError:
		ttsEvent.Type = EventError
	default:
		ttsEvent.Type = EventError
		ttsEvent.Error = internalError("unknown provider event type")
	}

	return ttsEvent
}

func copySegmentRequest(segment *SegmentRequest) *SegmentRequest {
	if segment == nil {
		return nil
	}
	copy := *segment
	return &copy
}

func providerSegmentFromSegment(segment *SegmentRequest) *ProviderSegmentRequest {
	if segment == nil {
		return nil
	}
	return &ProviderSegmentRequest{
		SegmentID:      segment.SegmentID,
		Text:           segment.Text,
		Language:       segment.Language,
		Voice:          segment.Voice,
		GuidanceText:   segment.GuidanceText,
		ReferenceAudio: segment.ReferenceAudio,
		Speed:          segment.Speed,
		Pitch:          segment.Pitch,
		Volume:         segment.Volume,
		Emotion:        segment.Emotion,
		IsLast:         segment.IsLast,
		Options:        segment.Options,
	}
}

func sessionClosedError(providerName, sessionID string) *Error {
	return &Error{
		Code:      ErrSessionClosed,
		Message:   "session is closed",
		Provider:  providerName,
		SessionID: sessionID,
	}
}

func errorToTTSError(err error, providerName, sessionID, segmentID string) *Error {
	if err == nil {
		return nil
	}
	if ttsErr, ok := err.(*Error); ok {
		return ttsErr
	}
	return &Error{
		Code:      ErrInternal,
		Message:   err.Error(),
		Provider:  providerName,
		SessionID: sessionID,
		SegmentID: segmentID,
		Cause:     err,
	}
}
