// Package audio exposes public audio types and helpers used by the TTS API.
package audio

import internal "github.com/caitunai/tts/internal/audio"

type Codec = internal.Codec
type Container = internal.Container
type PCMFormat = internal.PCMFormat
type OutputConfig = internal.OutputConfig
type Frame = internal.Frame

type OpusPacket = internal.OpusPacket
type OggOpusDemuxer = internal.OggOpusDemuxer
type OggOpusMuxer = internal.OggOpusMuxer

type PCMData = internal.PCMData
type TailPolicy = internal.TailPolicy
type PCMFrameSplitterConfig = internal.PCMFrameSplitterConfig
type PCMFrameSplitter = internal.PCMFrameSplitter

type Chunk = internal.Chunk
type NormalizerConfig = internal.NormalizerConfig
type Normalizer = internal.Normalizer

type Resampler = internal.Resampler
type MP3StreamDecoder = internal.MP3StreamDecoder

const (
	CodecAuto = internal.CodecAuto
	CodecOpus = internal.CodecOpus
	CodecPCM  = internal.CodecPCM
	CodecMP3  = internal.CodecMP3
	CodecAAC  = internal.CodecAAC
	CodecWAV  = internal.CodecWAV
)

const (
	ContainerNone = internal.ContainerNone
	ContainerRaw  = internal.ContainerRaw
	ContainerOgg  = internal.ContainerOgg
	ContainerWAV  = internal.ContainerWAV
)

const (
	PCMFormatUnspecified = internal.PCMFormatUnspecified
	PCMFormatS16LE       = internal.PCMFormatS16LE
)

const (
	DefaultSampleRate = internal.DefaultSampleRate
	OpusSampleRate    = internal.OpusSampleRate
	DefaultChannels   = internal.DefaultChannels
	DefaultFrameMS    = internal.DefaultFrameMS
)

const (
	TailPolicyPadSilence = internal.TailPolicyPadSilence
	TailPolicyDrop       = internal.TailPolicyDrop
)

func DefaultOutputConfig() OutputConfig {
	return internal.DefaultOutputConfig()
}

func NewOggOpusMuxer() *OggOpusMuxer {
	return internal.NewOggOpusMuxer()
}

func ParseWAV(data []byte) (PCMData, error) {
	return internal.ParseWAV(data)
}

func NewPCMFrameSplitter(cfg PCMFrameSplitterConfig) (*PCMFrameSplitter, error) {
	return internal.NewPCMFrameSplitter(cfg)
}

func NewNormalizer(cfg NormalizerConfig) (*Normalizer, error) {
	return internal.NewNormalizer(cfg)
}

func NewResampler(inRate, outRate int) *Resampler {
	return internal.NewResampler(inRate, outRate)
}

func Int16ToBytes(samples []int16) []byte {
	return internal.Int16ToBytes(samples)
}

func NewMP3StreamDecoder() *MP3StreamDecoder {
	return internal.NewMP3StreamDecoder()
}
