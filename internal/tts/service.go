package tts

import (
	"context"
	"fmt"

	"github.com/caitunai/tts/internal/audio"
)

const defaultServiceName = "tts"

// ProviderRegistry is the registry behavior required by the default service.
type ProviderRegistry interface {
	Get(name string) (Provider, bool)
	Capabilities(ctx context.Context) ([]*ProviderCapabilities, error)
}

// DefaultService is the default Service implementation.
type DefaultService struct {
	name     string
	registry ProviderRegistry
}

// NewService creates a default TTS service backed by a provider registry.
func NewService(name string, registry ProviderRegistry) *DefaultService {
	if name == "" {
		name = defaultServiceName
	}
	return &DefaultService{
		name:     name,
		registry: registry,
	}
}

func (s *DefaultService) Name() string {
	return s.name
}

func (s *DefaultService) Capabilities(ctx context.Context) (*ServiceCapabilities, error) {
	if s.registry == nil {
		return nil, internalError("provider registry is nil")
	}

	providers, err := s.registry.Capabilities(ctx)
	if err != nil {
		return nil, err
	}

	return &ServiceCapabilities{Providers: providers}, nil
}

func (s *DefaultService) SynthesizeOnce(ctx context.Context, req *SynthesizeRequest) (<-chan *Event, error) {
	if req == nil {
		return nil, internalError("synthesize request is nil")
	}
	reqCopy := *req
	reqCopy.Language = NormalizeLanguage(reqCopy.Language)

	provider, caps, err := s.providerForRequest(ctx, requestProvider(&reqCopy))
	if err != nil {
		return nil, err
	}
	output := resolveOutputConfig(reqCopy.Output, caps)
	if err := validateSynthesizeRequest(&reqCopy, caps, output); err != nil {
		return nil, err
	}

	providerEvents, err := provider.SynthesizeOnce(ctx, &ProviderSynthesizeRequest{
		RequestID:      reqCopy.RequestID,
		Text:           reqCopy.Text,
		Language:       reqCopy.Language,
		Voice:          reqCopy.Voice,
		GuidanceText:   reqCopy.GuidanceText,
		ReferenceAudio: reqCopy.ReferenceAudio,
		Output:         output,
		Options:        reqCopy.Options,
	})
	if err != nil {
		return nil, err
	}

	return providerEventsToEvents(providerEvents, output), nil
}

func (s *DefaultService) OpenSession(ctx context.Context, req *OpenSessionRequest) (Session, error) {
	if req == nil {
		return nil, internalError("open session request is nil")
	}
	reqCopy := *req
	reqCopy.Language = NormalizeLanguage(reqCopy.Language)

	provider, caps, err := s.providerForRequest(ctx, sessionProvider(&reqCopy))
	if err != nil {
		return nil, err
	}
	output := resolveOutputConfig(reqCopy.Output, caps)
	if err := validateOpenSessionRequest(&reqCopy, caps, output); err != nil {
		return nil, err
	}

	providerSession, err := provider.OpenSession(ctx, &ProviderOpenSessionRequest{
		SessionID:      reqCopy.SessionID,
		Language:       reqCopy.Language,
		Voice:          reqCopy.Voice,
		GuidanceText:   reqCopy.GuidanceText,
		ReferenceAudio: reqCopy.ReferenceAudio,
		Output:         output,
		Options:        reqCopy.Options,
	})
	if err != nil {
		return nil, err
	}

	return newProviderBackedSession(reqCopy.Provider, output, providerSession), nil
}

func (s *DefaultService) providerForRequest(ctx context.Context, providerName string) (Provider, *ProviderCapabilities, error) {
	if s.registry == nil {
		return nil, nil, internalError("provider registry is nil")
	}
	if providerName == "" {
		return nil, nil, &Error{
			Code:    ErrUnsupportedProvider,
			Message: "provider is required",
		}
	}

	provider, ok := s.registry.Get(providerName)
	if !ok {
		return nil, nil, &Error{
			Code:     ErrUnsupportedProvider,
			Message:  fmt.Sprintf("provider %q is not registered", providerName),
			Provider: providerName,
		}
	}

	caps, err := provider.Capabilities(ctx)
	if err != nil {
		return nil, nil, err
	}
	if caps == nil {
		caps = &ProviderCapabilities{Name: provider.Name()}
	}
	if caps.Name == "" {
		capsCopy := *caps
		capsCopy.Name = provider.Name()
		caps = &capsCopy
	}

	return provider, caps, nil
}

