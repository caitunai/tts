package tts

import (
	"context"
	"testing"

	"github.com/caitunai/tts/internal/audio"
)

type fakeRegistry struct {
	providers map[string]Provider
}

func (r fakeRegistry) Get(name string) (Provider, bool) {
	provider, ok := r.providers[name]
	return provider, ok
}

func (r fakeRegistry) Capabilities(ctx context.Context) ([]*ProviderCapabilities, error) {
	caps := make([]*ProviderCapabilities, 0, len(r.providers))
	for _, provider := range r.providers {
		providerCaps, err := provider.Capabilities(ctx)
		if err != nil {
			return nil, err
		}
		caps = append(caps, providerCaps)
	}
	return caps, nil
}

type fakeServiceProvider struct {
	name            string
	caps            *ProviderCapabilities
	seenSynthReq    *ProviderSynthesizeRequest
	seenOpenReq     *ProviderOpenSessionRequest
	synthEvents     chan *ProviderEvent
	providerSession ProviderSession
}

func (p *fakeServiceProvider) Name() string {
	return p.name
}

func (p *fakeServiceProvider) Capabilities(context.Context) (*ProviderCapabilities, error) {
	if p.caps == nil {
		return &ProviderCapabilities{Name: p.name}, nil
	}
	return p.caps, nil
}

func (p *fakeServiceProvider) SynthesizeOnce(_ context.Context, req *ProviderSynthesizeRequest) (<-chan *ProviderEvent, error) {
	p.seenSynthReq = req
	if p.synthEvents != nil {
		return p.synthEvents, nil
	}
	events := make(chan *ProviderEvent)
	close(events)
	return events, nil
}

func (p *fakeServiceProvider) OpenSession(_ context.Context, req *ProviderOpenSessionRequest) (ProviderSession, error) {
	p.seenOpenReq = req
	if p.providerSession != nil {
		return p.providerSession, nil
	}
	return &fakeProviderSession{id: "provider_session"}, nil
}

type fakeProviderSession struct {
	id           string
	seenSegment  *ProviderSegmentRequest
	events       chan *ProviderEvent
	finishCalled bool
	closeCalled  bool
}

func (s *fakeProviderSession) ID() string {
	return s.id
}

func (s *fakeProviderSession) AppendText(_ context.Context, segment *ProviderSegmentRequest) error {
	s.seenSegment = segment
	return nil
}

func (s *fakeProviderSession) Finish(context.Context) error {
	s.finishCalled = true
	return nil
}

func (s *fakeProviderSession) Events() <-chan *ProviderEvent {
	if s.events != nil {
		return s.events
	}
	events := make(chan *ProviderEvent)
	close(events)
	return events
}

func (s *fakeProviderSession) Close() error {
	s.closeCalled = true
	return nil
}

func TestDefaultServiceRequiresExplicitProvider(t *testing.T) {
	service := NewService("", fakeRegistry{})

	_, err := service.SynthesizeOnce(context.Background(), &SynthesizeRequest{})
	requireTTSErrorCode(t, err, ErrUnsupportedProvider)
}

func TestDefaultServiceRejectsNilSynthesizeRequest(t *testing.T) {
	service := NewService("", fakeRegistry{})

	_, err := service.SynthesizeOnce(context.Background(), nil)
	requireTTSErrorCode(t, err, ErrInternal)
}

func TestDefaultServiceDoesNotTreatAutoAsProviderSelection(t *testing.T) {
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{
			"real": &fakeServiceProvider{name: "real"},
		},
	})

	_, err := service.SynthesizeOnce(context.Background(), &SynthesizeRequest{Provider: "auto"})
	requireTTSErrorCode(t, err, ErrUnsupportedProvider)
}

func TestDefaultServiceDispatchesSynthesizeOnce(t *testing.T) {
	provider := &fakeServiceProvider{
		name: "mock",
		caps: &ProviderCapabilities{
			Name:                     "mock",
			SupportsGuidanceText:     true,
			SupportsReferenceAudio:   true,
			SupportsOggOpusOutput:    true,
			ReferenceAudioCodecs:     []audio.Codec{audio.CodecWAV},
			ReferenceAudioContainers: []audio.Container{audio.ContainerWAV},
		},
	}
	service := NewService("svc", fakeRegistry{
		providers: map[string]Provider{"mock": provider},
	})

	events, err := service.SynthesizeOnce(context.Background(), &SynthesizeRequest{
		RequestID:    "req_001",
		Provider:     "mock",
		Text:         "hello",
		Language:     "en",
		GuidanceText: "warm",
		ReferenceAudio: &ReferenceAudio{
			Codec:     audio.CodecWAV,
			Container: audio.ContainerWAV,
			Data:      []byte("wav"),
		},
		Output: audio.OutputConfig{PreferCodec: audio.CodecOpus},
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}
	if events == nil {
		t.Fatal("events channel is nil")
	}
	if provider.seenSynthReq == nil {
		t.Fatal("provider did not receive synth request")
	}
	if provider.seenSynthReq.GuidanceText != "warm" {
		t.Fatalf("GuidanceText = %q, want warm", provider.seenSynthReq.GuidanceText)
	}
}

func TestDefaultServiceNormalizesSynthesizeLanguage(t *testing.T) {
	provider := &fakeServiceProvider{
		name: "mock",
		caps: &ProviderCapabilities{
			Name:              "mock",
			SupportsPCMOutput: true,
		},
	}
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{"mock": provider},
	})

	_, err := service.SynthesizeOnce(context.Background(), &SynthesizeRequest{
		Provider: "mock",
		Text:     "hello",
		Language: "eng-US",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}
	if provider.seenSynthReq == nil {
		t.Fatal("provider did not receive synth request")
	}
	if provider.seenSynthReq.Language != "en-US" {
		t.Fatalf("Language = %q, want en-US", provider.seenSynthReq.Language)
	}
}

