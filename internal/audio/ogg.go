package audio

import (
	"encoding/binary"
	"fmt"
)

const (
	oggCapturePattern = "OggS"
	oggFixedHeaderLen = 27
	oggMaxSegments    = 255
)

// OpusPacket is one raw Opus packet extracted from an Ogg Opus stream.
type OpusPacket struct {
	Data []byte

	GranulePosition uint64
	PageSequence    uint32
	SerialNumber    uint32
}

// OggOpusDemuxer extracts raw Opus packets from Ogg pages.
type OggOpusDemuxer struct {
	buffer        []byte
	pendingPacket []byte
	lastPageSeq   *uint32
}

// Push appends bytes and returns all complete Opus packets currently available.
func (d *OggOpusDemuxer) Push(data []byte) ([]OpusPacket, error) {
	if len(data) > 0 {
		d.buffer = append(d.buffer, data...)
	}

	var packets []OpusPacket
	for {
		page, ok, err := d.nextPage()
		if err != nil {
			return nil, err
		}
		if !ok {
			return packets, nil
		}

		pagePackets, err := d.packetsFromPage(page)
		if err != nil {
			return nil, err
		}
		packets = append(packets, pagePackets...)
	}
}

// BufferedBytes returns the number of bytes currently waiting for a complete
// Ogg page.
func (d *OggOpusDemuxer) BufferedBytes() int {
	return len(d.buffer)
}

func (d *OggOpusDemuxer) nextPage() (oggPage, bool, error) {
	if len(d.buffer) < oggFixedHeaderLen {
		return oggPage{}, false, nil
	}

	if string(d.buffer[:4]) != oggCapturePattern {
		return oggPage{}, false, fmt.Errorf("invalid ogg capture pattern")
	}

	segmentCount := int(d.buffer[26])
	if segmentCount > oggMaxSegments {
		return oggPage{}, false, fmt.Errorf("invalid ogg segment count %d", segmentCount)
	}

	headerLen := oggFixedHeaderLen + segmentCount
	if len(d.buffer) < headerLen {
		return oggPage{}, false, nil
	}

	payloadLen := 0
	for _, lace := range d.buffer[oggFixedHeaderLen:headerLen] {
		payloadLen += int(lace)
	}

	pageLen := headerLen + payloadLen
	if len(d.buffer) < pageLen {
		return oggPage{}, false, nil
	}

	raw := d.buffer[:pageLen]
	page := oggPage{
		headerType:      raw[5],
		granulePosition: binary.LittleEndian.Uint64(raw[6:14]),
		serialNumber:    binary.LittleEndian.Uint32(raw[14:18]),
		pageSequence:    binary.LittleEndian.Uint32(raw[18:22]),
		lacingValues:    append([]byte(nil), raw[oggFixedHeaderLen:headerLen]...),
		payload:         append([]byte(nil), raw[headerLen:pageLen]...),
	}
	d.buffer = d.buffer[pageLen:]

	if d.lastPageSeq != nil && page.pageSequence != *d.lastPageSeq+1 {
		return oggPage{}, false, fmt.Errorf("ogg page sequence discontinuity: got %d after %d", page.pageSequence, *d.lastPageSeq)
	}
	seq := page.pageSequence
	d.lastPageSeq = &seq

	return page, true, nil
}

func (d *OggOpusDemuxer) packetsFromPage(page oggPage) ([]OpusPacket, error) {
	var packets []OpusPacket
	offset := 0

	for _, lace := range page.lacingValues {
		nextOffset := offset + int(lace)
		if nextOffset > len(page.payload) {
			return nil, fmt.Errorf("ogg lacing payload exceeds page payload")
		}

		d.pendingPacket = append(d.pendingPacket, page.payload[offset:nextOffset]...)
		offset = nextOffset

		if lace < 255 {
			packetData := make([]byte, len(d.pendingPacket))
			copy(packetData, d.pendingPacket)
			packets = append(packets, OpusPacket{
				Data:            packetData,
				GranulePosition: page.granulePosition,
				PageSequence:    page.pageSequence,
				SerialNumber:    page.serialNumber,
			})
			d.pendingPacket = d.pendingPacket[:0]
		}
	}

	if offset != len(page.payload) {
		return nil, fmt.Errorf("ogg page has unread payload")
	}

	return packets, nil
}

type oggPage struct {
	headerType      byte
	granulePosition uint64
	serialNumber    uint32
	pageSequence    uint32
	lacingValues    []byte
	payload         []byte
}
