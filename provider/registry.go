// Package provider exposes the public provider registry.
package provider

import (
	"context"

	tts "github.com/caitunai/tts"
	internal "github.com/caitunai/tts/internal/provider"
)

type Registry struct {
	inner *internal.Registry
}

func NewRegistry() *Registry {
	return &Registry{inner: internal.NewRegistry()}
}

func (r *Registry) Register(provider tts.Provider) error {
	return r.registry().Register(provider)
}

func (r *Registry) Get(name string) (tts.Provider, bool) {
	return r.registry().Get(name)
}

func (r *Registry) List() []tts.Provider {
	return r.registry().List()
}

func (r *Registry) Capabilities(ctx context.Context) ([]*tts.ProviderCapabilities, error) {
	return r.registry().Capabilities(ctx)
}

func (r *Registry) registry() *internal.Registry {
	if r.inner == nil {
		r.inner = internal.NewRegistry()
	}
	return r.inner
}
