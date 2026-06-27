package tts

import "github.com/caitunai/tts/internal/audio"

// EventType identifies the public event emitted to applications.
type EventType string

const (
	EventSessionStart EventType = "session_start"
	EventSegmentStart EventType = "segment_start"
	EventAudioFrame   EventType = "audio_frame"
	EventSegmentEnd   EventType = "segment_end"
	EventSessionEnd   EventType = "session_end"
	EventError        EventType = "error"
)

// Event is the public event emitted by the TTS platform.
type Event struct {
	Type EventType

	RequestID string
	SessionID string
	SegmentID string

	Audio *audio.Frame

	Meta  map[string]any
	Error *Error
}

// ProviderEventType identifies normalized provider adapter events.
type ProviderEventType string

const (
	ProviderEventSessionStart ProviderEventType = "session_start"
	ProviderEventSegmentStart ProviderEventType = "segment_start"
	ProviderEventAudio        ProviderEventType = "audio"
	ProviderEventSegmentEnd   ProviderEventType = "segment_end"
	ProviderEventSessionEnd   ProviderEventType = "session_end"
	ProviderEventError        ProviderEventType = "error"
)

// ProviderEvent is the provider-facing normalized event consumed by the service
// layer.
type ProviderEvent struct {
	Type ProviderEventType

	Provider string

	RequestID string
	SessionID string
	SegmentID string

	ProviderRequestID string
	ProviderTaskID    string

	Audio *ProviderAudioChunk

	Final bool

	RawMeta map[string]any
	Error   *Error
}

// ProviderAudioChunk carries the raw audio chunk returned by a provider.
type ProviderAudioChunk struct {
	Codec     audio.Codec
	Container audio.Container

	SampleRate int
	Channels   int
	Format     audio.PCMFormat

	Data []byte
}
