// Package minimax exposes Minimax realtime WebSocket TTS.
package minimax

import (
	"context"

	tts "github.com/caitunai/tts"
	internal "github.com/caitunai/tts/internal/provider/minimaxtts"
)

type Config = internal.Config
type Emotion = internal.Emotion

const ProviderName = "minimax_tts"

const (
	EmotionUnknown   = internal.EmotionUnknown
	EmotionHappy     = internal.EmotionHappy
	EmotionSad       = internal.EmotionSad
	EmotionAngry     = internal.EmotionAngry
	EmotionFearful   = internal.EmotionFearful
	EmotionDisgusted = internal.EmotionDisgusted
	EmotionSurprised = internal.EmotionSurprised
	EmotionCalm      = internal.EmotionCalm
	EmotionFluent    = internal.EmotionFluent
	EmotionWhisper   = internal.EmotionWhisper
)

const (
	Chinese    = internal.Chinese
	Yue        = internal.Yue
	English    = internal.English
	Arabic     = internal.Arabic
	Russian    = internal.Russian
	Spanish    = internal.Spanish
	French     = internal.French
	Portuguese = internal.Portuguese
	German     = internal.German
	Turkish    = internal.Turkish
	Dutch      = internal.Dutch
	Ukrainian  = internal.Ukrainian
	Vietnamese = internal.Vietnamese
	Indonesian = internal.Indonesian
	Japanese   = internal.Japanese
	Italian    = internal.Italian
	Korean     = internal.Korean
	Thai       = internal.Thai
	Polish     = internal.Polish
	Romanian   = internal.Romanian
	Greek      = internal.Greek
	Czech      = internal.Czech
	Finnish    = internal.Finnish
	Hindi      = internal.Hindi
	Bulgarian  = internal.Bulgarian
	Danish     = internal.Danish
	Hebrew     = internal.Hebrew
	Malay      = internal.Malay
	Persian    = internal.Persian
	Slovak     = internal.Slovak
	Swedish    = internal.Swedish
	Croatian   = internal.Croatian
	Filipino   = internal.Filipino
	Hungarian  = internal.Hungarian
	Norwegian  = internal.Norwegian
	Slovenian  = internal.Slovenian
	Catalan    = internal.Catalan
	Nynorsk    = internal.Nynorsk
	Tamil      = internal.Tamil
	Afrikaans  = internal.Afrikaans
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
