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

	oggDemuxer   OggOpusDemuxer
	pcmSplitter  *PCMFrameSplitter
	pcmResampler *Resampler

	pcmInputSampleRate int

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
		data, err := n.normalizeStreamingPCM(chunk)
		if err != nil {
			return nil, err
		}
		frames := n.pcmSplitter.Push(data)
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
	data, sampleRate, channels, format, err := n.normalizePCMData(pcm)
	if err != nil {
		return nil, err
	}

	splitter, err := NewPCMFrameSplitter(PCMFrameSplitterConfig{
		RequestID:         n.cfg.RequestID,
		SessionID:         n.cfg.SessionID,
		SegmentID:         n.cfg.SegmentID,
		SampleRate:        sampleRate,
		Channels:          channels,
		FrameMS:           n.cfg.Output.FrameMS,
		Format:            format,
		TailPolicy:        n.cfg.TailPolicy,
		StartingSeq:       n.seq,
		StartingGlobalSeq: n.globalSeq,
		StartingTimestamp: n.timestampMS,
	})
	if err != nil {
		return nil, err
	}

	frames := splitter.Push(data)
	frames = append(frames, splitter.Finish()...)
	n.advanceFromFrames(frames)
	return frames, nil
}

func (n *Normalizer) normalizeStreamingPCM(chunk Chunk) ([]byte, error) {
	pcm := PCMData{
		SampleRate: chunk.SampleRate,
		Channels:   chunk.Channels,
		Format:     chunk.Format,
		Data:       chunk.Data,
	}
	if pcm.SampleRate == 0 {
		pcm.SampleRate = n.cfg.Output.SampleRate
	}
	if pcm.Channels == 0 {
		pcm.Channels = n.cfg.Output.Channels
	}
	if pcm.Format == "" {
		pcm.Format = n.cfg.Output.PCMFormat
	}

	if err := n.validatePCMShape(pcm); err != nil {
		return nil, err
	}
	if pcm.SampleRate == n.cfg.Output.SampleRate {
		return pcm.Data, nil
	}

	if n.pcmResampler == nil || n.pcmInputSampleRate != pcm.SampleRate {
		n.pcmResampler = NewResampler(pcm.SampleRate, n.cfg.Output.SampleRate)
		n.pcmInputSampleRate = pcm.SampleRate
	}
	return n.pcmResampler.ProcessBytes(pcm.Data), nil
}

func (n *Normalizer) normalizePCMData(pcm PCMData) ([]byte, int, int, PCMFormat, error) {
	if pcm.SampleRate == 0 {
		pcm.SampleRate = n.cfg.Output.SampleRate
	}
	if pcm.Channels == 0 {
		pcm.Channels = n.cfg.Output.Channels
	}
	if pcm.Format == "" {
		pcm.Format = n.cfg.Output.PCMFormat
	}

	if err := n.validatePCMShape(pcm); err != nil {
		return nil, 0, 0, "", err
	}
	if pcm.SampleRate == n.cfg.Output.SampleRate {
		return pcm.Data, n.cfg.Output.SampleRate, n.cfg.Output.Channels, n.cfg.Output.PCMFormat, nil
	}

	resampler := NewResampler(pcm.SampleRate, n.cfg.Output.SampleRate)
	return resampler.ProcessBytes(pcm.Data), n.cfg.Output.SampleRate, n.cfg.Output.Channels, n.cfg.Output.PCMFormat, nil
}

func (n *Normalizer) validatePCMShape(pcm PCMData) error {
	if pcm.Format != PCMFormatS16LE {
		return fmt.Errorf("unsupported PCM format %q", pcm.Format)
	}
	if n.cfg.Output.PCMFormat != PCMFormatS16LE {
		return fmt.Errorf("unsupported output PCM format %q", n.cfg.Output.PCMFormat)
	}
	if pcm.Channels != n.cfg.Output.Channels {
		return fmt.Errorf("unsupported PCM channel conversion %d -> %d", pcm.Channels, n.cfg.Output.Channels)
	}
	if pcm.SampleRate <= 0 {
		return fmt.Errorf("sample rate must be positive")
	}
	return nil
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
