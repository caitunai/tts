package tts

import (
	"testing"

	"github.com/caitunai/tts/internal/audio"
)

func TestProviderCapabilitiesDeclareAdvancedInputs(t *testing.T) {
	caps := ProviderCapabilities{
		Name:                               "mock",
		Transports:                         []TransportType{TransportHTTP},
		SupportsGuidanceText:               true,
		SupportsReferenceAudio:             true,
		ReferenceAudioCodecs:               []audio.Codec{audio.CodecWAV},
		ReferenceAudioContainers:           []audio.Container{audio.ContainerWAV},
		MaxReferenceAudioBytes:             1 << 20,
		RequiresReferenceText:              true,
		SupportsReferenceAudioURL:          true,
		SupportsSegmentLevelGuidance:       true,
		SupportsSegmentLevelReferenceAudio: true,
	}

	if !caps.SupportsGuidanceText {
		t.Fatal("SupportsGuidanceText = false, want true")
	}
	if !caps.SupportsReferenceAudio {
		t.Fatal("SupportsReferenceAudio = false, want true")
	}
	if caps.ReferenceAudioCodecs[0] != audio.CodecWAV {
		t.Fatalf("ReferenceAudioCodecs[0] = %q, want %q", caps.ReferenceAudioCodecs[0], audio.CodecWAV)
	}
	if caps.ReferenceAudioContainers[0] != audio.ContainerWAV {
		t.Fatalf("ReferenceAudioContainers[0] = %q, want %q", caps.ReferenceAudioContainers[0], audio.ContainerWAV)
	}
	if !caps.RequiresReferenceText {
		t.Fatal("RequiresReferenceText = false, want true")
	}
}