func TestDefaultServiceRejectsUnsupportedGuidanceText(t *testing.T) {
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{
			"mock": &fakeServiceProvider{name: "mock"},
		},
	})

	_, err := service.SynthesizeOnce(context.Background(), &SynthesizeRequest{
		Provider:     "mock",
		Text:         "hello",
		GuidanceText: "warm",
	})
	requireTTSErrorCode(t, err, ErrUnsupportedFeature)
}

func TestDefaultServiceRejectsUnsupportedReferenceAudio(t *testing.T) {
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{
			"mock": &fakeServiceProvider{name: "mock"},
		},
	})

	_, err := service.SynthesizeOnce(context.Background(), &SynthesizeRequest{
		Provider: "mock",
		Text:     "hello",
		ReferenceAudio: &ReferenceAudio{
			Data: []byte("wav"),
		},
	})
	requireTTSErrorCode(t, err, ErrUnsupportedFeature)
}

func TestDefaultServiceOpenSessionWrapsProviderSession(t *testing.T) {
	providerSession := &fakeProviderSession{id: "sess_provider"}
	provider := &fakeServiceProvider{
		name: "mock",
		caps: &ProviderCapabilities{
			Name:               "mock",
			SupportsAppendText: true,
			SupportsPCMOutput:  true,
		},
		providerSession: providerSession,
	}
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{"mock": provider},
	})

	session, err := service.OpenSession(context.Background(), &OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "mock",
		Output:    audio.OutputConfig{PreferCodec: audio.CodecPCM},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if session.ID() != "sess_provider" {
		t.Fatalf("session ID = %q, want sess_provider", session.ID())
	}
	if session.ProviderName() != "mock" {
		t.Fatalf("ProviderName = %q, want mock", session.ProviderName())
	}

	if err := session.AppendText(context.Background(), &SegmentRequest{SegmentID: "seg_001", Text: "hello"}); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	if providerSession.seenSegment == nil {
		t.Fatal("provider session did not receive segment")
	}
}

func TestDefaultServiceNormalizesOpenSessionAndSegmentLanguage(t *testing.T) {
	providerSession := &fakeProviderSession{id: "sess_provider"}
	provider := &fakeServiceProvider{
		name: "mock",
		caps: &ProviderCapabilities{
			Name:               "mock",
			SupportsAppendText: true,
			SupportsPCMOutput:  true,
		},
		providerSession: providerSession,
	}
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{"mock": provider},
	})

	session, err := service.OpenSession(context.Background(), &OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "mock",
		Language:  "zho-Hans-CN",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if provider.seenOpenReq == nil {
		t.Fatal("provider did not receive open session request")
	}
	if provider.seenOpenReq.Language != "zh-Hans-CN" {
		t.Fatalf("open language = %q, want zh-Hans-CN", provider.seenOpenReq.Language)
	}

	if err := session.AppendText(context.Background(), &SegmentRequest{SegmentID: "seg_001", Text: "hello", Language: "jpn_JP"}); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	if providerSession.seenSegment == nil {
		t.Fatal("provider session did not receive segment")
	}
	if providerSession.seenSegment.Language != "ja-JP" {
		t.Fatalf("segment language = %q, want ja-JP", providerSession.seenSegment.Language)
	}
}

func TestDefaultServiceOpenSessionDefaultsOutputFromCapabilities(t *testing.T) {
	provider := &fakeServiceProvider{
		name: "opus",
		caps: &ProviderCapabilities{
			Name:                  "opus",
			SupportsAppendText:    true,
			SupportsOggOpusOutput: true,
			OutputCodecs:          []audio.Codec{audio.CodecOpus},
			OutputContainers:      []audio.Container{audio.ContainerOgg},
			OutputSampleRates:     []int{audio.OpusSampleRate},
			OutputChannels:        []int{audio.DefaultChannels},
		},
		providerSession: &fakeProviderSession{id: "sess_provider"},
	}
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{"opus": provider},
	})

	session, err := service.OpenSession(context.Background(), &OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "opus",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	if provider.seenOpenReq == nil {
		t.Fatal("provider did not receive open session request")
	}
	assertOpusOutput(t, provider.seenOpenReq.Output)
	assertOpusOutput(t, session.Output())
}

