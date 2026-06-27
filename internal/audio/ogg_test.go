package audio

import (
	"encoding/binary"
	"testing"
)

func TestOggOpusDemuxerHandlesSplitPage(t *testing.T) {
	page := makeOggPage(t, 7, 0, [][]byte{[]byte("packet")})
	demuxer := &OggOpusDemuxer{}

	packets, err := demuxer.Push(page[:10])
	if err != nil {
		t.Fatalf("push partial page: %v", err)
	}
	if len(packets) != 0 {
		t.Fatalf("packets = %d, want 0", len(packets))
	}

	packets, err = demuxer.Push(page[10:])
	if err != nil {
		t.Fatalf("push rest page: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("packets = %d, want 1", len(packets))
	}
	if string(packets[0].Data) != "packet" {
		t.Fatalf("packet = %q, want packet", string(packets[0].Data))
	}
	if packets[0].PageSequence != 0 {
		t.Fatalf("PageSequence = %d, want 0", packets[0].PageSequence)
	}
}

func TestOggOpusDemuxerHandlesMultiplePagesInOneChunk(t *testing.T) {
	first := makeOggPage(t, 7, 0, [][]byte{[]byte("one")})
	second := makeOggPage(t, 7, 1, [][]byte{[]byte("two")})
	demuxer := &OggOpusDemuxer{}

	packets, err := demuxer.Push(append(first, second...))
	if err != nil {
		t.Fatalf("push pages: %v", err)
	}
	if len(packets) != 2 {
		t.Fatalf("packets = %d, want 2", len(packets))
	}
	if string(packets[0].Data) != "one" || string(packets[1].Data) != "two" {
		t.Fatalf("packets = [%q %q], want [one two]", packets[0].Data, packets[1].Data)
	}
}

func TestOggOpusDemuxerReassemblesLargePacket(t *testing.T) {
	packet := make([]byte, 300)
	for i := range packet {
		packet[i] = byte(i)
	}
	page := makeOggPage(t, 7, 0, [][]byte{packet})
	demuxer := &OggOpusDemuxer{}

	packets, err := demuxer.Push(page)
	if err != nil {
		t.Fatalf("push page: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("packets = %d, want 1", len(packets))
	}
	if len(packets[0].Data) != len(packet) {
		t.Fatalf("packet length = %d, want %d", len(packets[0].Data), len(packet))
	}
}

func makeOggPage(t *testing.T, serial, seq uint32, packets [][]byte) []byte {
	t.Helper()

	var lacing []byte
	var payload []byte
	for _, packet := range packets {
		remaining := len(packet)
		offset := 0
		for remaining >= 255 {
			lacing = append(lacing, 255)
			payload = append(payload, packet[offset:offset+255]...)
			offset += 255
			remaining -= 255
		}
		lacing = append(lacing, byte(remaining))
		payload = append(payload, packet[offset:]...)
	}

	header := make([]byte, oggFixedHeaderLen)
	copy(header[:4], "OggS")
	header[4] = 0
	header[5] = 0
	binary.LittleEndian.PutUint64(header[6:14], uint64(len(payload)))
	binary.LittleEndian.PutUint32(header[14:18], serial)
	binary.LittleEndian.PutUint32(header[18:22], seq)
	header[26] = byte(len(lacing))

	page := append(header, lacing...)
	page = append(page, payload...)
	return page
}
