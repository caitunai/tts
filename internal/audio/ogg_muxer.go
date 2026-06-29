package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	oggHeaderTypeContinuation = 0x01
	oggHeaderTypeBOS          = 0x02
	oggHeaderTypeEOS          = 0x04

	defaultOpusPreSkip = 312
)

// OggOpusMuxer wraps raw Opus packets into a minimal Ogg Opus stream.
type OggOpusMuxer struct {
	serialNumber uint32
	seq          uint32
	granule      uint64
	wroteHeaders bool
}

// NewOggOpusMuxer creates a muxer for mono 48 kHz Opus streams.
func NewOggOpusMuxer() *OggOpusMuxer {
	return &OggOpusMuxer{
		serialNumber: uint32(time.Now().UnixNano()),
	}
}

// WritePacket writes one raw Opus packet, writing Opus headers first if needed.
func (m *OggOpusMuxer) WritePacket(w io.Writer, packet []byte) error {
	if len(packet) == 0 {
		return nil
	}
	if !m.wroteHeaders {
		if err := m.writeHeaders(w); err != nil {
			return err
		}
	}

	m.granule += uint64(opusPacketDurationSamples(packet))
	return m.writePage(w, 0, m.granule, packet)
}

// Finish writes an empty EOS page. It is safe to call multiple times.
func (m *OggOpusMuxer) Finish(w io.Writer) error {
	if !m.wroteHeaders {
		if err := m.writeHeaders(w); err != nil {
			return err
		}
	}
	return m.writePage(w, oggHeaderTypeEOS, m.granule, nil)
}

func (m *OggOpusMuxer) writeHeaders(w io.Writer) error {
	if err := m.writePage(w, oggHeaderTypeBOS, 0, opusHeadPacket()); err != nil {
		return err
	}
	if err := m.writePage(w, 0, 0, opusTagsPacket("github.com/caitunai/tts")); err != nil {
		return err
	}
	m.wroteHeaders = true
	return nil
}

func (m *OggOpusMuxer) writePage(w io.Writer, headerType byte, granule uint64, packet []byte) error {
	lacing, err := packetLacing(packet)
	if err != nil {
		return err
	}

	page := make([]byte, 0, oggFixedHeaderLen+len(lacing)+len(packet))
	header := make([]byte, oggFixedHeaderLen)
	copy(header[:4], oggCapturePattern)
	header[4] = 0
	header[5] = headerType
	binary.LittleEndian.PutUint64(header[6:14], granule)
	binary.LittleEndian.PutUint32(header[14:18], m.serialNumber)
	binary.LittleEndian.PutUint32(header[18:22], m.seq)
	header[26] = byte(len(lacing))

	page = append(page, header...)
	page = append(page, lacing...)
	page = append(page, packet...)
	checksum := oggChecksum(page)
	binary.LittleEndian.PutUint32(page[22:26], checksum)

	if _, err := w.Write(page); err != nil {
		return err
	}
	m.seq++
	return nil
}

func packetLacing(packet []byte) ([]byte, error) {
	if len(packet) == 0 {
		return nil, nil
	}

	lacing := make([]byte, 0, len(packet)/255+1)
	remaining := len(packet)
	for remaining >= 255 {
		lacing = append(lacing, 255)
		remaining -= 255
	}
	lacing = append(lacing, byte(remaining))
	if len(lacing) > oggMaxSegments {
		return nil, fmt.Errorf("opus packet too large for one ogg page: %d bytes", len(packet))
	}
	return lacing, nil
}

func opusHeadPacket() []byte {
	packet := make([]byte, 19)
	copy(packet[:8], "OpusHead")
	packet[8] = 1
	packet[9] = byte(DefaultChannels)
	binary.LittleEndian.PutUint16(packet[10:12], defaultOpusPreSkip)
	binary.LittleEndian.PutUint32(packet[12:16], OpusSampleRate)
	return packet
}

func opusTagsPacket(vendor string) []byte {
	vendorBytes := []byte(vendor)
	packet := make([]byte, 8+4+len(vendorBytes)+4)
	copy(packet[:8], "OpusTags")
	binary.LittleEndian.PutUint32(packet[8:12], uint32(len(vendorBytes)))
	copy(packet[12:12+len(vendorBytes)], vendorBytes)
	return packet
}

func opusPacketDurationSamples(packet []byte) int {
	if len(packet) == 0 {
		return OpusSampleRate * DefaultFrameMS / 1000
	}

	frames := 1
	switch packet[0] & 0x03 {
	case 0:
		frames = 1
	case 1, 2:
		frames = 2
	case 3:
		if len(packet) > 1 {
			frames = int(packet[1] & 0x3f)
		}
	}
	if frames <= 0 {
		frames = 1
	}
	return frames * opusFrameDurationSamples(packet[0])
}

func opusFrameDurationSamples(toc byte) int {
	config := toc >> 3
	switch {
	case config <= 11:
		switch config % 4 {
		case 0:
			return 480
		case 1:
			return 960
		case 2:
			return 1920
		default:
			return 2880
		}
	case config <= 15:
		if config%2 == 0 {
			return 480
		}
		return 960
	default:
		switch config % 4 {
		case 0:
			return 120
		case 1:
			return 240
		case 2:
			return 480
		default:
			return 960
		}
	}
}

func oggChecksum(page []byte) uint32 {
	var crc uint32
	for _, value := range page {
		crc = (crc << 8) ^ oggCRC32Table[byte(crc>>24)^value]
	}
	return crc
}

var oggCRC32Table = func() [256]uint32 {
	var table [256]uint32
	for i := range table {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = (r << 1) ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		table[i] = r
	}
	return table
}()
