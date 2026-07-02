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
	"github.com/caitunai/tts/providers/fishaudio"
)

const (
	defaultEndpoint   = "wss://api.fish.audio/v1/tts/live"
	defaultModel      = "s1"
	defaultText       = "Hello, this is Fish Audio text to speech."
	defaultAppendText = ""
	defaultLanguage   = "en"
	defaultLatency    = "normal"
	defaultOutput     = "local_fishaudio_tts.ogg"
)

func main() {
	var (
		endpoint           = flag.String("endpoint", envOrDefault("FISHAUDIO_TTS_ENDPOINT", defaultEndpoint), "Fish Audio TTS websocket endpoint")
		apiKey             = flag.String("key", firstEnv("FISHAUDIO_API_KEY", "FISHAUDIO_TTS_API_KEY"), "API key; defaults to FISHAUDIO_API_KEY or FISHAUDIO_TTS_API_KEY")
		model              = flag.String("model", envOrDefault("FISHAUDIO_TTS_MODEL", defaultModel), "Fish Audio model name, for example s1 or s2-pro")
		voice              = flag.String("voice", envOrDefault("FISHAUDIO_TTS_VOICE", ""), "Fish Audio reference_id / voice model id")
		language           = flag.String("language", envOrDefault("FISHAUDIO_TTS_LANGUAGE", defaultLanguage), "language code used by platform events")
		text               = flag.String("text", defaultText, "text to synthesize")
		appendText         = flag.String("append-text", defaultAppendText, "second text segment appended to the same websocket session; empty disables the second append")
		outPath            = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout            = flag.Duration("timeout", 60*time.Second, "session timeout")
		latency            = flag.String("latency", envOrDefault("FISHAUDIO_TTS_LATENCY", defaultLatency), "Fish Audio latency mode")
		opusBitrate        = flag.Int("opus-bitrate", -1000, "Fish Audio opus_bitrate; -1000 lets Fish Audio choose automatically")
		segmentIdleTimeout = flag.Duration("segment-idle-timeout", 800*time.Millisecond, "idle window used to emit segment_end after the last audio packet")
		chunkLength        = flag.Int("chunk-length", 0, "optional chunk_length; 0 omits it")
		speed              = flag.Float64("speed", 0, "optional prosody.speed; 0 omits it")
		volume             = flag.Float64("volume", 0, "optional prosody.volume; 0 omits it")
	)
	flag.Parse()

	if *apiKey == "" {
		log.Fatal("missing api key: pass -key or set FISHAUDIO_API_KEY")
	}
	if *voice == "" {
		log.Fatal("missing voice id: pass -voice or set FISHAUDIO_TTS_VOICE")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:           *endpoint,
		apiKey:             *apiKey,
		model:              *model,
		voice:              *voice,
		language:           *language,
		text:               *text,
		appendText:         *appendText,
		outPath:            *outPath,
		latency:            *latency,
		opusBitrate:        *opusBitrate,
		segmentIdleTimeout: *segmentIdleTimeout,
		chunkLength:        *chunkLength,
		speed:              *speed,
		volume:             *volume,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint           string
	apiKey             string
	model              string
	voice              string
	language           string
	text               string
	appendText         string
	outPath            string
	latency            string
	opusBitrate        int
	segmentIdleTimeout time.Duration
	chunkLength        int
	speed              float64
	volume             float64
}

func run(ctx context.Context, cfg config) error {
	fishProvider, err := fishaudio.NewProvider(fishaudio.Config{
		Name:               fishaudio.ProviderName,
		Endpoint:           cfg.endpoint,
		APIKey:             cfg.apiKey,
		Model:              cfg.model,
		DefaultVoice:       cfg.voice,
		DefaultLanguage:    cfg.language,
		Latency:            cfg.latency,
		OpusBitrate:        cfg.opusBitrate,
		SegmentIdleTimeout: cfg.segmentIdleTimeout,
		ChunkLength:        cfg.chunkLength,
		DefaultSpeed:       cfg.speed,
		DefaultVolume:      cfg.volume,
	})
	if err != nil {
		return err
	}

	registry := provider.NewRegistry()
	if err := registry.Register(fishProvider); err != nil {
		return err
	}

	service := tts.NewService("local-fishaudio-test", registry)

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

	sessionID := fmt.Sprintf("local_fishaudio_%d", time.Now().UnixNano())
	startedAt := time.Now()

	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID: sessionID,
		Provider:  fishaudio.ProviderName,
		Voice:     cfg.voice,
		Language:  cfg.language,
		Output: audio.OutputConfig{
			PreferCodec:        audio.CodecOpus,
			SampleRate:         audio.OpusSampleRate,
			Channels:           audio.DefaultChannels,
			FrameMS:            audio.DefaultFrameMS,
			AllowRawOpusOutput: true,
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = session.Close()
	}()

	events := session.Events()
	if err := session.AppendText(ctx, &tts.SegmentRequest{
		SegmentID: "seg_001",
		Text:      cfg.text,
		Voice:     cfg.voice,
		Language:  cfg.language,
		IsLast:    cfg.appendText == "",
	}); err != nil {
		return err
	}
	if cfg.appendText != "" {
		if err := session.AppendText(ctx, &tts.SegmentRequest{
			SegmentID: "seg_002",
			Text:      cfg.appendText,
			Voice:     cfg.voice,
			Language:  cfg.language,
			IsLast:    true,
		}); err != nil {
			return err
		}
	}
	if err := session.Finish(ctx); err != nil {
		return err
	}

	fmt.Printf("session_id=%s endpoint=%s model=%s voice=%s language=%s sample_rate=%d\n", session.ID(), cfg.endpoint, cfg.model, cfg.voice, cfg.language, audio.OpusSampleRate)

	var (
		packetCount  int
		audioBytes   int
		firstAudioAt time.Time
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return printSummary(cfg, muxer, out, packetCount, audioBytes, time.Since(startedAt))
			}
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
					"opus_packet segment_id=%s seq=%d global_seq=%d sample_rate=%d channels=%d bytes=%d final=%v\n",
					event.SegmentID,
					event.Audio.Seq,
					event.Audio.GlobalSeq,
					event.Audio.SampleRate,
					event.Audio.Channels,
					len(event.Audio.Data),
					event.Audio.SegmentFinal,
				)
			case tts.EventSegmentEnd:
				fmt.Printf("segment_end segment_id=%s\n", event.SegmentID)
			case tts.EventSessionEnd:
				return printSummary(cfg, muxer, out, packetCount, audioBytes, time.Since(startedAt))
			case tts.EventError:
				if event.Error != nil {
					return event.Error
				}
				return fmt.Errorf("received unknown TTS error event")
			}
		}
	}
}

func printSummary(cfg config, muxer *audio.OggOpusMuxer, out *os.File, packetCount, audioBytes int, elapsed time.Duration) error {
	if muxer != nil {
		if err := muxer.Finish(out); err != nil {
			return err
		}
	}

	fmt.Printf("done opus_packets=%d audio_bytes=%d elapsed=%s\n", packetCount, audioBytes, elapsed.Round(time.Millisecond))
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
