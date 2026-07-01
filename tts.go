// Package tts exposes the public TTS platform API.
package tts

import internal "github.com/caitunai/tts/internal/tts"

type Service = internal.Service
type Provider = internal.Provider
type Session = internal.Session
type ProviderSession = internal.ProviderSession
type ProviderRegistry = internal.ProviderRegistry
type DefaultService = internal.DefaultService

type SynthesizeRequest = internal.SynthesizeRequest
type OpenSessionRequest = internal.OpenSessionRequest
type SegmentRequest = internal.SegmentRequest
type ReferenceAudio = internal.ReferenceAudio
type ProviderSynthesizeRequest = internal.ProviderSynthesizeRequest
type ProviderOpenSessionRequest = internal.ProviderOpenSessionRequest
type ProviderSegmentRequest = internal.ProviderSegmentRequest

type TransportType = internal.TransportType
type ProviderCapabilities = internal.ProviderCapabilities
type ServiceCapabilities = internal.ServiceCapabilities
type VoiceInfo = internal.VoiceInfo
type LanguageInfo = internal.LanguageInfo

type EventType = internal.EventType
type Event = internal.Event
type ProviderEventType = internal.ProviderEventType
type ProviderEvent = internal.ProviderEvent
type ProviderAudioChunk = internal.ProviderAudioChunk

type ErrorCode = internal.ErrorCode
type Error = internal.Error

type SessionState = internal.SessionState
type SegmentState = internal.SegmentState

const (
	TransportHTTP      = internal.TransportHTTP
	TransportWebSocket = internal.TransportWebSocket
)

const (
	EventSessionStart = internal.EventSessionStart
	EventSegmentStart = internal.EventSegmentStart
	EventAudioFrame   = internal.EventAudioFrame
	EventSegmentEnd   = internal.EventSegmentEnd
	EventSessionEnd   = internal.EventSessionEnd
	EventError        = internal.EventError
)

const (
	ProviderEventSessionStart = internal.ProviderEventSessionStart
	ProviderEventSegmentStart = internal.ProviderEventSegmentStart
	ProviderEventAudio        = internal.ProviderEventAudio
	ProviderEventSegmentEnd   = internal.ProviderEventSegmentEnd
	ProviderEventSessionEnd   = internal.ProviderEventSessionEnd
	ProviderEventError        = internal.ProviderEventError
)

const (
	ErrUnsupportedProvider    = internal.ErrUnsupportedProvider
	ErrUnsupportedFeature     = internal.ErrUnsupportedFeature
	ErrUnsupportedCodec       = internal.ErrUnsupportedCodec
	ErrUnsupportedVoice       = internal.ErrUnsupportedVoice
	ErrUnsupportedLanguage    = internal.ErrUnsupportedLanguage
	ErrInvalidGuidanceText    = internal.ErrInvalidGuidanceText
	ErrInvalidReferenceAudio  = internal.ErrInvalidReferenceAudio
	ErrReferenceAudioTooLarge = internal.ErrReferenceAudioTooLarge

	ErrProviderUnavailable = internal.ErrProviderUnavailable
	ErrProviderTimeout     = internal.ErrProviderTimeout
	ErrProviderAuthFailed  = internal.ErrProviderAuthFailed
	ErrProviderRateLimited = internal.ErrProviderRateLimited

	ErrSessionClosed = internal.ErrSessionClosed
	ErrSegmentFailed = internal.ErrSegmentFailed

	ErrAudioDecodeFailed    = internal.ErrAudioDecodeFailed
	ErrAudioNormalizeFailed = internal.ErrAudioNormalizeFailed

	ErrInternal = internal.ErrInternal
)

const (
	SessionStateIdle         = internal.SessionStateIdle
	SessionStateOpening      = internal.SessionStateOpening
	SessionStateReady        = internal.SessionStateReady
	SessionStateSynthesizing = internal.SessionStateSynthesizing
	SessionStateFinishing    = internal.SessionStateFinishing
	SessionStateClosed       = internal.SessionStateClosed
	SessionStateFailed       = internal.SessionStateFailed
)

const (
	SegmentStatePending        = internal.SegmentStatePending
	SegmentStateSentToProvider = internal.SegmentStateSentToProvider
	SegmentStateReceivingAudio = internal.SegmentStateReceivingAudio
	SegmentStateEnded          = internal.SegmentStateEnded
	SegmentStateFailed         = internal.SegmentStateFailed
)

func NewService(name string, registry ProviderRegistry) *DefaultService {
	return internal.NewService(name, registry)
}
