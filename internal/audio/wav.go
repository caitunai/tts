package audio

import (
	"encoding/binary"
	"fmt"
)

const (
	wavRIFFHeaderLen  = 12
	wavChunkHeaderLen = 8
	wavAudioFormatPCM = 1
)

// PCMData contains decoded PCM bytes and their audio parameters.
type PCMData struct {
	SampleRate int
	Channels   int
	Format     PCMFormat
	Data       []byte
}

// ParseWAV parses a RIFF/WAVE file and returns s16le PCM data.
func ParseWAV(data []byte) (PCMData, error) {
	if len(data) < wavRIFFHeaderLen {
		return PCMData{}, fmt.Errorf("wav data too short")
	}
	if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return PCMData{}, fmt.Errorf("invalid wav header")
	}

	var (
		formatFound bool
		dataFound   bool
		pcm         PCMData
	)

	offset := wavRIFFHeaderLen
	for offset+wavChunkHeaderLen <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += wavChunkHeaderLen

		if chunkSize < 0 || offset+chunkSize > len(data) {
			return PCMData{}, fmt.Errorf("invalid wav chunk size")
		}

		chunk := data[offset : offset+chunkSize]
		switch chunkID {
		case "fmt ":
			if len(chunk) < 16 {
				return PCMData{}, fmt.Errorf("wav fmt chunk too short")
			}
			audioFormat := binary.LittleEndian.Uint16(chunk[0:2])
			channels := binary.LittleEndian.Uint16(chunk[2:4])
			sampleRate := binary.LittleEndian.Uint32(chunk[4:8])
			bitsPerSample := binary.LittleEndian.Uint16(chunk[14:16])

			if audioFormat != wavAudioFormatPCM {
				return PCMData{}, fmt.Errorf("unsupported wav audio format %d", audioFormat)
			}
			if bitsPerSample != 16 {
				return PCMData{}, fmt.Errorf("unsupported wav bit depth %d", bitsPerSample)
			}
			if channels == 0 || sampleRate == 0 {
				return PCMData{}, fmt.Errorf("invalid wav format")
			}

			pcm.SampleRate = int(sampleRate)
			pcm.Channels = int(channels)
			pcm.Format = PCMFormatS16LE
			formatFound = true
		case "data":
			pcm.Data = append([]byte(nil), chunk...)
			dataFound = true
		}

		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}

	if !formatFound {
		return PCMData{}, fmt.Errorf("wav fmt chunk not found")
	}
	if !dataFound {
		return PCMData{}, fmt.Errorf("wav data chunk not found")
	}

	return pcm, nil
}
