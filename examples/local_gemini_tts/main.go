package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	tts "github.com/caitunai/tts"
	"github.com/caitunai/tts/audio"
	"github.com/caitunai/tts/provider"
	"github.com/caitunai/tts/providers/gemini"
)

const (
	defaultEndpoint     = "https://generativelanguage.googleapis.com/v1beta/interactions"
	defaultModel        = "gemini-3.1-flash-tts-preview"
	defaultVoice        = "Kore"
	defaultText         = "Say cheerfully: Have a wonderful day!"
	defaultInstructions = ""
	defaultOutput       = "local_gemini_tts_16k_mono_s16le.pcm"
)

func main() {
	var (
		endpoint     = flag.String("endpoint", envOrDefault("GEMINI_TTS_ENDPOINT", defaultEndpoint), "Gemini Interactions API endpoint")
		apiKey       = flag.String("key", firstEnv("GEMINI_API_KEY", "GEMINI_TTS_API_KEY"), "API key; defaults to GEMINI_API_KEY or GEMINI_TTS_API_KEY")
		apiRevision  = flag.String("api-revision", envOrDefault("GEMINI_TTS_API_REVISION", "2026-05-20"), "Gemini API revision header")
		model        = flag.String("model", envOrDefault("GEMINI_TTS_MODEL", defaultModel), "Gemini TTS model")
		voice        = flag.String("voice", envOrDefault("GEMINI_TTS_VOICE", defaultVoice), "Gemini TTS voice")
		text         = flag.String("text", defaultText, "text to synthesize")
		instructions = flag.String("instructions", envOrDefault("GEMINI_TTS_INSTRUCTIONS", defaultInstructions), "optional Gemini TTS guidance text prepended to the input")
		outPath      = flag.String("out", defaultOutput, "raw PCM output path; empty disables file output")
		timeout      = flag.Duration("timeout", 60*time.Second, "request timeout")
		sampleRate   = flag.Int("sample-rate", audio.DefaultSampleRate, "output PCM sample rate")
		audioIdle    = flag.Duration("audio-idle-timeout", 500*time.Millisecond, "finish after this idle window following the last received audio chunk; 0 waits for EOF/completed event only")
	)
	flag.Parse()

	if *apiKey == "" {
		log.Fatal("missing api key: pass -key or set GEMINI_API_KEY")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:     *endpoint,
		apiKey:       *apiKey,
		apiRevision:  *apiRevision,
		model:        *model,
		voice:        *voice,
		text:         *text,
		instructions: *instructions,
		outPath:      *outPath,
		sampleRate:   *sampleRate,
		audioIdle:    *audioIdle,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint     string
	apiKey       string
	apiRevision  string
	model        string
	voice        string
	text         string
	instructions string
	outPath      string
	sampleRate   int
	audioIdle    time.Duration
}

func run(ctx context.Context, cfg config) error {
	geminiProvider, err := gemini.NewProvider(gemini.Config{
		Name:                gemini.ProviderName,
		Endpoint:            cfg.endpoint,
		APIKey:              cfg.apiKey,
		APIRevision:         cfg.apiRevision,
		Model:               cfg.model,
		DefaultVoice:        cfg.voice,
		DefaultInstructions: cfg.instructions,
		AudioIdleTimeout:    cfg.audioIdle,
	})
	if err != nil {
		return err
	}

	registry := provider.NewRegistry()
	if err := registry.Register(geminiProvider); err != nil {
		return err
	}

	service := tts.NewService("local-gemini-test", registry)

	var out *os.File
	if cfg.outPath != "" {
		out, err = os.Create(cfg.outPath)
		if err != nil {
			return err
		}
		defer func() {
			_ = out.Close()
		}()
	}

	requestID := fmt.Sprintf("local_gemini_%d", time.Now().UnixNano())
	startedAt := time.Now()

	events, err := service.SynthesizeOnce(ctx, &tts.SynthesizeRequest{
		RequestID:    requestID,
		Provider:     gemini.ProviderName,
		Text:         cfg.text,
		Voice:        cfg.voice,
		GuidanceText: cfg.instructions,
		Output: audio.OutputConfig{
			PreferCodec:         audio.CodecPCM,
			SampleRate:          cfg.sampleRate,
			Channels:            audio.DefaultChannels,
			FrameMS:             audio.DefaultFrameMS,
			PCMFormat:           audio.PCMFormatS16LE,
			AllowPCMFrameOutput: true,
		},
	})
	if err != nil {
		return err
	}

	fmt.Printf("request_id=%s endpoint=%s model=%s voice=%s sample_rate=%d\n", requestID, cfg.endpoint, cfg.model, cfg.voice, cfg.sampleRate)

	var (
		frameCount   int
		audioBytes   int
		firstFrameAt time.Time
	)

	for event := range events {
		switch event.Type {
		case tts.EventSegmentStart:
			fmt.Printf("segment_start segment_id=%s\n", event.SegmentID)
		case tts.EventAudioFrame:
			if event.Audio == nil {
				return fmt.Errorf("audio event has nil frame")
			}
			if event.Audio.Codec != audio.CodecPCM || event.Audio.SampleRate != cfg.sampleRate {
				return fmt.Errorf("unexpected audio frame: codec=%s sample_rate=%d", event.Audio.Codec, event.Audio.SampleRate)
			}
			if firstFrameAt.IsZero() {
				firstFrameAt = time.Now()
				fmt.Printf("first_audio_latency=%s\n", firstFrameAt.Sub(startedAt).Round(time.Millisecond))
			}
			frameCount++
			audioBytes += len(event.Audio.Data)
			if out != nil {
				if _, err := out.Write(event.Audio.Data); err != nil {
					return err
				}
			}
			fmt.Printf(
				"audio_frame seq=%d global_seq=%d sample_rate=%d channels=%d frame_ms=%d bytes=%d final=%v\n",
				event.Audio.Seq,
				event.Audio.GlobalSeq,
				event.Audio.SampleRate,
				event.Audio.Channels,
				event.Audio.FrameMS,
				len(event.Audio.Data),
				event.Audio.SegmentFinal,
			)
		case tts.EventSegmentEnd:
			fmt.Printf("segment_end segment_id=%s\n", event.SegmentID)
		case tts.EventError:
			if event.Error != nil {
				return event.Error
			}
			return fmt.Errorf("received unknown TTS error event")
		}
	}

	elapsed := time.Since(startedAt).Round(time.Millisecond)
	fmt.Printf("done pcm_frames=%d audio_bytes=%d elapsed=%s\n", frameCount, audioBytes, elapsed)
	if cfg.outPath != "" {
		fmt.Printf("wrote_pcm=%s\n", cfg.outPath)
		fmt.Printf("playback: ffplay -f s16le -ar %d -ac 1 %s\n", cfg.sampleRate, cfg.outPath)
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	value := os.Getenv(name)
	if value != "" {
		return value
	}
	return fallback
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
