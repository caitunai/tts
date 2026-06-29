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
	"github.com/caitunai/tts/internal/provider/minimaxtts"
	"github.com/caitunai/tts/internal/tts"
)

const (
	defaultModel    = "speech-2.8-hd"
	defaultText     = "你好，今天天气怎么样呢？"
	defaultAppend   = "这是第二段追加文本，用来验证 Minimax append text 是否生效。"
	defaultVoice    = "male-qn-qingse"
	defaultLanguage = "zh"
	defaultEmotion  = "happy"
	defaultOutput   = "local_minimax_tts_16k_mono_s16le.pcm"
)

func main() {
	var (
		endpoint   = flag.String("endpoint", envOrDefault("MINIMAX_TTS_ENDPOINT", ""), "Minimax realtime TTS websocket endpoint")
		token      = flag.String("token", firstEnv("MINIMAX_TTS_TOKEN", "MINIMAX_API_KEY"), "Bearer token; defaults to MINIMAX_TTS_TOKEN or MINIMAX_API_KEY")
		model      = flag.String("model", envOrDefault("MINIMAX_TTS_MODEL", defaultModel), "Minimax TTS model")
		text       = flag.String("text", defaultText, "first text segment")
		appendText = flag.String("append-text", defaultAppend, "second text segment appended to the same realtime session; empty disables the second append")
		voice      = flag.String("voice", defaultVoice, "voice id")
		language   = flag.String("language", defaultLanguage, "language code")
		emotion    = flag.String("emotion", defaultEmotion, "optional Minimax emotion: happy/sad/angry/fearful/disgusted/surprised/calm/fluent/whisper")
		outPath    = flag.String("out", defaultOutput, "output PCM file path; empty disables file output")
		timeout    = flag.Duration("timeout", 60*time.Second, "session timeout")
	)
	flag.Parse()

	if *endpoint == "" {
		log.Fatal("missing endpoint: pass -endpoint or set MINIMAX_TTS_ENDPOINT")
	}
	if *token == "" {
		log.Fatal("missing bearer token: pass -token or set MINIMAX_TTS_TOKEN")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:   *endpoint,
		token:      *token,
		model:      *model,
		text:       *text,
		appendText: *appendText,
		voice:      *voice,
		language:   *language,
		emotion:    *emotion,
		outPath:    *outPath,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint   string
	token      string
	model      string
	text       string
	appendText string
	voice      string
	language   string
	emotion    string
	outPath    string
}

func run(ctx context.Context, cfg config) error {
	minimaxProvider, err := minimaxtts.NewProvider(minimaxtts.Config{
		Name:            "minimax",
		Endpoint:        cfg.endpoint,
		Token:           cfg.token,
		Model:           cfg.model,
		DefaultVoice:    cfg.voice,
		DefaultLanguage: cfg.language,
	})
	if err != nil {
		return err
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(minimaxProvider); err != nil {
		return err
	}

	service := tts.NewService("local-minimax-test", registry)

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

	sessionID := fmt.Sprintf("local_minimax_%d", time.Now().UnixNano())
	startedAt := time.Now()

	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID:    sessionID,
		Provider:     "minimax",
		Voice:        cfg.voice,
		Language:     cfg.language,
		GuidanceText: cfg.emotion,
		Output: audio.OutputConfig{
			PreferCodec: audio.CodecPCM,
			SampleRate:  16000,
			Channels:    1,
			FrameMS:     20,
			PCMFormat:   audio.PCMFormatS16LE,
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

	fmt.Printf("session_id=%s endpoint=%s model=%s voice=%s language=%s emotion=%s\n", session.ID(), cfg.endpoint, cfg.model, cfg.voice, cfg.language, cfg.emotion)

	var (
		chunkCount   int
		audioBytes   int
		firstAudioAt time.Time
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return printSummary(cfg.outPath, chunkCount, audioBytes, time.Since(startedAt))
			}
			switch event.Type {
			case tts.EventSegmentStart:
				fmt.Printf("segment_start segment_id=%s\n", event.SegmentID)
			case tts.EventAudioFrame:
				if event.Audio == nil {
					return fmt.Errorf("audio event has nil frame")
				}
				if event.Audio.Codec != audio.CodecPCM {
					return fmt.Errorf("unexpected audio codec: %s", event.Audio.Codec)
				}
				if firstAudioAt.IsZero() {
					firstAudioAt = time.Now()
					fmt.Printf("first_audio_latency=%s\n", firstAudioAt.Sub(startedAt).Round(time.Millisecond))
				}
				chunkCount++
				audioBytes += len(event.Audio.Data)
				if out != nil {
					if _, err := out.Write(event.Audio.Data); err != nil {
						return err
					}
				}
				fmt.Printf(
					"pcm_frame seq=%d global_seq=%d sample_rate=%d channels=%d frame_ms=%d bytes=%d final=%v\n",
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
				return printSummary(cfg.outPath, chunkCount, audioBytes, time.Since(startedAt))
			case tts.EventError:
				if event.Error != nil {
					return event.Error
				}
				return fmt.Errorf("received unknown TTS error event")
			}
		}
	}
}

func printSummary(outPath string, chunks, bytes int, elapsed time.Duration) error {
	fmt.Printf("done pcm_frames=%d audio_bytes=%d elapsed=%s\n", chunks, bytes, elapsed.Round(time.Millisecond))
	if outPath != "" {
		fmt.Printf("wrote_pcm=%s\n", outPath)
		fmt.Printf("playback: ffplay -f s16le -ar 16000 -ac 1 %s\n", outPath)
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
