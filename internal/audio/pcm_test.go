package audio

import "testing"

func TestPCMFrameSplitterAcrossChunks(t *testing.T) {
	splitter, err := NewPCMFrameSplitter(PCMFrameSplitterConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameMS:    20,
	})
	if err != nil {
		t.Fatalf("NewPCMFrameSplitter: %v", err)
	}

	if splitter.FrameBytes() != 640 {
		t.Fatalf("FrameBytes = %d, want 640", splitter.FrameBytes())
	}

	if frames := splitter.Push(make([]byte, 300)); len(frames) != 0 {
		t.Fatalf("frames after partial push = %d, want 0", len(frames))
	}

	frames := splitter.Push(make([]byte, 340))
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if len(frames[0].Data) != 640 {
		t.Fatalf("frame data length = %d, want 640", len(frames[0].Data))
	}
	if frames[0].Seq != 0 {
		t.Fatalf("Seq = %d, want 0", frames[0].Seq)
	}
}

func TestPCMFrameSplitterPadsTail(t *testing.T) {
	splitter, err := NewPCMFrameSplitter(PCMFrameSplitterConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameMS:    20,
	})
	if err != nil {
		t.Fatalf("NewPCMFrameSplitter: %v", err)
	}

	_ = splitter.Push([]byte{1, 2, 3})
	frames := splitter.Finish()
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if len(frames[0].Data) != 640 {
		t.Fatalf("frame data length = %d, want 640", len(frames[0].Data))
	}
	if !frames[0].SegmentFinal {
		t.Fatal("SegmentFinal = false, want true")
	}
	if frames[0].Data[0] != 1 || frames[0].Data[1] != 2 || frames[0].Data[2] != 3 {
		t.Fatal("tail data was not preserved before padding")
	}
}

func TestPCMFrameSplitterDropsTail(t *testing.T) {
	splitter, err := NewPCMFrameSplitter(PCMFrameSplitterConfig{
		SampleRate: 16000,
		Channels:   1,
		FrameMS:    20,
		TailPolicy: TailPolicyDrop,
	})
	if err != nil {
		t.Fatalf("NewPCMFrameSplitter: %v", err)
	}

	_ = splitter.Push([]byte{1, 2, 3})
	frames := splitter.Finish()
	if len(frames) != 0 {
		t.Fatalf("frames = %d, want 0", len(frames))
	}
}
