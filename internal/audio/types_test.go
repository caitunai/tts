package audio

import "testing"

func TestDefaultOutputConfig(t *testing.T) {
	cfg := DefaultOutputConfig()

	if cfg.PreferCodec != CodecAuto {
		t.Fatalf("PreferCodec = %q, want %q", cfg.PreferCodec, CodecAuto)
	}
	if cfg.SampleRate != DefaultSampleRate {
		t.Fatalf("SampleRate = %d, want %d", cfg.SampleRate, DefaultSampleRate)
	}
	if cfg.Channels != DefaultChannels {
		t.Fatalf("Channels = %d, want %d", cfg.Channels, DefaultChannels)
	}
	if cfg.FrameMS != DefaultFrameMS {
		t.Fatalf("FrameMS = %d, want %d", cfg.FrameMS, DefaultFrameMS)
	}
	if cfg.PCMFormat != PCMFormatS16LE {
		t.Fatalf("PCMFormat = %q, want %q", cfg.PCMFormat, PCMFormatS16LE)
	}
	if !cfg.AllowOggOpusDemux {
		t.Fatal("AllowOggOpusDemux = false, want true")
	}
	if !cfg.AllowRawOpusOutput {
		t.Fatal("AllowRawOpusOutput = false, want true")
	}
	if !cfg.AllowPCMFrameOutput {
		t.Fatal("AllowPCMFrameOutput = false, want true")
	}
}

func TestFrameCanRepresentRawOpusPacket(t *testing.T) {
	frame := Frame{
		Codec:            CodecOpus,
		Container:        ContainerRaw,
		SampleRate:       48000,
		Channels:         1,
		PacketDurationMS: 20,
		Data:             []byte{0x01, 0x02},
	}

	if frame.Codec != CodecOpus {
		t.Fatalf("Codec = %q, want %q", frame.Codec, CodecOpus)
	}
	if frame.Container != ContainerRaw {
		t.Fatalf("Container = %q, want %q", frame.Container, ContainerRaw)
	}
	if len(frame.Data) != 2 {
		t.Fatalf("Data length = %d, want 2", len(frame.Data))
	}
}
