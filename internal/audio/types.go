package audio

// Codec identifies the encoding of an audio payload.
type Codec string

const (
	CodecAuto Codec = "auto"
	CodecOpus Codec = "opus"
	CodecPCM  Codec = "pcm"
	CodecMP3  Codec = "mp3"
	CodecAAC  Codec = "aac"
	CodecWAV  Codec = "wav"
)

// Container identifies the container wrapping an audio payload.
type Container string

const (
	ContainerNone Container = ""
	ContainerRaw  Container = "raw"
	ContainerOgg  Container = "ogg"
	ContainerWAV  Container = "wav"
)

// PCMFormat identifies the sample format used for PCM payloads.
type PCMFormat string

const (
	PCMFormatUnspecified PCMFormat = ""
	PCMFormatS16LE       PCMFormat = "s16le"
)

const (
	DefaultSampleRate = 16000
	OpusSampleRate    = 48000
	DefaultChannels   = 1
	DefaultFrameMS    = 20
)

// OutputConfig describes the normalized audio output requested from the
// platform.
type OutputConfig struct {
	PreferCodec Codec
	ActualCodec Codec

	SampleRate int
	Channels   int
	FrameMS    int

	PCMFormat PCMFormat

	AllowOggOpusDemux   bool
	AllowRawOpusOutput  bool
	AllowPCMFrameOutput bool
}

// DefaultOutputConfig returns the first-stage default for realtime TTS output.
func DefaultOutputConfig() OutputConfig {
	return OutputConfig{
		PreferCodec:         CodecAuto,
		SampleRate:          DefaultSampleRate,
		Channels:            DefaultChannels,
		FrameMS:             DefaultFrameMS,
		PCMFormat:           PCMFormatS16LE,
		AllowOggOpusDemux:   true,
		AllowRawOpusOutput:  true,
		AllowPCMFrameOutput: true,
	}
}

// Frame is the normalized audio unit emitted by the platform.
type Frame struct {
	RequestID string
	SessionID string
	SegmentID string

	Codec     Codec
	Container Container

	SampleRate       int
	Channels         int
	FrameMS          int
	PacketDurationMS int
	Format           PCMFormat

	Seq       uint32
	GlobalSeq uint64

	TimestampMS int64

	Data []byte

	SegmentFinal bool
	SessionFinal bool
}
