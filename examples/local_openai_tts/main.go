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
	"github.com/caitunai/tts/providers/openai"
)

const (
	defaultEndpoint     = "https://api.openai.com/v1/audio/speech"
	defaultModel        = "gpt-4o-mini-tts"
	defaultVoice        = "coral"
	defaultText         = "Hello, this is OpenAI text to speech."
	defaultInstructions = ""
	defaultOutput       = "local_openai_tts.ogg"
)

func main() {
	var (
		endpoint      = flag.String("endpoint", envOrDefault("OPENAI_TTS_ENDPOINT", defaultEndpoint), "OpenAI Speech API endpoint")
		apiKey        = flag.String("key", firstEnv("OPENAI_API_KEY", "OPENAI_TTS_API_KEY"), "API key; defaults to OPENAI_API_KEY or OPENAI_TTS_API_KEY")
		authorization = flag.String("authorization", envOrDefault("OPENAI_TTS_AUTHORIZATION", ""), "full Authorization header; overrides -key")
		model         = flag.String("model", envOrDefault("OPENAI_TTS_MODEL", defaultModel), "OpenAI TTS model")
		voice         = flag.String("voice", envOrDefault("OPENAI_TTS_VOICE", defaultVoice), "OpenAI voice")
		text          = flag.String("text", defaultText, "text to synthesize")
		instructions  = flag.String("instructions", envOrDefault("OPENAI_TTS_INSTRUCTIONS", defaultInstructions), "optional OpenAI TTS instructions / guidance text")
		outPath       = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout       = flag.Duration("timeout", 60*time.Second, "request timeout")
		speed         = flag.Float64("speed", 0, "optional OpenAI speed, 0.25-4.0; 0 omits it")
	)
	flag.Parse()

	if *authorization == "" && *apiKey == "" {
		log.Fatal("missing api key: pass -key, -authorization, or set OPENAI_API_KEY")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:      *endpoint,
		apiKey:        *apiKey,
		authorization: *authorization,
		model:         *model,
		voice:         *voice,
		text:          *text,
		instructions:  *instructions,
		outPath:       *outPath,
		speed:         *speed,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint      string
	apiKey        string
	authorization string
	model         string
	voice         string
	text          string
	instructions  string
	outPath       string
	speed         float64
}

func run(ctx context.Context, cfg config) error {
	openAIProvider, err := openai.NewProvider(openai.Config{
		Name:                openai.ProviderName,
		Endpoint:            cfg.endpoint,
		APIKey:              cfg.apiKey,
		Authorization:       cfg.authorization,
		Model:               cfg.model,
		DefaultVoice:        cfg.voice,
		DefaultInstructions: cfg.instructions,
		Speed:               cfg.speed,
	})
	if err != nil {
		return err
	}

	registry := provider.NewRegistry()
	if err := registry.Register(openAIProvider); err != nil {
		return err
	}

	service := tts.NewService("local-openai-test", registry)

	var out *os.File
	var muxer *audio.OggOpusMuxer
	if cfg.outPath != "" {
		out, err = os.Create(cfg.outPath)
		if err != nil {
			return err
		}
		defer func() {
			_ = out.Close()
		}()
		muxer = audio.NewOggOpusMuxer()
	}

	requestID := fmt.Sprintf("local_openai_%d", time.Now().UnixNano())
	startedAt := time.Now()

	events, err := service.SynthesizeOnce(ctx, &tts.SynthesizeRequest{
		RequestID:    requestID,
		Provider:     openai.ProviderName,
		Text:         cfg.text,
		Voice:        cfg.voice,
		GuidanceText: cfg.instructions,
	})
	if err != nil {
		return err
	}

	fmt.Printf("request_id=%s endpoint=%s model=%s voice=%s sample_rate=%d\n", requestID, cfg.endpoint, cfg.model, cfg.voice, audio.OpusSampleRate)

	var (
		packetCount  int
		audioBytes   int
		firstAudioAt time.Time
	)

	for event := range events {
		switch event.Type {
		case tts.EventSegmentStart:
			fmt.Printf("segment_start segment_id=%s\n", event.SegmentID)
		case tts.EventAudioFrame:
			if event.Audio == nil {
				return fmt.Errorf("audio event has nil frame")
			}
			if event.Audio.Codec != audio.CodecOpus || event.Audio.SampleRate != audio.OpusSampleRate {
				return fmt.Errorf("unexpected audio frame: codec=%s sample_rate=%d", event.Audio.Codec, event.Audio.SampleRate)
			}
			if firstAudioAt.IsZero() {
				firstAudioAt = time.Now()
				fmt.Printf("first_audio_latency=%s\n", firstAudioAt.Sub(startedAt).Round(time.Millisecond))
			}
			packetCount++
			audioBytes += len(event.Audio.Data)
			if muxer != nil {
				if err := muxer.WritePacket(out, event.Audio.Data); err != nil {
					return err
				}
			}
			fmt.Printf(
				"opus_packet seq=%d global_seq=%d sample_rate=%d channels=%d bytes=%d final=%v\n",
				event.Audio.Seq,
				event.Audio.GlobalSeq,
				event.Audio.SampleRate,
				event.Audio.Channels,
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

	if muxer != nil {
		if err := muxer.Finish(out); err != nil {
			return err
		}
	}

	elapsed := time.Since(startedAt).Round(time.Millisecond)
	fmt.Printf("done opus_packets=%d audio_bytes=%d elapsed=%s\n", packetCount, audioBytes, elapsed)
	if cfg.outPath != "" {
		fmt.Printf("wrote_ogg_opus=%s\n", cfg.outPath)
		fmt.Printf("playback: ffplay %s\n", cfg.outPath)
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
