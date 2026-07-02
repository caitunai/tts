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
	"github.com/caitunai/tts/providers/cartesia"
)

const (
	defaultEndpoint   = "wss://api.cartesia.ai/tts/websocket"
	defaultVersion    = "2026-03-01"
	defaultModel      = "sonic-3.5"
	defaultText       = "Hello, this is Cartesia text to speech."
	defaultAppendText = ""
	defaultLanguage   = "en"
	defaultSampleRate = audio.DefaultSampleRate
	defaultOutput     = "local_cartesia_tts_16k_mono_s16le.pcm"
)

func main() {
	var (
		endpoint         = flag.String("endpoint", envOrDefault("CARTESIA_TTS_ENDPOINT", defaultEndpoint), "Cartesia TTS websocket endpoint")
		apiKey           = flag.String("key", firstEnv("CARTESIA_API_KEY", "CARTESIA_TTS_API_KEY"), "API key; defaults to CARTESIA_API_KEY or CARTESIA_TTS_API_KEY")
		accessToken      = flag.String("access-token", envOrDefault("CARTESIA_TTS_ACCESS_TOKEN", ""), "optional browser-style access_token query parameter")
		version          = flag.String("version", envOrDefault("CARTESIA_VERSION", defaultVersion), "Cartesia API version")
		model            = flag.String("model", envOrDefault("CARTESIA_TTS_MODEL", defaultModel), "Cartesia model id")
		voice            = flag.String("voice", envOrDefault("CARTESIA_TTS_VOICE", ""), "Cartesia voice id")
		language         = flag.String("language", envOrDefault("CARTESIA_TTS_LANGUAGE", defaultLanguage), "language code")
		text             = flag.String("text", defaultText, "text to synthesize")
		appendText       = flag.String("append-text", defaultAppendText, "second text segment appended to the same websocket session; empty disables the second append")
		outPath          = flag.String("out", defaultOutput, "raw PCM output path; empty disables file output")
		timeout          = flag.Duration("timeout", 60*time.Second, "session timeout")
		sampleRate       = flag.Int("sample-rate", defaultSampleRate, "raw PCM sample rate")
		maxBufferDelayMS = flag.Int("max-buffer-delay-ms", 0, "optional Cartesia max_buffer_delay_ms; 0 omits it")
		speed            = flag.Float64("speed", 0, "optional generation_config.speed; 0 omits it")
		volume           = flag.Float64("volume", 0, "optional generation_config.volume; 0 omits it")
		emotion          = flag.String("emotion", "", "optional generation_config.emotion")
	)
	flag.Parse()

	if *apiKey == "" && *accessToken == "" {
		log.Fatal("missing credentials: pass -key, -access-token, or set CARTESIA_API_KEY")
	}
	if *voice == "" {
		log.Fatal("missing voice id: pass -voice or set CARTESIA_TTS_VOICE")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:         *endpoint,
		apiKey:           *apiKey,
		accessToken:      *accessToken,
		version:          *version,
		model:            *model,
		voice:            *voice,
		language:         *language,
		text:             *text,
		appendText:       *appendText,
		outPath:          *outPath,
		sampleRate:       *sampleRate,
		maxBufferDelayMS: *maxBufferDelayMS,
		speed:            *speed,
		volume:           *volume,
		emotion:          *emotion,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint    string
	apiKey      string
	accessToken string
	version     string
	model       string
	voice       string
	language    string
	text        string
	appendText  string
	outPath     string
	sampleRate  int

	maxBufferDelayMS int
	speed            float64
	volume           float64
	emotion          string
}

func run(ctx context.Context, cfg config) error {
	cartesiaProvider, err := cartesia.NewProvider(cartesia.Config{
		Name:             cartesia.ProviderName,
		Endpoint:         cfg.endpoint,
		APIKey:           cfg.apiKey,
		AccessToken:      cfg.accessToken,
		Version:          cfg.version,
		Model:            cfg.model,
		DefaultVoice:     cfg.voice,
		DefaultLanguage:  cfg.language,
		SampleRate:       cfg.sampleRate,
		MaxBufferDelayMS: cfg.maxBufferDelayMS,
	})
	if err != nil {
		return err
	}

	registry := provider.NewRegistry()
	if err := registry.Register(cartesiaProvider); err != nil {
		return err
	}

	service := tts.NewService("local-cartesia-test", registry)

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

	sessionID := fmt.Sprintf("local_cartesia_%d", time.Now().UnixNano())
	startedAt := time.Now()

	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID: sessionID,
		Provider:  cartesia.ProviderName,
		Voice:     cfg.voice,
		Language:  cfg.language,
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
	defer func() {
		_ = session.Close()
	}()

	events := session.Events()
	if err := session.AppendText(ctx, &tts.SegmentRequest{
		SegmentID: "seg_001",
		Text:      cfg.text,
		Voice:     cfg.voice,
		Language:  cfg.language,
		Speed:     cfg.speed,
		Volume:    cfg.volume,
		Emotion:   cfg.emotion,
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
			Speed:     cfg.speed,
			Volume:    cfg.volume,
			Emotion:   cfg.emotion,
			IsLast:    true,
		}); err != nil {
			return err
		}
	}
	if err := session.Finish(ctx); err != nil {
		return err
	}

	fmt.Printf("session_id=%s endpoint=%s model=%s voice=%s language=%s sample_rate=%d\n", session.ID(), cfg.endpoint, cfg.model, cfg.voice, cfg.language, cfg.sampleRate)

	var (
		frameCount   int
		audioBytes   int
		firstFrameAt time.Time
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return printSummary(cfg, frameCount, audioBytes, time.Since(startedAt))
			}
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
					"audio_frame segment_id=%s seq=%d global_seq=%d sample_rate=%d channels=%d frame_ms=%d bytes=%d final=%v\n",
					event.SegmentID,
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
			case tts.EventSessionEnd:
				fmt.Printf("session_end session_id=%s\n", event.SessionID)
				return printSummary(cfg, frameCount, audioBytes, time.Since(startedAt))
			case tts.EventError:
				if event.Error != nil {
					return event.Error
				}
				return fmt.Errorf("received unknown TTS error event")
			}
		}
	}
}

func printSummary(cfg config, frames, bytes int, elapsed time.Duration) error {
	fmt.Printf("done frames=%d audio_bytes=%d elapsed=%s\n", frames, bytes, elapsed.Round(time.Millisecond))
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
