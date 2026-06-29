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
	"github.com/caitunai/tts/internal/provider/elevenlabstts"
	"github.com/caitunai/tts/internal/tts"
)

const (
	defaultEndpoint = "wss://api.elevenlabs.io/v1/text-to-speech/:voice_id/stream-input"
	defaultModel    = "eleven_flash_v2_5"
	defaultText     = "Hello, how is the weather today?"
	defaultAppend   = ""
	defaultVoice    = "21m00Tcm4TlvDq8ikWAM"
	defaultLanguage = "en"
	defaultOutput   = "local_elevenlabs_tts.ogg"
)

func main() {
	var (
		endpoint   = flag.String("endpoint", envOrDefault("ELEVENLABS_TTS_ENDPOINT", defaultEndpoint), "ElevenLabs realtime TTS websocket endpoint; use :voice_id as placeholder")
		apiKey     = flag.String("key", firstEnv("ELEVENLABS_API_KEY", "ELEVENLABS_TTS_KEY"), "API key; defaults to ELEVENLABS_API_KEY or ELEVENLABS_TTS_KEY")
		model      = flag.String("model", envOrDefault("ELEVENLABS_TTS_MODEL", defaultModel), "ElevenLabs model id")
		text       = flag.String("text", defaultText, "text to synthesize")
		appendText = flag.String("append-text", defaultAppend, "second text segment appended to the same realtime session; empty disables the second append")
		voice      = flag.String("voice", envOrDefault("ELEVENLABS_TTS_VOICE", defaultVoice), "voice id")
		language   = flag.String("language", defaultLanguage, "language code carried through the platform request")
		outPath    = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout    = flag.Duration("timeout", 60*time.Second, "session timeout")
	)
	flag.Parse()

	if *apiKey == "" {
		log.Fatal("missing api key: pass -key or set ELEVENLABS_API_KEY")
	}
	if *voice == "" {
		log.Fatal("missing voice id: pass -voice or set ELEVENLABS_TTS_VOICE")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:   *endpoint,
		apiKey:     *apiKey,
		model:      *model,
		text:       *text,
		appendText: *appendText,
		voice:      *voice,
		language:   *language,
		outPath:    *outPath,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint   string
	apiKey     string
	model      string
	text       string
	appendText string
	voice      string
	language   string
	outPath    string
}

func run(ctx context.Context, cfg config) error {
	elevenProvider, err := elevenlabstts.NewProvider(elevenlabstts.Config{
		Name:            "elevenlabs",
		Endpoint:        cfg.endpoint,
		APIKey:          cfg.apiKey,
		Model:           cfg.model,
		DefaultVoice:    cfg.voice,
		DefaultLanguage: cfg.language,
	})
	if err != nil {
		return err
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(elevenProvider); err != nil {
		return err
	}

	service := tts.NewService("local-elevenlabs-test", registry)

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

	sessionID := fmt.Sprintf("local_elevenlabs_%d", time.Now().UnixNano())
	startedAt := time.Now()

	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID: sessionID,
		Provider:  "elevenlabs",
		Voice:     cfg.voice,
		Language:  cfg.language,
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

	fmt.Printf("session_id=%s endpoint=%s model=%s voice=%s sample_rate=%d\n", session.ID(), cfg.endpoint, cfg.model, cfg.voice, audio.OpusSampleRate)

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

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
