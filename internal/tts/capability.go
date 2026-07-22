package tts

import "github.com/caitunai/tts/internal/audio"

// TransportType identifies the transport used by a provider.
type TransportType string

const (
	TransportHTTP      TransportType = "http"
	TransportWebSocket TransportType = "websocket"
)

// ProviderCapabilities describe a provider's static feature set.
type ProviderCapabilities struct {
	Name string

	Transports []TransportType

	SupportsStreaming      bool
	SupportsAppendText     bool
	SupportsSSML           bool
	SupportsVoiceClone     bool
	SupportsGuidanceText   bool
	SupportsReferenceAudio bool
	SupportsEmotion        bool
	SupportsSpeed          bool
	SupportsPitch          bool
	SupportsVolume         bool

	OutputCodecs      []audio.Codec
	OutputContainers  []audio.Container
	OutputSampleRates []int
	OutputChannels    []int

	ReferenceAudioCodecs               []audio.Codec
	ReferenceAudioContainers           []audio.Container
	MaxReferenceAudioBytes             int64
	MinReferenceAudioMS                int
	MaxReferenceAudioMS                int
	RequiresReferenceText              bool
	SupportsReferenceAudioURL          bool
	SupportsSegmentLevelGuidance       bool
	SupportsSegmentLevelReferenceAudio bool

	SupportsSegmentEndEvent bool
	SupportsOggOpusOutput   bool
	SupportsPCMOutput       bool

	// Empty lists mean the provider does not expose a finite platform-side allowlist.
	Voices    []VoiceInfo
	Languages []LanguageInfo
}

// ServiceCapabilities describes aggregate service-level capabilities.
type ServiceCapabilities struct {
	Providers []*ProviderCapabilities
}

// VoiceInfo describes one provider voice.
type VoiceInfo struct {
	ID       string
	Name     string
	Language string
	Gender   string
	Meta     map[string]any
}

// LanguageInfo describes one supported language.
type LanguageInfo struct {
	Code string
	Name string
	Meta map[string]any
}