func TestDefaultServiceDefaultsMinimaxStyleOutputToPCM(t *testing.T) {
	provider := &fakeServiceProvider{
		name: "minimax",
		caps: &ProviderCapabilities{
			Name:               "minimax",
			SupportsAppendText: true,
			SupportsPCMOutput:  true,
			OutputCodecs:       []audio.Codec{audio.CodecMP3, audio.CodecPCM},
			OutputContainers:   []audio.Container{audio.ContainerRaw},
			OutputSampleRates:  []int{audio.DefaultSampleRate},
			OutputChannels:     []int{audio.DefaultChannels},
		},
	}
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{"minimax": provider},
	})

	session, err := service.OpenSession(context.Background(), &OpenSessionRequest{
		SessionID: "sess_001",
		Provider:  "minimax",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	if provider.seenOpenReq == nil {
		t.Fatal("provider did not receive open session request")
	}
	assertPCMOutput(t, provider.seenOpenReq.Output)
	assertPCMOutput(t, session.Output())
}

func TestDefaultServiceSynthesizeOnceDefaultsOutputFromCapabilities(t *testing.T) {
	provider := &fakeServiceProvider{
		name: "pcm",
		caps: &ProviderCapabilities{
			Name:              "pcm",
			SupportsPCMOutput: true,
			OutputCodecs:      []audio.Codec{audio.CodecPCM},
			OutputContainers:  []audio.Container{audio.ContainerRaw},
			OutputSampleRates: []int{audio.DefaultSampleRate},
			OutputChannels:    []int{audio.DefaultChannels},
		},
	}
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{"pcm": provider},
	})

	_, err := service.SynthesizeOnce(context.Background(), &SynthesizeRequest{
		Provider: "pcm",
		Text:     "hello",
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	if provider.seenSynthReq == nil {
		t.Fatal("provider did not receive synthesize request")
	}
	assertPCMOutput(t, provider.seenSynthReq.Output)
}

func TestDefaultServiceRejectsSessionWhenAppendUnsupported(t *testing.T) {
	service := NewService("", fakeRegistry{
		providers: map[string]Provider{
			"mock": &fakeServiceProvider{name: "mock"},
		},
	})

	_, err := service.OpenSession(context.Background(), &OpenSessionRequest{Provider: "mock"})
	requireTTSErrorCode(t, err, ErrUnsupportedFeature)
}

func TestDefaultServiceRejectsNilOpenSessionRequest(t *testing.T) {
	service := NewService("", fakeRegistry{})

	_, err := service.OpenSession(context.Background(), nil)
	requireTTSErrorCode(t, err, ErrInternal)
}

func assertOpusOutput(t *testing.T, output audio.OutputConfig) {
	t.Helper()
	if output.PreferCodec != audio.CodecOpus {
		t.Fatalf("PreferCodec = %q, want opus", output.PreferCodec)
	}
	if output.SampleRate != audio.OpusSampleRate {
		t.Fatalf("SampleRate = %d, want %d", output.SampleRate, audio.OpusSampleRate)
	}
	if !output.AllowOggOpusDemux {
		t.Fatal("AllowOggOpusDemux = false, want true")
	}
	if !output.AllowRawOpusOutput {
		t.Fatal("AllowRawOpusOutput = false, want true")
	}
}

func assertPCMOutput(t *testing.T, output audio.OutputConfig) {
	t.Helper()
	if output.PreferCodec != audio.CodecPCM {
		t.Fatalf("PreferCodec = %q, want pcm", output.PreferCodec)
	}
	if output.SampleRate != audio.DefaultSampleRate {
		t.Fatalf("SampleRate = %d, want %d", output.SampleRate, audio.DefaultSampleRate)
	}
	if output.Channels != audio.DefaultChannels {
		t.Fatalf("Channels = %d, want %d", output.Channels, audio.DefaultChannels)
	}
	if output.FrameMS != audio.DefaultFrameMS {
		t.Fatalf("FrameMS = %d, want %d", output.FrameMS, audio.DefaultFrameMS)
	}
	if output.PCMFormat != audio.PCMFormatS16LE {
		t.Fatalf("PCMFormat = %q, want s16le", output.PCMFormat)
	}
	if !output.AllowPCMFrameOutput {
		t.Fatal("AllowPCMFrameOutput = false, want true")
	}
}

func requireTTSErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()

	ttsErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error = %T, want *Error", err)
	}
	if ttsErr.Code != code {
		t.Fatalf("error code = %q, want %q", ttsErr.Code, code)
	}
}
