// Package qwenaudiohttp exposes Qwen Audio HTTP SSE and non-streaming TTS.
package qwenaudiohttp

import (
	"context"

	tts "github.com/caitunai/tts"
	internal "github.com/caitunai/tts/internal/provider/qwenaudiohttp"
)

type Config = internal.Config
type Provider struct {
	inner *internal.Provider
}

const (
	ProviderName             = internal.ProviderName
	ModelQwenAudio30TTSFlash = internal.ModelQwenAudio30TTSFlash
	ModelQwenAudio30TTSPlus  = internal.ModelQwenAudio30TTSPlus
	ModelQwenAudio31TTSFlash = internal.ModelQwenAudio31TTSFlash
)

func NewProvider(cfg Config) (*Provider, error) {
	inner, err := internal.NewProvider(cfg)
	if err != nil {
		return nil, err
	}
	return &Provider{inner: inner}, nil
}

func (p *Provider) Name() string { return p.inner.Name() }

func (p *Provider) Capabilities(ctx context.Context) (*tts.ProviderCapabilities, error) {
	return p.inner.Capabilities(ctx)
}

func (p *Provider) SynthesizeOnce(ctx context.Context, req *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	return p.inner.SynthesizeOnce(ctx, req)
}

func (p *Provider) OpenSession(ctx context.Context, req *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
	return p.inner.OpenSession(ctx, req)
}
