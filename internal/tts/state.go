package tts

// SessionState describes the lifecycle state of a TTS session.
type SessionState string

const (
	SessionStateIdle         SessionState = "idle"
	SessionStateOpening      SessionState = "opening"
	SessionStateReady        SessionState = "ready"
	SessionStateSynthesizing SessionState = "synthesizing"
	SessionStateFinishing    SessionState = "finishing"
	SessionStateClosed       SessionState = "closed"
	SessionStateFailed       SessionState = "failed"
)

// SegmentState describes the lifecycle state of a text segment.
type SegmentState string

const (
	SegmentStatePending        SegmentState = "pending"
	SegmentStateSentToProvider SegmentState = "sent_to_provider"
	SegmentStateReceivingAudio SegmentState = "receiving_audio"
	SegmentStateEnded          SegmentState = "ended"
	SegmentStateFailed         SegmentState = "failed"
)
