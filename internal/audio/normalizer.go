package audio

import "fmt"

// Chunk is a provider audio chunk in audio-package form.
type Chunk struct {
	Codec     Codec
	Container Container

	SampleRate int
	Channels   int
	Format     PCMFormat

	Data []byte
}

// NormalizerConfig configures an audio normalizer.
type NormalizerConfig struct {
	RequestID string
	SessionID string
	SegmentID string

	Output OutputConfig

	TailPolicy TailPolicy

	StartingSeq       uint32
	StartingGlobalSeq uint64
	StartingTimestamp int64
}

// Normalizer converts provider audio chunks to normalized audio frames.
type Normalizer struct {
	cfg NormalizerConfig

	oggDemuxer  OggOpusDemuxer
	pcmSplitter *PCMFrameSplitter

	seq         uint32
	globalSeq   uint64
	timestampMS int64
}

// NewNormalizer creates an audio normalizer.
func NewNormalizer(cfg NormalizerConfig) (*Normalizer, error) {
	output := cfg.Output
	if output.SampleRate == 0 {
		output.SampleRate = DefaultSampleRate
	}
	if output.Channels == 0 {
		output.Channels = DefaultChannels
	}
	if output.FrameMS == 0 {
		output.FrameMS = DefaultFrameMS
	}
	if output.PCMFormat == "" {
		output.PCMFormat = PCMFormatS16LE
	}
	cfg.Output = output

	splitter, err := NewPCMFrameSplitter(PCMFrameSplitterConfig{
		RequestID:         cfg.RequestID,
		SessionID:         cfg.SessionID,
		SegmentID:         cfg.SegmentID,
		SampleRate:        output.SampleRate,
		Channels:          output.Channels,
		FrameMS:           output.FrameMS,
		Format:            output.PCMFormat,
		TailPolicy:        cfg.TailPolicy,
		StartingSeq:       cfg.StartingSeq,
		StartingGlobalSeq: cfg.StartingGlobalSeq,
		StartingTimestamp: cfg.StartingTimestamp,
	})
	if err != nil {
		return nil, err
	}

	return &Normalizer{
		cfg:         cfg,
		pcmSplitter: splitter,
		seq:         cfg.StartingSeq,
		globalSeq:   cfg.StartingGlobalSeq,
		timestampMS: cfg.StartingTimestamp,
	}, nil
}

// Push converts one provider audio chunk into zero or more frames.
func (n *Normalizer) Push(chunk Chunk) ([]Frame, error) {
	switch chunk.Codec {
	case CodecOpus:
		if chunk.Container != ContainerOgg {
			return nil, fmt.Errorf("unsupported opus container %q", chunk.Container)
		}
		packets, err := n.oggDemuxer.Push(chunk.Data)
		if err != nil {
			return nil, err
		}
		return n.opusPacketsToFrames(packets), nil
	case CodecPCM:
		frames := n.pcmSplitter.Push(chunk.Data)
		n.advanceFromFrames(frames)
		return frames, nil
	case CodecWAV:
		pcm, err := ParseWAV(chunk.Data)
		if err != nil {
			return nil, err
		}
		return n.framesFromPCMData(pcm)
	default:
		return nil, fmt.Errorf("unsupported codec %q", chunk.Codec)
	}
}

// Finish flushes any buffered PCM tail data.
func (n *Normalizer) Finish() []Frame {
	frames := n.pcmSplitter.Finish()
	n.advanceFromFrames(frames)
	return frames
}

func (n *Normalizer) opusPacketsToFrames(packets []OpusPacket) []Frame {
	frames := make([]Frame, 0, len(packets))
	for _, packet := range packets {
		if isOggOpusHeaderPacket(packet.Data) {
			continue
		}

		data := make([]byte, len(packet.Data))
		copy(data, packet.Data)
		frames = append(frames, Frame{
			RequestID:   n.cfg.RequestID,
			SessionID:   n.cfg.SessionID,
			SegmentID:   n.cfg.SegmentID,
			Codec:       CodecOpus,
			Container:   ContainerRaw,
			SampleRate:  n.cfg.Output.SampleRate,
			Channels:    n.cfg.Output.Channels,
			Seq:         n.seq,
			GlobalSeq:   n.globalSeq,
			TimestampMS: n.timestampMS,
			Data:        data,
		})

		n.seq++
		n.globalSeq++
	}
	return frames
}

func (n *Normalizer) framesFromPCMData(pcm PCMData) ([]Frame, error) {
	splitter, err := NewPCMFrameSplitter(PCMFrameSplitterConfig{
		RequestID:         n.cfg.RequestID,
		SessionID:         n.cfg.SessionID,
		SegmentID:         n.cfg.SegmentID,
		SampleRate:        pcm.SampleRate,
		Channels:          pcm.Channels,
		FrameMS:           n.cfg.Output.FrameMS,
		Format:            pcm.Format,
		TailPolicy:        n.cfg.TailPolicy,
		StartingSeq:       n.seq,
		StartingGlobalSeq: n.globalSeq,
		StartingTimestamp: n.timestampMS,
	})
	if err != nil {
		return nil, err
	}

	frames := splitter.Push(pcm.Data)
	frames = append(frames, splitter.Finish()...)
	n.advanceFromFrames(frames)
	return frames, nil
}

func (n *Normalizer) advanceFromFrames(frames []Frame) {
	if len(frames) == 0 {
		return
	}

	last := frames[len(frames)-1]
	n.seq = last.Seq + 1
	n.globalSeq = last.GlobalSeq + 1
	if last.FrameMS > 0 {
		n.timestampMS = last.TimestampMS + int64(last.FrameMS)
		return
	}
	if last.PacketDurationMS > 0 {
		n.timestampMS = last.TimestampMS + int64(last.PacketDurationMS)
	}
}

func isOggOpusHeaderPacket(data []byte) bool {
	return hasPrefixString(data, "OpusHead") || hasPrefixString(data, "OpusTags")
}

func hasPrefixString(data []byte, prefix string) bool {
	if len(data) < len(prefix) {
		return false
	}
	return string(data[:len(prefix)]) == prefix
}
