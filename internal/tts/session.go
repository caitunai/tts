package tts

import (
	"context"
	"sync"

	"github.com/caitunai/tts/internal/audio"
)

type providerBackedSession struct {
	providerName string
	output       audio.OutputConfig
	provider     ProviderSession

	eventsOnce sync.Once
	events     chan *Event
}

func newProviderBackedSession(providerName string, output audio.OutputConfig, provider ProviderSession) Session {
	session := &providerBackedSession{
		providerName: providerName,
		output:       output,
		provider:     provider,
	}
	return session
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

	return s.provider.AppendText(ctx, &ProviderSegmentRequest{
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
	})
}

func (s *providerBackedSession) Finish(ctx context.Context) error {
	if s.provider == nil {
		return internalError("provider session is nil")
	}
	return s.provider.Finish(ctx)
}

func (s *providerBackedSession) Events() <-chan *Event {
	s.eventsOnce.Do(func() {
		s.events = make(chan *Event)
		go func() {
			defer close(s.events)
			if s.provider == nil {
				s.events <- &Event{
					Type:  EventError,
					Error: internalError("provider session is nil"),
				}
				return
			}
			for event := range s.provider.Events() {
				s.events <- providerEventToEvent(event)
			}
		}()
	})
	return s.events
}

func (s *providerBackedSession) Close() error {
	if s.provider == nil {
		return nil
	}
	return s.provider.Close()
}

func providerEventsToEvents(providerEvents <-chan *ProviderEvent) <-chan *Event {
	events := make(chan *Event)
	go func() {
		defer close(events)
		if providerEvents == nil {
			return
		}
		for event := range providerEvents {
			events <- providerEventToEvent(event)
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