func resolveOutputConfig(output audio.OutputConfig, caps *ProviderCapabilities) audio.OutputConfig {
	if caps == nil {
		caps = &ProviderCapabilities{}
	}

	if output.PreferCodec == "" {
		output.PreferCodec = audio.CodecAuto
	}
	if output.PreferCodec == audio.CodecAuto {
		output.PreferCodec = defaultOutputCodec(caps)
	}
	if output.SampleRate == 0 {
		output.SampleRate = firstPositive(caps.OutputSampleRates, audio.DefaultSampleRate)
	}
	if output.Channels == 0 {
		output.Channels = firstPositive(caps.OutputChannels, audio.DefaultChannels)
	}
	if output.FrameMS == 0 {
		output.FrameMS = audio.DefaultFrameMS
	}
	if output.PCMFormat == "" {
		output.PCMFormat = audio.PCMFormatS16LE
	}

	switch output.PreferCodec {
	case audio.CodecOpus:
		if !output.AllowOggOpusDemux && !output.AllowRawOpusOutput && !output.AllowPCMFrameOutput {
			output.AllowOggOpusDemux = true
			output.AllowRawOpusOutput = true
		}
	case audio.CodecPCM:
		if !output.AllowOggOpusDemux && !output.AllowRawOpusOutput && !output.AllowPCMFrameOutput {
			output.AllowPCMFrameOutput = true
		}
	case audio.CodecAuto:
		if !output.AllowOggOpusDemux && !output.AllowRawOpusOutput && !output.AllowPCMFrameOutput {
			output.AllowOggOpusDemux = true
			output.AllowRawOpusOutput = true
			output.AllowPCMFrameOutput = true
		}
	}

	return output
}

func defaultOutputCodec(caps *ProviderCapabilities) audio.Codec {
	if caps.SupportsOggOpusOutput || containsCodec(caps.OutputCodecs, audio.CodecOpus) {
		return audio.CodecOpus
	}
	if caps.SupportsPCMOutput || containsCodec(caps.OutputCodecs, audio.CodecPCM) {
		return audio.CodecPCM
	}
	if len(caps.OutputCodecs) > 0 {
		return caps.OutputCodecs[0]
	}
	return audio.CodecAuto
}

func firstPositive(values []int, fallback int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return fallback
}

func validateSynthesizeRequest(req *SynthesizeRequest, caps *ProviderCapabilities, output audio.OutputConfig) error {
	return validateRequestFeatures(requestFeatureSet{
		provider:       req.Provider,
		language:       req.Language,
		voice:          req.Voice,
		guidanceText:   req.GuidanceText,
		referenceAudio: req.ReferenceAudio,
		output:         output,
	}, caps)
}

func validateOpenSessionRequest(req *OpenSessionRequest, caps *ProviderCapabilities, output audio.OutputConfig) error {
	if !caps.SupportsAppendText {
		return unsupportedFeature(req.Provider, "provider does not support append text sessions")
	}

	return validateRequestFeatures(requestFeatureSet{
		provider:       req.Provider,
		language:       req.Language,
		voice:          req.Voice,
		guidanceText:   req.GuidanceText,
		referenceAudio: req.ReferenceAudio,
		output:         output,
	}, caps)
}

type requestFeatureSet struct {
	provider       string
	language       string
	voice          string
	guidanceText   string
	referenceAudio *ReferenceAudio
	output         audio.OutputConfig
}

func validateRequestFeatures(features requestFeatureSet, caps *ProviderCapabilities) error {
	if caps == nil {
		caps = &ProviderCapabilities{}
	}

	if features.guidanceText != "" && !caps.SupportsGuidanceText {
		return unsupportedFeature(features.provider, "provider does not support guidance text")
	}

	if features.referenceAudio != nil {
		if !caps.SupportsReferenceAudio {
			return unsupportedFeature(features.provider, "provider does not support reference audio")
		}
		if err := validateReferenceAudio(features.provider, features.referenceAudio, caps); err != nil {
			return err
		}
	}

	if err := validateLanguage(features.provider, features.language, caps); err != nil {
		return err
	}
	if err := validateVoice(features.provider, features.voice, caps); err != nil {
		return err
	}
	if err := validateOutput(features.provider, features.output, caps); err != nil {
		return err
	}

	return nil
}

