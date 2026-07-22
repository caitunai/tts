package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/provider/doubaotts"
	"github.com/caitunai/tts/internal/tts"
)

const (
	defaultEndpoint = "wss://openspeech.bytedance.com/api/v3/tts/bidirection"
	defaultText     = "你好，今天天气怎么样呢？"
	defaultAppend   = ""
	defaultOutput   = "local_doubao_tts.ogg"
)

func main() {
	var (
		endpoint           = flag.String("endpoint", envOrDefault("DOUBAO_TTS_ENDPOINT", defaultEndpoint), "Doubao bidirectional TTS websocket endpoint")
		apiKey             = flag.String("key", envOrDefault("DOUBAO_TTS_API_KEY", ""), "X-Api-Key; defaults to DOUBAO_TTS_API_KEY")
		appID              = flag.String("app-id", envOrDefault("DOUBAO_TTS_APP_ID", ""), "legacy X-Api-App-Key; optional")
		accessKey          = flag.String("access-key", envOrDefault("DOUBAO_TTS_ACCESS_KEY", ""), "legacy X-Api-Access-Key; optional")
		resourceID         = flag.String("resource-id", envOrDefault("DOUBAO_TTS_RESOURCE_ID", "seed-tts-2.0"), "X-Api-Resource-Id")
		text               = flag.String("text", defaultText, "text to synthesize")
		appendText         = flag.String("append-text", defaultAppend, "second text segment appended to the same realtime session; empty disables the second append")
		voice              = flag.String("voice", envOrDefault("DOUBAO_TTS_VOICE", ""), "speaker/voice id")
		guidance           = flag.String("guidance", "", "optional guidance text mapped to context_texts")
		sectionID          = flag.String("section-id", envOrDefault("DOUBAO_TTS_SECTION_ID", ""), "optional multi-round section_id")
		outPath            = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout            = flag.Duration("timeout", 60*time.Second, "session timeout")
		segmentIdleTimeout = flag.Duration("segment-idle-timeout", 800*time.Millisecond, "idle window before emitting segment_end when Doubao sends no explicit final audio marker")
	)
	flag.Parse()

	if *apiKey == "" && (*appID == "" || *accessKey == "") {
		log.Fatal("missing credentials: pass -key or both -app-id and -access-key")
	}
	if *voice == "" {
		log.Fatal("missing voice: pass -voice or set DOUBAO_TTS_VOICE")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:           *endpoint,
		apiKey:             *apiKey,
		appID:              *appID,
		accessKey:          *accessKey,
		resourceID:         *resourceID,
		text:               *text,
		appendText:         *appendText,
		voice:              *voice,
		guidance:           *guidance,
		sectionID:          *sectionID,
		outPath:            *outPath,
		segmentIdleTimeout: *segmentIdleTimeout,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint           string
	apiKey             string
	appID              string
	accessKey          string
	resourceID         string
	text               string
	appendText         string
	voice              string
	guidance           string
	sectionID          string
	outPath            string
	segmentIdleTimeout time.Duration
}

func run(ctx context.Context, cfg config) error {
	doubaoProvider, err := doubaotts.NewProvider(doubaotts.Config{
		Name:               "doubao",
		Endpoint:           cfg.endpoint,
		APIKey:             cfg.apiKey,
		AppID:              cfg.appID,
		AccessKey:          cfg.accessKey,
		ResourceID:         cfg.resourceID,
		DefaultVoice:       cfg.voice,
		DefaultSectionID:   cfg.sectionID,
		SegmentIdleTimeout: cfg.segmentIdleTimeout,
	})
	if err != nil {
		return err
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(doubaoProvider); err != nil {
		return err
	}

	service := tts.NewService("local-doubao-test", registry)

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

	sessionID := fmt.Sprintf("local_doubao_%d", time.Now().UnixNano())
	startedAt := time.Now()

	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID:    sessionID,
		Provider:     "doubao",
		Voice:        cfg.voice,
		GuidanceText: cfg.guidance,
		Output: audio.OutputConfig{
			PreferCodec:        audio.CodecOpus,
			SampleRate:         audio.OpusSampleRate,
			Channels:           audio.DefaultChannels,
			FrameMS:            audio.DefaultFrameMS,
			AllowOggOpusDemux:  true,
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
		SegmentID:    "seg_001",
		Text:         cfg.text,
		Voice:        cfg.voice,
		GuidanceText: cfg.guidance,
		IsLast:       cfg.appendText == "",
	}); err != nil {
		return err
	}
	if cfg.appendText != "" {
		if err := session.AppendText(ctx, &tts.SegmentRequest{
			SegmentID:    "seg_002",
			Text:         cfg.appendText,
			Voice:        cfg.voice,
			GuidanceText: cfg.guidance,
			IsLast:       true,
		}); err != nil {
			return err
		}
	}
	if err := session.Finish(ctx); err != nil {
		return err
	}

	fmt.Printf("session_id=%s endpoint=%s resource_id=%s voice=%s sample_rate=%d\n", session.ID(), cfg.endpoint, cfg.resourceID, cfg.voice, audio.OpusSampleRate)

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
				if muxer != nil {
					if err := muxer.Finish(out); err != nil {
						return err
					}
				}
				return printSummary(cfg.outPath, packetCount, audioBytes, time.Since(startedAt))
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
				fmt.Printf("session_end session_id=%s\n", event.SessionID)
				if muxer != nil {
					if err := muxer.Finish(out); err != nil {
						return err
					}
				}
				return printSummary(cfg.outPath, packetCount, audioBytes, time.Since(startedAt))
			case tts.EventError:
				if event.Error != nil {
					return event.Error
				}
				return fmt.Errorf("received unknown TTS error event")
			}
		}
	}
}

func printSummary(outPath string, packets, bytes int, elapsed time.Duration) error {
	fmt.Printf("done opus_packets=%d audio_bytes=%d elapsed=%s\n", packets, bytes, elapsed.Round(time.Millisecond))
	if outPath != "" {
		fmt.Printf("wrote_ogg_opus=%s\n", outPath)
		fmt.Printf("playback: ffplay %s\n", outPath)
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
