package tts

import "github.com/caitunai/tts/internal/audio"

// SynthesizeRequest describes a single TTS synthesis request.
type SynthesizeRequest struct {
	RequestID string

	Provider string

	Text     string
	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *ReferenceAudio

	Output audio.OutputConfig

	Options map[string]any
}

// OpenSessionRequest describes a long-lived TTS session request.
type OpenSessionRequest struct {
	SessionID string

	Provider string

	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *ReferenceAudio

	Output audio.OutputConfig

	Options map[string]any
}

// SegmentRequest describes one text segment appended to an open session.
type SegmentRequest struct {
	SegmentID string

	Text     string
	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *ReferenceAudio

	Speed   float64
	Pitch   float64
	Volume  float64
	Emotion string

	IsLast bool

	Options map[string]any
}

// ReferenceAudio describes a wav reference sample used by capable providers.
type ReferenceAudio struct {
	ID string

	Codec     audio.Codec
	Container audio.Container

	SampleRate int
	Channels   int

	Data []byte
	URL  string

	Text string

	Options map[string]any
}

// ProviderSynthesizeRequest is the provider-facing form of SynthesizeRequest.
type ProviderSynthesizeRequest struct {
	RequestID string

	Text     string
	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *ReferenceAudio

	Output audio.OutputConfig

	Options map[string]any
}

// ProviderOpenSessionRequest is the provider-facing form of OpenSessionRequest.
type ProviderOpenSessionRequest struct {
	SessionID string

	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *ReferenceAudio

	Output audio.OutputConfig

	Options map[string]any
}

// ProviderSegmentRequest is the provider-facing form of SegmentRequest.
type ProviderSegmentRequest struct {
	SegmentID string

	Text     string
	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *ReferenceAudio

	Speed   float64
	Pitch   float64
	Volume  float64
	Emotion string

	IsLast bool

	Options map[string]any
}