func validateReferenceAudio(provider string, ref *ReferenceAudio, caps *ProviderCapabilities) error {
	if len(ref.Data) == 0 && ref.URL == "" {
		return &Error{
			Code:     ErrInvalidReferenceAudio,
			Message:  "reference audio data or url is required",
			Provider: provider,
		}
	}
	if ref.URL != "" && len(ref.Data) == 0 && !caps.SupportsReferenceAudioURL {
		return unsupportedFeature(provider, "provider does not support reference audio url")
	}
	if caps.MaxReferenceAudioBytes > 0 && int64(len(ref.Data)) > caps.MaxReferenceAudioBytes {
		return &Error{
			Code:     ErrReferenceAudioTooLarge,
			Message:  "reference audio exceeds max size",
			Provider: provider,
		}
	}
	if caps.RequiresReferenceText && ref.Text == "" {
		return &Error{
			Code:     ErrInvalidReferenceAudio,
			Message:  "reference audio text is required",
			Provider: provider,
		}
	}
	if ref.Codec != "" && len(caps.ReferenceAudioCodecs) > 0 && !containsCodec(caps.ReferenceAudioCodecs, ref.Codec) {
		return &Error{
			Code:     ErrInvalidReferenceAudio,
			Message:  fmt.Sprintf("reference audio codec %q is not supported", ref.Codec),
			Provider: provider,
		}
	}
	if ref.Container != "" && len(caps.ReferenceAudioContainers) > 0 && !containsContainer(caps.ReferenceAudioContainers, ref.Container) {
		return &Error{
			Code:     ErrInvalidReferenceAudio,
			Message:  fmt.Sprintf("reference audio container %q is not supported", ref.Container),
			Provider: provider,
		}
	}

	return nil
}

func validateLanguage(provider, language string, caps *ProviderCapabilities) error {
	if language == "" || len(caps.Languages) == 0 {
		return nil
	}
	for _, supported := range caps.Languages {
		if supported.Code == language {
			return nil
		}
	}
	return &Error{
		Code:     ErrUnsupportedLanguage,
		Message:  fmt.Sprintf("language %q is not supported", language),
		Provider: provider,
	}
}

func validateVoice(provider, voice string, caps *ProviderCapabilities) error {
	if voice == "" || len(caps.Voices) == 0 {
		return nil
	}
	for _, supported := range caps.Voices {
		if supported.ID == voice {
			return nil
		}
	}
	return &Error{
		Code:     ErrUnsupportedVoice,
		Message:  fmt.Sprintf("voice %q is not supported", voice),
		Provider: provider,
	}
}

func validateOutput(provider string, output audio.OutputConfig, caps *ProviderCapabilities) error {
	switch output.PreferCodec {
	case "", audio.CodecAuto:
		return nil
	case audio.CodecOpus:
		if caps.SupportsOggOpusOutput || containsCodec(caps.OutputCodecs, audio.CodecOpus) {
			return nil
		}
	case audio.CodecPCM:
		if caps.SupportsPCMOutput || containsCodec(caps.OutputCodecs, audio.CodecPCM) {
			return nil
		}
	default:
		if containsCodec(caps.OutputCodecs, output.PreferCodec) {
			return nil
		}
	}

	return &Error{
		Code:     ErrUnsupportedCodec,
		Message:  fmt.Sprintf("output codec %q is not supported", output.PreferCodec),
		Provider: provider,
	}
}

func containsCodec(values []audio.Codec, target audio.Codec) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsContainer(values []audio.Container, target audio.Container) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func unsupportedFeature(provider, message string) *Error {
	return &Error{
		Code:     ErrUnsupportedFeature,
		Message:  message,
		Provider: provider,
	}
}

func internalError(message string) *Error {
	return &Error{
		Code:    ErrInternal,
		Message: message,
	}
}

func requestProvider(req *SynthesizeRequest) string {
	if req == nil {
		return ""
	}
	return req.Provider
}

func sessionProvider(req *OpenSessionRequest) string {
	if req == nil {
		return ""
	}
	return req.Provider
}
