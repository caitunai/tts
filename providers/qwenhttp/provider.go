// Package qwenhttp exposes Alibaba Cloud Qwen HTTP SSE TTS.
package qwenhttp

import (
	"context"

	tts "github.com/caitunai/tts"
	internal "github.com/caitunai/tts/internal/provider/qwentts"
)

type Config = internal.Config

const ProviderName = "qwen_tts"

const (
	Chinese    = internal.Chinese
	English    = internal.English
	German     = internal.German
	Italian    = internal.Italian
	Portuguese = internal.Portuguese
	Spanish    = internal.Spanish
	Japanese   = internal.Japanese
	Korean     = internal.Korean
	French     = internal.French
	Russian    = internal.Russian
	Auto       = internal.Auto
)

type Provider struct {
	inner *internal.Provider
}

func NewProvider(cfg Config) (*Provider, error) {
	inner, err := internal.NewProvider(cfg)
	if err != nil {
		return nil, err
	}
	return &Provider{inner: inner}, nil
}

func (p *Provider) Name() string {
	return p.inner.Name()
}

func (p *Provider) Capabilities(ctx context.Context) (*tts.ProviderCapabilities, error) {
	return p.inner.Capabilities(ctx)
}

func (p *Provider) SynthesizeOnce(ctx context.Context, req *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	return p.inner.SynthesizeOnce(ctx, req)
}

func (p *Provider) OpenSession(ctx context.Context, req *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
	return p.inner.OpenSession(ctx, req)
}
