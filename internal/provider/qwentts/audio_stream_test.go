package qwentts

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/caitunai/tts/internal/audio"
)

func TestAudioStreamDecoderWAVAtEverySplit(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	wav := makeTestWAV(pcm, 16000, 2, true)
	for split := range len(wav) + 1 {
		decoder := newAudioStreamDecoder(24000)
		var got []byte
		for _, data := range [][]byte{wav[:split], wav[split:]} {
			decoded, err := decoder.Push(data)
			if err != nil {
				t.Fatalf("split %d: Push: %v", split, err)
			}
			if len(decoded.Data) > 0 {
				if decoded.SampleRate != 16000 || decoded.Channels != 2 || decoded.Format != audio.PCMFormatS16LE {
					t.Fatalf("split %d: unexpected WAV parameters: %+v", split, decoded)
				}
				got = append(got, decoded.Data...)
			}
		}
		if _, err := decoder.Finish(); err != nil {
			t.Fatalf("split %d: Finish: %v", split, err)
		}
		if !bytes.Equal(got, pcm) {
			t.Fatalf("split %d: PCM = %v, want %v", split, got, pcm)
		}
	}
}

func TestAudioStreamDecoderRawPCM(t *testing.T) {
	// Raw PCM may start with a partial RIFF signature; it must survive sniffing.
	for _, input := range [][]byte{{'R', 'I'}, {'R', 'I', 'x', 0, 5, 0}, {1, 0, 2, 0}} {
		decoder := newAudioStreamDecoder(24000)
		var got []byte
		for _, b := range input {
			decoded, err := decoder.Push([]byte{b})
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, decoded.Data...)
		}
		tail, err := decoder.Finish()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, tail.Data...)
		if !bytes.Equal(got, input) {
			t.Fatalf("PCM = %v, want %v", got, input)
		}
	}
}

func TestAudioStreamDecoderRejectsInvalidWAV(t *testing.T) {
	valid := makeTestWAV([]byte{1, 0}, 24000, 1, false)
	unsupported := bytes.Clone(valid)
	binary.LittleEndian.PutUint16(unsupported[20:22], 3)
	for _, input := range [][]byte{valid[:30], unsupported, []byte("RIFF1234NOPE")} {
		decoder := newAudioStreamDecoder(24000)
		decoded, err := decoder.Push(input)
		if err == nil {
			_, err = decoder.Finish()
		}
		if err == nil {
			t.Fatalf("input %v: expected WAV decode error", input)
		}
		if len(decoded.Data) != 0 {
			t.Fatal("invalid WAV must not be emitted as PCM")
		}
	}
}

func makeTestWAV(pcm []byte, sampleRate, channels int, extraChunk bool) []byte {
	header := make([]byte, 12)
	copy(header, "RIFF")
	copy(header[8:], "WAVE")
	format := make([]byte, 16)
	binary.LittleEndian.PutUint16(format[:2], 1)
	binary.LittleEndian.PutUint16(format[2:4], uint16(channels))
	binary.LittleEndian.PutUint32(format[4:8], uint32(sampleRate))
	binary.LittleEndian.PutUint32(format[8:12], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(format[12:14], uint16(channels*2))
	binary.LittleEndian.PutUint16(format[14:16], 16)
	header = appendWAVChunk(header, "fmt ", format)
	if extraChunk {
		header = appendWAVChunk(header, "JUNK", []byte{1, 2, 3})
	}
	header = appendWAVChunk(header, "data", pcm)
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(header)-8))
	return header
}

func appendWAVChunk(dst []byte, id string, data []byte) []byte {
	header := make([]byte, 8)
	copy(header, id)
	binary.LittleEndian.PutUint32(header[4:], uint32(len(data)))
	dst = append(dst, header...)
	dst = append(dst, data...)
	if len(data)%2 != 0 {
		dst = append(dst, 0)
	}
	return dst
}
