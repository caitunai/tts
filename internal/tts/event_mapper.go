package tts

import "github.com/caitunai/tts/internal/audio"

type eventMapper struct {
	output audio.OutputConfig

	normalizers map[string]*audio.Normalizer
	globalSeq   uint64
}

func newEventMapper(output audio.OutputConfig) *eventMapper {
	return &eventMapper{
		output:      output,
		normalizers: make(map[string]*audio.Normalizer),
	}
}

func (m *eventMapper) convert(event *ProviderEvent) []*Event {
	if event == nil {
		return []*Event{{
			Type:  EventError,
			Error: internalError("provider event is nil"),
		}}
	}

	switch event.Type {
	case ProviderEventAudio:
		return m.convertAudio(event)
	case ProviderEventSegmentEnd:
		events := m.flushSegment(event)
		events = append(events, mapProviderEvent(event))
		m.dropNormalizer(event)
		return events
	case ProviderEventSessionEnd:
		events := m.flushAll(event)
		events = append(events, mapProviderEvent(event))
		return events
	default:
		return []*Event{mapProviderEvent(event)}
	}
}

func (m *eventMapper) convertAudio(event *ProviderEvent) []*Event {
	if event.Audio == nil {
		return []*Event{{
			Type:      EventError,
			RequestID: event.RequestID,
			SessionID: event.SessionID,
			SegmentID: event.SegmentID,
			Error:     internalError("provider audio event has nil audio"),
		}}
	}

	normalizer, err := m.normalizerFor(event)
	if err != nil {
		return []*Event{{
			Type:      EventError,
			RequestID: event.RequestID,
			SessionID: event.SessionID,
			SegmentID: event.SegmentID,
			Error:     errorToTTSError(err, event.Provider, event.SessionID, event.SegmentID),
		}}
	}

	frames, err := normalizer.Push(audio.Chunk{
		Codec:      event.Audio.Codec,
		Container:  event.Audio.Container,
		SampleRate: event.Audio.SampleRate,
		Channels:   event.Audio.Channels,
		Format:     event.Audio.Format,
		Data:       event.Audio.Data,
	})
	if err != nil {
		return []*Event{{
			Type:      EventError,
			RequestID: event.RequestID,
			SessionID: event.SessionID,
			SegmentID: event.SegmentID,
			Error:     errorToTTSError(err, event.Provider, event.SessionID, event.SegmentID),
		}}
	}

	events := m.framesToEvents(event, frames)
	if event.Final {
		events = append(events, m.flushSegment(event)...)
	}
	return events
}

func (m *eventMapper) flushSegment(event *ProviderEvent) []*Event {
	normalizer := m.normalizers[m.key(event)]
	if normalizer == nil {
		return nil
	}

	frames := normalizer.Finish()
	events := m.framesToEvents(event, frames)
	if len(events) > 0 {
		events[len(events)-1].Audio.SegmentFinal = true
	}
	return events
}

func (m *eventMapper) flushAll(event *ProviderEvent) []*Event {
	var events []*Event
	for _, normalizer := range m.normalizers {
		frames := normalizer.Finish()
		events = append(events, m.framesToEvents(event, frames)...)
	}
	return events
}

func (m *eventMapper) framesToEvents(event *ProviderEvent, frames []audio.Frame) []*Event {
	events := make([]*Event, 0, len(frames))
	for i := range frames {
		frame := frames[i]
		frame.RequestID = event.RequestID
		frame.SessionID = event.SessionID
		frame.SegmentID = event.SegmentID
		frame.GlobalSeq = m.globalSeq
		m.globalSeq++

		frameCopy := frame
		events = append(events, &Event{
			Type:      EventAudioFrame,
			RequestID: event.RequestID,
			SessionID: event.SessionID,
			SegmentID: event.SegmentID,
			Audio:     &frameCopy,
			Meta:      event.RawMeta,
		})
	}
	return events
}

func (m *eventMapper) normalizerFor(event *ProviderEvent) (*audio.Normalizer, error) {
	key := m.key(event)
	if normalizer := m.normalizers[key]; normalizer != nil {
		return normalizer, nil
	}

	normalizer, err := audio.NewNormalizer(audio.NormalizerConfig{
		RequestID:         event.RequestID,
		SessionID:         event.SessionID,
		SegmentID:         event.SegmentID,
		Output:            m.output,
		StartingGlobalSeq: m.globalSeq,
		StartingTimestamp: 0,
		StartingSeq:       0,
	})
	if err != nil {
		return nil, err
	}
	m.normalizers[key] = normalizer
	return normalizer, nil
}

func (m *eventMapper) key(event *ProviderEvent) string {
	if event.SegmentID != "" {
		return event.SegmentID
	}
	if event.RequestID != "" {
		return event.RequestID
	}
	return event.SessionID
}

func (m *eventMapper) dropNormalizer(event *ProviderEvent) {
	delete(m.normalizers, m.key(event))
}

func mapProviderEvent(event *ProviderEvent) *Event {
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
