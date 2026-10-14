package qwentts

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/caitunai/tts/internal/audio"
)

const maxWAVHeaderBytes = 1 << 20

// audioStreamDecoder distinguishes raw PCM from a streaming RIFF/WAVE header.
// Its state belongs to one synthesis request, since SSE boundaries need not
// coincide with WAV header or chunk boundaries.
type audioStreamDecoder struct {
	pcm         audio.PCMData
	header      []byte
	offset      int
	wav         bool
	ready       bool
	formatFound bool
	remainder   []byte
}

func newAudioStreamDecoder(sampleRate int) *audioStreamDecoder {
	return &audioStreamDecoder{pcm: audio.PCMData{
		SampleRate: sampleRate,
		Channels:   audio.DefaultChannels,
		Format:     audio.PCMFormatS16LE,
	}}
}

func (d *audioStreamDecoder) Push(data []byte) (audio.PCMData, error) {
	if d.ready {
		return d.output(data), nil
	}
	d.header = append(d.header, data...)
	if !d.wav {
		if len(d.header) < 4 && bytes.HasPrefix([]byte("RIFF"), d.header) {
			return audio.PCMData{}, nil
		}
		if !bytes.HasPrefix(d.header, []byte("RIFF")) {
			d.ready = true
			return d.releaseHeader(0), nil
		}
		if len(d.header) < 12 {
			return audio.PCMData{}, nil
		}
		if string(d.header[8:12]) != "WAVE" {
			return audio.PCMData{}, fmt.Errorf("invalid RIFF/WAVE header")
		}
		d.wav = true
		d.offset = 12
	}

	for len(d.header)-d.offset >= 8 {
		id := string(d.header[d.offset : d.offset+4])
		size := uint64(binary.LittleEndian.Uint32(d.header[d.offset+4 : d.offset+8]))
		start := d.offset + 8
		if id == "data" {
			if !d.formatFound {
				return audio.PCMData{}, fmt.Errorf("wav data chunk precedes fmt chunk")
			}
			// Qwen's streaming WAV sizes can be estimates, so SSE EOF, rather
			// than the RIFF/data length fields, determines the end of audio.
			d.ready = true
			return d.releaseHeader(start), nil
		}
		end := uint64(start) + size + size%2
		if end > maxWAVHeaderBytes {
			return audio.PCMData{}, fmt.Errorf("wav header exceeds %d bytes", maxWAVHeaderBytes)
		}
		if end > uint64(len(d.header)) {
			return audio.PCMData{}, nil
		}
		if id == "fmt " {
			chunk := d.header[start : uint64(start)+size]
			if len(chunk) < 16 {
				return audio.PCMData{}, fmt.Errorf("wav fmt chunk too short")
			}
			format := binary.LittleEndian.Uint16(chunk[:2])
			bits := binary.LittleEndian.Uint16(chunk[14:16])
			if format != 1 || bits != 16 {
				return audio.PCMData{}, fmt.Errorf("unsupported wav format %d, bit depth %d (expected s16le PCM)", format, bits)
			}
			d.pcm.Channels = int(binary.LittleEndian.Uint16(chunk[2:4]))
			d.pcm.SampleRate = int(binary.LittleEndian.Uint32(chunk[4:8]))
			if d.pcm.Channels == 0 || d.pcm.SampleRate == 0 {
				return audio.PCMData{}, fmt.Errorf("invalid wav sample rate or channels")
			}
			d.formatFound = true
		}
		d.offset = int(end)
	}
	return audio.PCMData{}, nil
}

func (d *audioStreamDecoder) Finish() (audio.PCMData, error) {
	if len(d.remainder) > 0 {
		return audio.PCMData{}, fmt.Errorf("incomplete wav PCM sample")
	}
	if d.ready || len(d.header) == 0 {
		return audio.PCMData{}, nil
	}
	if d.wav || bytes.HasPrefix(d.header, []byte("RIFF")) {
		return audio.PCMData{}, fmt.Errorf("incomplete wav header")
	}
	return d.releaseHeader(0), nil
}

func (d *audioStreamDecoder) output(data []byte) audio.PCMData {
	if d.wav {
		if len(d.remainder) > 0 {
			data = append(d.remainder, data...)
			d.remainder = nil
		}
		aligned := len(data) / (d.pcm.Channels * 2) * (d.pcm.Channels * 2)
		d.remainder = bytes.Clone(data[aligned:])
		data = data[:aligned]
	}
	pcm := d.pcm
	pcm.Data = data
	return pcm
}

func (d *audioStreamDecoder) releaseHeader(offset int) audio.PCMData {
	data := d.header[offset:]
	d.header = nil
	return d.output(data)
}
