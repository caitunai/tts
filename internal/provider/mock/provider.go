// Package mock contains providers for end-to-end platform tests.
package mock

import (
	"context"
	"encoding/binary"
	"sync"

	"github.com/caitunai/tts/internal/audio"
	"github.com/caitunai/tts/internal/tts"
)

// Provider is a configurable mock TTS provider.
type Provider struct {
	name string
	caps *tts.ProviderCapabilities

	synthesize func(*tts.ProviderSynthesizeRequest) ([]*tts.ProviderEvent, error)
	session    func(*tts.ProviderOpenSessionRequest) (tts.ProviderSession, error)

	mu            sync.Mutex
	SynthesizeReq *tts.ProviderSynthesizeRequest
	OpenReq       *tts.ProviderOpenSessionRequest
}

func NewPCMProvider(name string, pcm []byte) *Provider {
	provider := &Provider{name: name}
	provider.caps = &tts.ProviderCapabilities{
		Name:              name,
		Transports:        []tts.TransportType{tts.TransportHTTP},
		SupportsStreaming: true,
		SupportsPCMOutput: true,
		OutputCodecs:      []audio.Codec{audio.CodecPCM},
		OutputContainers:  []audio.Container{audio.ContainerRaw},
		OutputSampleRates: []int{audio.DefaultSampleRate},
		OutputChannels:    []int{audio.DefaultChannels},
	}
	provider.synthesize = func(req *tts.ProviderSynthesizeRequest) ([]*tts.ProviderEvent, error) {
		return []*tts.ProviderEvent{
			segmentStart(req.RequestID, "", "seg_001"),
			audioEvent(req.RequestID, "", "seg_001", audio.CodecPCM, audio.ContainerRaw, pcm),
			segmentEnd(req.RequestID, "", "seg_001"),
		}, nil
	}
	return provider
}

func NewOggOpusProvider(name string, packet []byte) *Provider {
	provider := &Provider{name: name}
	provider.caps = &tts.ProviderCapabilities{
		Name:                  name,
		Transports:            []tts.TransportType{tts.TransportHTTP},
		SupportsStreaming:     true,
		SupportsOggOpusOutput: true,
		OutputCodecs:          []audio.Codec{audio.CodecOpus},
		OutputContainers:      []audio.Container{audio.ContainerOgg},
		OutputSampleRates:     []int{48000},
		OutputChannels:        []int{1},
	}
	provider.synthesize = func(req *tts.ProviderSynthesizeRequest) ([]*tts.ProviderEvent, error) {
		ogg := append(MakeOggPage(1, 0, [][]byte{[]byte("OpusHead")}), MakeOggPage(1, 1, [][]byte{packet})...)
		return []*tts.ProviderEvent{
			segmentStart(req.RequestID, "", "seg_001"),
			audioEvent(req.RequestID, "", "seg_001", audio.CodecOpus, audio.ContainerOgg, ogg),
			segmentEnd(req.RequestID, "", "seg_001"),
		}, nil
	}
	return provider
}

func NewSessionProvider(name string, pcm []byte) *Provider {
	provider := &Provider{name: name}
	provider.caps = &tts.ProviderCapabilities{
		Name:                    name,
		Transports:              []tts.TransportType{tts.TransportWebSocket},
		SupportsStreaming:       true,
		SupportsAppendText:      true,
		SupportsSegmentEndEvent: true,
		SupportsPCMOutput:       true,
		OutputCodecs:            []audio.Codec{audio.CodecPCM},
		OutputContainers:        []audio.Container{audio.ContainerRaw},
	}
	provider.session = func(req *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
		return newSession(req.SessionID, pcm), nil
	}
	return provider
}

func NewAdvancedInputProvider(name string) *Provider {
	provider := NewPCMProvider(name, []byte{1, 2, 3})
	provider.caps.SupportsGuidanceText = true
	provider.caps.SupportsReferenceAudio = true
	provider.caps.ReferenceAudioCodecs = []audio.Codec{audio.CodecWAV}
	provider.caps.ReferenceAudioContainers = []audio.Container{audio.ContainerWAV}
	provider.caps.MaxReferenceAudioBytes = 1024
	return provider
}

func NewErrorProvider(name string, err *tts.Error) *Provider {
	provider := &Provider{name: name}
	provider.caps = &tts.ProviderCapabilities{Name: name}
	provider.synthesize = func(*tts.ProviderSynthesizeRequest) ([]*tts.ProviderEvent, error) {
		return nil, err
	}
	return provider
}

func (p *Provider) Name() string {
	return p.name
}

func (p *Provider) Capabilities(context.Context) (*tts.ProviderCapabilities, error) {
	return p.caps, nil
}

func (p *Provider) SynthesizeOnce(_ context.Context, req *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	p.mu.Lock()
	reqCopy := *req
	p.SynthesizeReq = &reqCopy
	p.mu.Unlock()

	if p.synthesize == nil {
		return nil, &tts.Error{Code: tts.ErrUnsupportedFeature, Provider: p.name, Message: "synthesize is not supported"}
	}

	events, err := p.synthesize(req)
	if err != nil {
		return nil, err
	}

	out := make(chan *tts.ProviderEvent, len(events))
	for _, event := range events {
		out <- event
	}
	close(out)
	return out, nil
}

func (p *Provider) OpenSession(_ context.Context, req *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
	p.mu.Lock()
	reqCopy := *req
	p.OpenReq = &reqCopy
	p.mu.Unlock()

	if p.session == nil {
		return nil, &tts.Error{Code: tts.ErrUnsupportedFeature, Provider: p.name, Message: "session is not supported"}
	}
	return p.session(req)
}

func (p *Provider) LastSynthesizeRequest() *tts.ProviderSynthesizeRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.SynthesizeReq
}

func segmentStart(requestID, sessionID, segmentID string) *tts.ProviderEvent {
	return &tts.ProviderEvent{Type: tts.ProviderEventSegmentStart, RequestID: requestID, SessionID: sessionID, SegmentID: segmentID}
}

func segmentEnd(requestID, sessionID, segmentID string) *tts.ProviderEvent {
	return &tts.ProviderEvent{Type: tts.ProviderEventSegmentEnd, RequestID: requestID, SessionID: sessionID, SegmentID: segmentID}
}

func audioEvent(requestID, sessionID, segmentID string, codec audio.Codec, container audio.Container, data []byte) *tts.ProviderEvent {
	return &tts.ProviderEvent{
		Type:      tts.ProviderEventAudio,
		RequestID: requestID,
		SessionID: sessionID,
		SegmentID: segmentID,
		Audio: &tts.ProviderAudioChunk{
			Codec:      codec,
			Container:  container,
			SampleRate: audio.DefaultSampleRate,
			Channels:   audio.DefaultChannels,
			Format:     audio.PCMFormatS16LE,
			Data:       data,
		},
	}
}

// MakeOggPage builds a minimal Ogg page for test data.
func MakeOggPage(serial, seq uint32, packets [][]byte) []byte {
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

	header := make([]byte, 27)
	copy(header[:4], "OggS")
	binary.LittleEndian.PutUint64(header[6:14], uint64(len(payload)))
	binary.LittleEndian.PutUint32(header[14:18], serial)
	binary.LittleEndian.PutUint32(header[18:22], seq)
	header[26] = byte(len(lacing))

	page := append(header, lacing...)
	return append(page, payload...)
}
