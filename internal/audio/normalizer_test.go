package audio

import "testing"

func TestNormalizerConvertsOggOpusToRawOpusFrames(t *testing.T) {
	header := makeOggPage(t, 1, 0, [][]byte{[]byte("OpusHead")})
	audioPage := makeOggPage(t, 1, 1, [][]byte{[]byte("audio-packet")})

	normalizer, err := NewNormalizer(NormalizerConfig{
		RequestID: "req",
		SegmentID: "seg",
		Output: OutputConfig{
			SampleRate: 48000,
			Channels:   1,
			FrameMS:    20,
		},
	})
	if err != nil {
		t.Fatalf("NewNormalizer: %v", err)
	}

	frames, err := normalizer.Push(Chunk{
		Codec:     CodecOpus,
		Container: ContainerOgg,
		Data:      append(header, audioPage...),
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if frames[0].Codec != CodecOpus {
		t.Fatalf("Codec = %q, want %q", frames[0].Codec, CodecOpus)
	}
	if frames[0].Container != ContainerRaw {
		t.Fatalf("Container = %q, want %q", frames[0].Container, ContainerRaw)
	}
	if string(frames[0].Data) != "audio-packet" {
		t.Fatalf("Data = %q, want audio-packet", string(frames[0].Data))
	}
}

func TestNormalizerConvertsWAVToPCMFrames(t *testing.T) {
	wav := makeTestWAV(t, 16000, 1, []byte{1, 2, 3})

	normalizer, err := NewNormalizer(NormalizerConfig{
		RequestID: "req",
		SegmentID: "seg",
		Output: OutputConfig{
			FrameMS: 20,
		},
	})
	if err != nil {
		t.Fatalf("NewNormalizer: %v", err)
	}

	frames, err := normalizer.Push(Chunk{
		Codec:     CodecWAV,
		Container: ContainerWAV,
		Data:      wav,
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if frames[0].Codec != CodecPCM {
		t.Fatalf("Codec = %q, want %q", frames[0].Codec, CodecPCM)
	}
	if len(frames[0].Data) != 640 {
		t.Fatalf("frame data length = %d, want 640", len(frames[0].Data))
	}
}
