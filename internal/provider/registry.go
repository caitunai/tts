package provider

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/caitunai/tts/internal/tts"
)

// Registry stores TTS providers by name.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]tts.Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]tts.Provider),
	}
}

// Register adds a provider to the registry.
func (r *Registry) Register(provider tts.Provider) error {
	if provider == nil {
		return &tts.Error{
			Code:    tts.ErrUnsupportedProvider,
			Message: "provider is nil",
		}
	}

	name := provider.Name()
	if name == "" {
		return &tts.Error{
			Code:    tts.ErrUnsupportedProvider,
			Message: "provider name is required",
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.providers == nil {
		r.providers = make(map[string]tts.Provider)
	}

	if _, exists := r.providers[name]; exists {
		return &tts.Error{
			Code:     tts.ErrUnsupportedProvider,
			Message:  fmt.Sprintf("provider %q is already registered", name),
			Provider: name,
		}
	}

	r.providers[name] = provider
	return nil
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (tts.Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, ok := r.providers[name]
	return provider, ok
}

// List returns all registered providers sorted by name.
func (r *Registry) List() []tts.Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)

	providers := make([]tts.Provider, 0, len(names))
	for _, name := range names {
		providers = append(providers, r.providers[name])
	}
	return providers
}

// Capabilities returns the capabilities for all registered providers.
func (r *Registry) Capabilities(ctx context.Context) ([]*tts.ProviderCapabilities, error) {
	providers := r.List()
	caps := make([]*tts.ProviderCapabilities, 0, len(providers))

	for _, provider := range providers {
		providerCaps, err := provider.Capabilities(ctx)
		if err != nil {
			return nil, err
		}
		if providerCaps == nil {
			providerCaps = &tts.ProviderCapabilities{}
		}

		copied := *providerCaps
		if copied.Name == "" {
			copied.Name = provider.Name()
		}
		caps = append(caps, &copied)
	}

	return caps, nil
}
