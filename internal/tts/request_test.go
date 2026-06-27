package tts

import (
	"testing"

	"github.com/caitunai/tts/internal/audio"
)

func TestRequestCarriesAdvancedInputs(t *testing.T) {
	ref := &ReferenceAudio{
		ID:        "ref_001",
		Codec:     audio.CodecWAV,
		Container: audio.ContainerWAV,
		Data:      []byte("wav"),
		Text:      "hello",
	}
	req := SynthesizeRequest{
		RequestID:      "req_001",
		Provider:       "doubao",
		Text:           "你好",
		GuidanceText:   "warm narration",
		ReferenceAudio: ref,
	}

	if req.Provider == "" {
		t.Fatal("Provider is empty")
	}
	if req.GuidanceText == "" {
		t.Fatal("GuidanceText is empty")
	}
	if req.ReferenceAudio != ref {
		t.Fatal("ReferenceAudio was not preserved")
	}
	if req.Text == req.GuidanceText {
		t.Fatal("GuidanceText must be separate from Text")
	}
}

func TestSegmentCanOverrideSessionAdvancedInputs(t *testing.T) {
	sessionRef := &ReferenceAudio{ID: "session_ref"}
	segmentRef := &ReferenceAudio{ID: "segment_ref"}

	open := OpenSessionRequest{
		SessionID:      "sess_001",
		Provider:       "doubao",
		GuidanceText:   "session guidance",
		ReferenceAudio: sessionRef,
	}
	segment := SegmentRequest{
		SegmentID:      "seg_001",
		Text:           "hello",
		GuidanceText:   "segment guidance",
		ReferenceAudio: segmentRef,
	}

	if open.ReferenceAudio == segment.ReferenceAudio {
		t.Fatal("segment reference audio should be able to override session reference audio")
	}
	if open.GuidanceText == segment.GuidanceText {
		t.Fatal("segment guidance should be able to override session guidance")
	}
}
