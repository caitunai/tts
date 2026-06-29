package audio

import (
	"encoding/binary"
	"fmt"
)

const s16leBytesPerSample = 2

// TailPolicy controls how a splitter handles the final partial PCM frame.
type TailPolicy string

const (
	TailPolicyPadSilence TailPolicy = "pad_silence"
	TailPolicyDrop       TailPolicy = "drop"
)

// PCMFrameSplitterConfig configures a PCM frame splitter.
type PCMFrameSplitterConfig struct {
	RequestID string
	SessionID string
	SegmentID string

	SampleRate int
	Channels   int
	FrameMS    int
	Format     PCMFormat

	TailPolicy TailPolicy

	StartingSeq       uint32
	StartingGlobalSeq uint64
	StartingTimestamp int64
}

// PCMFrameSplitter splits an arbitrary PCM byte stream into fixed-size PCM
// frames suitable for an Opus encoder.
type PCMFrameSplitter struct {
	cfg PCMFrameSplitterConfig

	frameBytes int
	buffer     []byte

	seq         uint32
	globalSeq   uint64
	timestampMS int64
}

// NewPCMFrameSplitter creates a splitter with validated defaults.
func NewPCMFrameSplitter(cfg PCMFrameSplitterConfig) (*PCMFrameSplitter, error) {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = DefaultSampleRate
	}
	if cfg.Channels == 0 {
		cfg.Channels = DefaultChannels
	}
	if cfg.FrameMS == 0 {
		cfg.FrameMS = DefaultFrameMS
	}
	if cfg.Format == "" {
		cfg.Format = PCMFormatS16LE
	}
	if cfg.TailPolicy == "" {
		cfg.TailPolicy = TailPolicyPadSilence
	}

	if cfg.SampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive")
	}
	if cfg.Channels <= 0 {
		return nil, fmt.Errorf("channels must be positive")
	}
	if cfg.FrameMS <= 0 {
		return nil, fmt.Errorf("frame ms must be positive")
	}
	if cfg.Format != PCMFormatS16LE {
		return nil, fmt.Errorf("unsupported PCM format %q", cfg.Format)
	}
	if cfg.TailPolicy != TailPolicyPadSilence && cfg.TailPolicy != TailPolicyDrop {
		return nil, fmt.Errorf("unsupported tail policy %q", cfg.TailPolicy)
	}

	frameBytes := cfg.SampleRate * cfg.FrameMS / 1000 * cfg.Channels * s16leBytesPerSample
	if frameBytes <= 0 {
		return nil, fmt.Errorf("frame bytes must be positive")
	}

	return &PCMFrameSplitter{
		cfg:         cfg,
		frameBytes:  frameBytes,
		seq:         cfg.StartingSeq,
		globalSeq:   cfg.StartingGlobalSeq,
		timestampMS: cfg.StartingTimestamp,
	}, nil
}

// FrameBytes returns the configured byte length of one PCM frame.
func (s *PCMFrameSplitter) FrameBytes() int {
	return s.frameBytes
}

// Push appends PCM data and returns all complete frames currently available.
func (s *PCMFrameSplitter) Push(data []byte) []Frame {
	if len(data) > 0 {
		s.buffer = append(s.buffer, data...)
	}
	return s.drain(false)
}

// Finish flushes the splitter and applies the configured tail policy.
func (s *PCMFrameSplitter) Finish() []Frame {
	if len(s.buffer) == 0 {
		return nil
	}
	if s.cfg.TailPolicy == TailPolicyDrop {
		s.buffer = nil
		return nil
	}

	padding := s.frameBytes - len(s.buffer)
	s.buffer = append(s.buffer, make([]byte, padding)...)
	return s.drain(true)
}

func (s *PCMFrameSplitter) drain(segmentFinal bool) []Frame {
	if len(s.buffer) < s.frameBytes {
		return nil
	}

	frames := make([]Frame, 0, len(s.buffer)/s.frameBytes)
	for len(s.buffer) >= s.frameBytes {
		data := make([]byte, s.frameBytes)
		copy(data, s.buffer[:s.frameBytes])
		s.buffer = s.buffer[s.frameBytes:]

		final := segmentFinal && len(s.buffer) == 0
		frames = append(frames, Frame{
			RequestID:        s.cfg.RequestID,
			SessionID:        s.cfg.SessionID,
			SegmentID:        s.cfg.SegmentID,
			Codec:            CodecPCM,
			Container:        ContainerRaw,
			SampleRate:       s.cfg.SampleRate,
			Channels:         s.cfg.Channels,
			FrameMS:          s.cfg.FrameMS,
			PacketDurationMS: s.cfg.FrameMS,
			Format:           s.cfg.Format,
			Seq:              s.seq,
			GlobalSeq:        s.globalSeq,
			TimestampMS:      s.timestampMS,
			Data:             data,
			SegmentFinal:     final,
		})

		s.seq++
		s.globalSeq++
		s.timestampMS += int64(s.cfg.FrameMS)
	}

	return frames
}

func stereoS16LEToMono(data []byte) []byte {
	frameBytes := s16leBytesPerSample * 2
	sampleFrames := len(data) / frameBytes
	if sampleFrames == 0 {
		return nil
	}

	mono := make([]byte, sampleFrames*s16leBytesPerSample)
	for i := 0; i < sampleFrames; i++ {
		offset := i * frameBytes
		left := int16(binary.LittleEndian.Uint16(data[offset : offset+s16leBytesPerSample]))
		right := int16(binary.LittleEndian.Uint16(data[offset+s16leBytesPerSample : offset+frameBytes]))
		mixed := int32(left)/2 + int32(right)/2
		binary.LittleEndian.PutUint16(mono[i*s16leBytesPerSample:(i+1)*s16leBytesPerSample], uint16(int16(mixed)))
	}
	return mono
}
