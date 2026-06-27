package audio

import (
	"encoding/binary"
	"testing"
)

func TestParseWAV(t *testing.T) {
	pcmBytes := []byte{1, 0, 2, 0}
	wav := makeTestWAV(t, 16000, 1, pcmBytes)

	pcm, err := ParseWAV(wav)
	if err != nil {
		t.Fatalf("ParseWAV: %v", err)
	}
	if pcm.SampleRate != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", pcm.SampleRate)
	}
	if pcm.Channels != 1 {
		t.Fatalf("Channels = %d, want 1", pcm.Channels)
	}
	if pcm.Format != PCMFormatS16LE {
		t.Fatalf("Format = %q, want %q", pcm.Format, PCMFormatS16LE)
	}
	if string(pcm.Data) != string(pcmBytes) {
		t.Fatalf("Data = %v, want %v", pcm.Data, pcmBytes)
	}
}

func makeTestWAV(t *testing.T, sampleRate, channels int, pcm []byte) []byte {
	t.Helper()

	fmtChunk := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtChunk[0:2], wavAudioFormatPCM)
	binary.LittleEndian.PutUint16(fmtChunk[2:4], uint16(channels))
	binary.LittleEndian.PutUint32(fmtChunk[4:8], uint32(sampleRate))
	byteRate := sampleRate * channels * s16leBytesPerSample
	binary.LittleEndian.PutUint32(fmtChunk[8:12], uint32(byteRate))
	blockAlign := channels * s16leBytesPerSample
	binary.LittleEndian.PutUint16(fmtChunk[12:14], uint16(blockAlign))
	binary.LittleEndian.PutUint16(fmtChunk[14:16], 16)

	body := make([]byte, 0, 4+8+len(fmtChunk)+8+len(pcm))
	body = append(body, []byte("WAVE")...)
	body = appendChunk(body, "fmt ", fmtChunk)
	body = appendChunk(body, "data", pcm)

	wav := make([]byte, 0, 8+len(body))
	wav = append(wav, []byte("RIFF")...)
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, uint32(len(body)))
	wav = append(wav, size...)
	wav = append(wav, body...)
	return wav
}

func appendChunk(dst []byte, id string, data []byte) []byte {
	dst = append(dst, []byte(id)...)
	size := make([]byte, 4)
	binary.LittleEndian.PutUint32(size, uint32(len(data)))
	dst = append(dst, size...)
	dst = append(dst, data...)
	if len(data)%2 == 1 {
		dst = append(dst, 0)
	}
	return dst
}
