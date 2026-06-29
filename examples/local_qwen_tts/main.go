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
	"github.com/caitunai/tts/internal/provider/qwentts"
	"github.com/caitunai/tts/internal/tts"
)

const (
	defaultEndpoint = "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	defaultModel    = "qwen-tts"
	defaultText     = "你好，今天天气怎么样呢？"
	defaultVoice    = "Cherry"
	defaultLanguage = "zh"
	defaultOutput   = "local_qwen_tts_16k_mono_s16le.pcm"
)

func main() {
	var (
		endpoint = flag.String("endpoint", envOrDefault("QWEN_TTS_ENDPOINT", defaultEndpoint), "Qwen TTS endpoint")
		token    = flag.String("token", firstEnv("DASHSCOPE_API_KEY", "QWEN_TTS_TOKEN"), "Bearer token; defaults to DASHSCOPE_API_KEY or QWEN_TTS_TOKEN")
		model    = flag.String("model", envOrDefault("QWEN_TTS_MODEL", defaultModel), "Qwen TTS model")
		text     = flag.String("text", defaultText, "text to synthesize")
		voice    = flag.String("voice", defaultVoice, "voice name")
		language = flag.String("language", defaultLanguage, "language code: zh/en/de/it/pt/es/ja/ko/fr/ru/auto")
		outPath  = flag.String("out", defaultOutput, "output PCM file path; empty disables file output")
		timeout  = flag.Duration("timeout", 60*time.Second, "request timeout")
	)
	flag.Parse()

	if *token == "" {
		log.Fatal("missing bearer token: pass -token or set DASHSCOPE_API_KEY")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint: *endpoint,
		token:    *token,
		model:    *model,
		text:     *text,
		voice:    *voice,
		language: *language,
		outPath:  *outPath,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint string
	token    string
	model    string
	text     string
	voice    string
	language string
	outPath  string
}

func run(ctx context.Context, cfg config) error {
	qwenProvider, err := qwentts.NewProvider(qwentts.Config{
		Name:            "qwen",
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
	if err := registry.Register(qwenProvider); err != nil {
		return err
	}

	service := tts.NewService("local-qwen-test", registry)

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

	requestID := fmt.Sprintf("local_qwen_%d", time.Now().UnixNano())
	startedAt := time.Now()

	events, err := service.SynthesizeOnce(ctx, &tts.SynthesizeRequest{
		RequestID: requestID,
		Provider:  "qwen",
		Text:      cfg.text,
		Voice:     cfg.voice,
		Language:  cfg.language,
		Output:    audio.DefaultOutputConfig(),
	})
	if err != nil {
		return err
	}

	fmt.Printf("request_id=%s endpoint=%s model=%s voice=%s language=%s\n", requestID, cfg.endpoint, cfg.model, cfg.voice, cfg.language)

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
				"audio_frame seq=%d global_seq=%d codec=%s sample_rate=%d channels=%d frame_ms=%d bytes=%d final=%v\n",
				event.Audio.Seq,
				event.Audio.GlobalSeq,
				event.Audio.Codec,
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
	fmt.Printf("done frames=%d audio_bytes=%d elapsed=%s\n", frameCount, audioBytes, elapsed)
	if cfg.outPath != "" {
		fmt.Printf("wrote_pcm=%s\n", cfg.outPath)
		fmt.Printf("playback: ffplay -f s16le -ar 16000 -ac 1 %s\n", cfg.outPath)
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
