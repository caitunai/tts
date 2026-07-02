package audio

import (
	"encoding/base64"
	"testing"
)

func TestNormalizerConvertsOggOpusToRawOpusFrames(t *testing.T) {
	header := makeOggPage(t, 1, 0, [][]byte{[]byte("OpusHead")})
	audioPage := makeOggPage(t, 1, 1, [][]byte{[]byte("audio-packet")})

	normalizer, err := NewNormalizer(NormalizerConfig{
		RequestID: "req",
		SegmentID: "seg",
		Output: OutputConfig{
			SampleRate: 16000,
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
	if frames[0].SampleRate != OpusSampleRate {
		t.Fatalf("SampleRate = %d, want %d", frames[0].SampleRate, OpusSampleRate)
	}
	if string(frames[0].Data) != "audio-packet" {
		t.Fatalf("Data = %q, want audio-packet", string(frames[0].Data))
	}
}

func TestNormalizerPassesRawOpusPackets(t *testing.T) {
	normalizer, err := NewNormalizer(NormalizerConfig{
		RequestID: "req",
		SegmentID: "seg",
		Output: OutputConfig{
			SampleRate: OpusSampleRate,
			Channels:   1,
			FrameMS:    20,
		},
	})
	if err != nil {
		t.Fatalf("NewNormalizer: %v", err)
	}

	frames, err := normalizer.Push(Chunk{
		Codec:      CodecOpus,
		Container:  ContainerRaw,
		SampleRate: OpusSampleRate,
		Channels:   1,
		Data:       []byte("raw-opus-packet"),
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
	if frames[0].SampleRate != OpusSampleRate {
		t.Fatalf("SampleRate = %d, want %d", frames[0].SampleRate, OpusSampleRate)
	}
	if string(frames[0].Data) != "raw-opus-packet" {
		t.Fatalf("Data = %q, want raw-opus-packet", string(frames[0].Data))
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

func TestNormalizerDecodesMP3ToPCMFrames(t *testing.T) {
	normalizer, err := NewNormalizer(NormalizerConfig{
		RequestID: "req",
		SegmentID: "seg",
		Output: OutputConfig{
			SampleRate: 16000,
			Channels:   1,
			FrameMS:    20,
		},
	})
	if err != nil {
		t.Fatalf("NewNormalizer: %v", err)
	}

	mp3Data, err := base64.StdEncoding.DecodeString(smallMP3Base64)
	if err != nil {
		t.Fatalf("decode mp3 fixture: %v", err)
	}

	frames, err := normalizer.Push(Chunk{
		Codec:     CodecMP3,
		Container: ContainerRaw,
		Data:      mp3Data[:257],
	})
	if err != nil {
		t.Fatalf("Push first chunk: %v", err)
	}
	more, err := normalizer.Push(Chunk{
		Codec:     CodecMP3,
		Container: ContainerRaw,
		Data:      mp3Data[257:],
	})
	if err != nil {
		t.Fatalf("Push second chunk: %v", err)
	}
	frames = append(frames, more...)
	frames = append(frames, normalizer.Finish()...)

	if len(frames) == 0 {
		t.Fatal("frames = 0, want decoded PCM frames")
	}
	for i, frame := range frames {
		if frame.Codec != CodecPCM {
			t.Fatalf("frame[%d].Codec = %q, want %q", i, frame.Codec, CodecPCM)
		}
		if frame.SampleRate != 16000 {
			t.Fatalf("frame[%d].SampleRate = %d, want 16000", i, frame.SampleRate)
		}
		if frame.Channels != 1 {
			t.Fatalf("frame[%d].Channels = %d, want 1", i, frame.Channels)
		}
		if frame.FrameMS != 20 {
			t.Fatalf("frame[%d].FrameMS = %d, want 20", i, frame.FrameMS)
		}
		if len(frame.Data) != 640 {
			t.Fatalf("frame[%d] data length = %d, want 640", i, len(frame.Data))
		}
	}
}

func TestNormalizerDownmixesStereoPCMToMono(t *testing.T) {
	normalizer, err := NewNormalizer(NormalizerConfig{
		RequestID: "req",
		SegmentID: "seg",
		Output: OutputConfig{
			SampleRate: 16000,
			Channels:   1,
			FrameMS:    20,
			PCMFormat:  PCMFormatS16LE,
		},
	})
	if err != nil {
		t.Fatalf("NewNormalizer: %v", err)
	}

	stereo := make([]int16, 0, 640)
	for range 320 {
		stereo = append(stereo, 100, 300)
	}
	frames, err := normalizer.Push(Chunk{
		Codec:      CodecPCM,
		Container:  ContainerRaw,
		SampleRate: 16000,
		Channels:   2,
		Format:     PCMFormatS16LE,
		Data:       Int16ToBytes(stereo),
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	samples := NewResampler(16000, 16000).BytesToInt16(frames[0].Data)
	for i, sample := range samples {
		if sample != 200 {
			t.Fatalf("sample[%d] = %d, want 200", i, sample)
		}
	}
}

func TestNormalizerResamplesPCMToOutputConfig(t *testing.T) {
	normalizer, err := NewNormalizer(NormalizerConfig{
		RequestID: "req",
		SegmentID: "seg",
		Output: OutputConfig{
			SampleRate: 16000,
			Channels:   1,
			FrameMS:    20,
			PCMFormat:  PCMFormatS16LE,
		},
	})
	if err != nil {
		t.Fatalf("NewNormalizer: %v", err)
	}

	frames, err := normalizer.Push(Chunk{
		Codec:      CodecPCM,
		Container:  ContainerRaw,
		SampleRate: 24000,
		Channels:   1,
		Format:     PCMFormatS16LE,
		Data:       Int16ToBytes(rampSamples(480)),
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	if frames[0].SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", frames[0].SampleRate)
	}
	if frames[0].Channels != 1 {
		t.Fatalf("channels = %d, want 1", frames[0].Channels)
	}
	if frames[0].FrameMS != 20 {
		t.Fatalf("frame ms = %d, want 20", frames[0].FrameMS)
	}
	if len(frames[0].Data) != 640 {
		t.Fatalf("frame data length = %d, want 640", len(frames[0].Data))
	}
}

const smallMP3Base64 = "SUQzBAAAAAAAI1RTU0UAAAAPAAADTGF2ZjU3LjcxLjEwMAAAAAAAAAAAAAAA//NgxAAdI/3kAUMYAAAAKu7uBgAAIREREd3d3dwMAAABOuaAYt+J/+iIhaIiIiJ/u7u5//9cAEJ/6O7u7/u7u5/+7ufEAwN3f0R3d3d3f//9E///93d+u7u7v//ERHf93c/0L9Hd3d3d0LiIiF/7u7l/+iAYGBu7vo7u/9cAEIGJdRkMtpsbBo9D6hoNBqLv8AvDJXXo/zsRNehi//NixBol6r7uX5iRIv+EFoA4bcpBaYG6ga2BL2SIo+AVYlMcZOMp1IGgYnGTL4nwvldMsp9qAYkFwIsmeZjRO2wXMCdDwgGKQHgn16Rmmh/z6CBTPidyDkTLRw7oOm57/+QMiZ43UggmYl9yDl9lM1fqTf//zcvl963LjKOKBILmjDU3f/Wb/9xQwmq28GRTlt2zWsJJBugJoak/BP/zYsQSJHNW2j/PWALsLp9JKVJlM25CqLiqfiEy6tQMD7eB4TdFplR6HFY7TpajY2rE1EdBci2qfLbuHOduci2WnWy6LbJq1rVWu3Q69zG1C0tb/CZ2aYrTrzznf///3DnLmzk4QQtKF1N0HFta+recr+pmnXbuIdMV////+yrZWxlzT7WmJpb/9QGfrorHZUFqOW6qYbUDJos="
