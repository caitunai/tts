package tts_test

import (
	"testing"

	tts "github.com/caitunai/tts"
	"github.com/caitunai/tts/audio"
	"github.com/caitunai/tts/provider"
	"github.com/caitunai/tts/providers/deepgram"
	"github.com/caitunai/tts/providers/doubao"
	"github.com/caitunai/tts/providers/elevenlabs"
	"github.com/caitunai/tts/providers/inworld"
	"github.com/caitunai/tts/providers/microsoft"
	"github.com/caitunai/tts/providers/minimax"
	"github.com/caitunai/tts/providers/qwenhttp"
	"github.com/caitunai/tts/providers/qwenrealtime"
	"github.com/caitunai/tts/providers/vllm"
)

var _ tts.Provider = (*deepgram.Provider)(nil)
var _ tts.Provider = (*doubao.Provider)(nil)
var _ tts.Provider = (*elevenlabs.Provider)(nil)
var _ tts.Provider = (*inworld.Provider)(nil)
var _ tts.Provider = (*microsoft.Provider)(nil)
var _ tts.Provider = (*minimax.Provider)(nil)
var _ tts.Provider = (*qwenhttp.Provider)(nil)
var _ tts.Provider = (*qwenrealtime.Provider)(nil)
var _ tts.Provider = (*vllm.Provider)(nil)

func TestPublicFacadeCompiles(t *testing.T) {
	registry := provider.NewRegistry()
	elevenProvider, err := elevenlabs.NewProvider(elevenlabs.Config{
		Name:         elevenlabs.ProviderName,
		Endpoint:     "wss://example.test/v1/text-to-speech/:voice_id/stream-input",
		DefaultVoice: "voice",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if err := registry.Register(elevenProvider); err != nil {
		t.Fatalf("Register: %v", err)
	}

	service := tts.NewService("app", registry)
	if service.Name() != "app" {
		t.Fatalf("service name = %q, want app", service.Name())
	}

	output := audio.DefaultOutputConfig()
	req := tts.OpenSessionRequest{
		Provider: elevenlabs.ProviderName,
		Voice:    "voice",
		Output:   output,
	}
	frame := &audio.Frame{Codec: audio.CodecOpus, SampleRate: audio.OpusSampleRate}
	event := tts.Event{Type: tts.EventAudioFrame, Audio: frame}

	if req.Output.SampleRate != audio.DefaultSampleRate {
		t.Fatalf("sample rate = %d, want %d", req.Output.SampleRate, audio.DefaultSampleRate)
	}
	if event.Audio.Codec != audio.CodecOpus {
		t.Fatalf("codec = %q, want opus", event.Audio.Codec)
	}
}

func TestProviderNameConstantsMatchDefaults(t *testing.T) {
	tests := []struct {
		name string
		want string
		new  func() (tts.Provider, error)
	}{
		{
			name: "deepgram",
			want: deepgram.ProviderName,
			new: func() (tts.Provider, error) {
				return deepgram.NewProvider(deepgram.Config{})
			},
		},
		{
			name: "doubao",
			want: doubao.ProviderName,
			new: func() (tts.Provider, error) {
				return doubao.NewProvider(doubao.Config{})
			},
		},
		{
			name: "elevenlabs",
			want: elevenlabs.ProviderName,
			new: func() (tts.Provider, error) {
				return elevenlabs.NewProvider(elevenlabs.Config{Endpoint: "wss://example.test/:voice_id"})
			},
		},
		{
			name: "inworld",
			want: inworld.ProviderName,
			new: func() (tts.Provider, error) {
				return inworld.NewProvider(inworld.Config{})
			},
		},
		{
			name: "microsoft",
			want: microsoft.ProviderName,
			new: func() (tts.Provider, error) {
				return microsoft.NewProvider(microsoft.Config{Endpoint: "https://example.test"})
			},
		},
		{
			name: "minimax",
			want: minimax.ProviderName,
			new: func() (tts.Provider, error) {
				return minimax.NewProvider(minimax.Config{Endpoint: "wss://example.test"})
			},
		},
		{
			name: "qwenhttp",
			want: qwenhttp.ProviderName,
			new: func() (tts.Provider, error) {
				return qwenhttp.NewProvider(qwenhttp.Config{Endpoint: "https://example.test"})
			},
		},
		{
			name: "qwenrealtime",
			want: qwenrealtime.ProviderName,
			new: func() (tts.Provider, error) {
				return qwenrealtime.NewProvider(qwenrealtime.Config{Endpoint: "wss://example.test"})
			},
		},
		{
			name: "vllm",
			want: vllm.ProviderName,
			new: func() (tts.Provider, error) {
				return vllm.NewProvider(vllm.Config{Endpoint: "http://example.test"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := tt.new()
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			if provider.Name() != tt.want {
				t.Fatalf("provider name = %q, want %q", provider.Name(), tt.want)
			}
		})
	}
}
