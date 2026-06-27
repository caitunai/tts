package tts

// ErrorCode identifies a platform error class.
type ErrorCode string

const (
	ErrUnsupportedProvider    ErrorCode = "unsupported_provider"
	ErrUnsupportedFeature     ErrorCode = "unsupported_feature"
	ErrUnsupportedCodec       ErrorCode = "unsupported_codec"
	ErrUnsupportedVoice       ErrorCode = "unsupported_voice"
	ErrUnsupportedLanguage    ErrorCode = "unsupported_language"
	ErrInvalidGuidanceText    ErrorCode = "invalid_guidance_text"
	ErrInvalidReferenceAudio  ErrorCode = "invalid_reference_audio"
	ErrReferenceAudioTooLarge ErrorCode = "reference_audio_too_large"

	ErrProviderUnavailable ErrorCode = "provider_unavailable"
	ErrProviderTimeout     ErrorCode = "provider_timeout"
	ErrProviderAuthFailed  ErrorCode = "provider_auth_failed"
	ErrProviderRateLimited ErrorCode = "provider_rate_limited"

	ErrSessionClosed ErrorCode = "session_closed"
	ErrSegmentFailed ErrorCode = "segment_failed"

	ErrAudioDecodeFailed    ErrorCode = "audio_decode_failed"
	ErrAudioNormalizeFailed ErrorCode = "audio_normalize_failed"

	ErrInternal ErrorCode = "internal_error"
)

// Error is the unified error shape returned or emitted by the platform.
type Error struct {
	Code ErrorCode

	Message string

	Provider  string
	SessionID string
	SegmentID string

	Cause error

	Retryable bool
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
