package tts

import (
	"context"

	"github.com/caitunai/tts/internal/audio"
)

// Service is the application-facing TTS entrypoint.
type Service interface {
	Name() string

	Capabilities(ctx context.Context) (*ServiceCapabilities, error)

	SynthesizeOnce(ctx context.Context, req *SynthesizeRequest) (<-chan *Event, error)

	OpenSession(ctx context.Context, req *OpenSessionRequest) (Session, error)
}

// Provider is implemented by provider adapters.
type Provider interface {
	Name() string

	Capabilities(ctx context.Context) (*ProviderCapabilities, error)

	SynthesizeOnce(ctx context.Context, req *ProviderSynthesizeRequest) (<-chan *ProviderEvent, error)

	OpenSession(ctx context.Context, req *ProviderOpenSessionRequest) (ProviderSession, error)
}

// Session represents one application-facing long-lived TTS session.
type Session interface {
	ID() string

	ProviderName() string

	Output() audio.OutputConfig

	AppendText(ctx context.Context, segment *SegmentRequest) error

	Finish(ctx context.Context) error

	Events() <-chan *Event

	Close() error
}

// ProviderSession represents one provider-facing long-lived TTS session.
type ProviderSession interface {
	ID() string

	AppendText(ctx context.Context, segment *ProviderSegmentRequest) error

	Finish(ctx context.Context) error

	Events() <-chan *ProviderEvent

	Close() error
}
