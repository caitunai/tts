package mock

import (
	"context"
	"sync"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
)

type Session struct {
	id  string
	pcm []byte

	mu       sync.Mutex
	segments []*tts.ProviderSegmentRequest
	closed   bool

	events chan *tts.ProviderEvent
}

func newSession(id string, pcm []byte) *Session {
	return &Session{
		id:     id,
		pcm:    pcm,
		events: make(chan *tts.ProviderEvent, 32),
	}
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) AppendText(_ context.Context, segment *tts.ProviderSegmentRequest) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return &tts.Error{Code: tts.ErrSessionClosed, SessionID: s.id, SegmentID: segment.SegmentID}
	}
	segmentCopy := *segment
	s.segments = append(s.segments, &segmentCopy)
	s.mu.Unlock()

	s.events <- &tts.ProviderEvent{Type: tts.ProviderEventSegmentStart, SessionID: s.id, SegmentID: segment.SegmentID}
	s.events <- &tts.ProviderEvent{
		Type:      tts.ProviderEventAudio,
		SessionID: s.id,
		SegmentID: segment.SegmentID,
		Audio: &tts.ProviderAudioChunk{
			Codec:      audio.CodecPCM,
			Container:  audio.ContainerRaw,
			SampleRate: audio.DefaultSampleRate,
			Channels:   audio.DefaultChannels,
			Format:     audio.PCMFormatS16LE,
			Data:       s.pcm,
		},
	}
	s.events <- &tts.ProviderEvent{Type: tts.ProviderEventSegmentEnd, SessionID: s.id, SegmentID: segment.SegmentID}
	return nil
}

func (s *Session) Finish(context.Context) error {
	s.events <- &tts.ProviderEvent{Type: tts.ProviderEventSessionEnd, SessionID: s.id}
	return nil
}

func (s *Session) Events() <-chan *tts.ProviderEvent {
	return s.events
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	close(s.events)
	return nil
}

func (s *Session) Segments() []*tts.ProviderSegmentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	segments := make([]*tts.ProviderSegmentRequest, len(s.segments))
	copy(segments, s.segments)
	return segments
}
