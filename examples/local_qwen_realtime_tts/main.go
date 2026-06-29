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
	"github.com/caitunai/tts/internal/provider/qwenrealtime"
	"github.com/caitunai/tts/internal/tts"
)

const (
	defaultEndpoint = "wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model="
	defaultModel    = "qwen3-tts-instruct-flash-realtime"
	defaultText     = "你好，今天天气怎么样呢？"
	defaultAppend   = "这是第二段追加文本，用来验证 append text 是否生效。"
	defaultVoice    = "Cherry"
	defaultLanguage = "zh"
	defaultOutput   = "local_qwen_realtime_tts.ogg"
)

func main() {
	var (
		endpoint     = flag.String("endpoint", envOrDefault("QWEN_REALTIME_TTS_ENDPOINT", defaultEndpoint), "Qwen realtime TTS websocket endpoint")
		token        = flag.String("token", firstEnv("DASHSCOPE_API_KEY", "QWEN_TTS_TOKEN"), "Bearer token; defaults to DASHSCOPE_API_KEY or QWEN_TTS_TOKEN")
		model        = flag.String("model", envOrDefault("QWEN_REALTIME_TTS_MODEL", defaultModel), "Qwen realtime TTS model")
		text         = flag.String("text", defaultText, "text to synthesize")
		appendText   = flag.String("append-text", defaultAppend, "second text segment appended to the same realtime session; empty disables the second append")
		voice        = flag.String("voice", defaultVoice, "voice name")
		language     = flag.String("language", defaultLanguage, "language code: zh/en/de/it/pt/es/ja/ko/fr/ru/auto")
		instructions = flag.String("instructions", "", "optional voice guidance/instructions")
		outPath      = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout      = flag.Duration("timeout", 60*time.Second, "session timeout")
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
		endpoint:     *endpoint,
		token:        *token,
		model:        *model,
		text:         *text,
		appendText:   *appendText,
		voice:        *voice,
		language:     *language,
		instructions: *instructions,
		outPath:      *outPath,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint     string
	token        string
	model        string
	text         string
	appendText   string
	voice        string
	language     string
	instructions string
	outPath      string
}

func run(ctx context.Context, cfg config) error {
	qwenProvider, err := qwenrealtime.NewProvider(qwenrealtime.Config{
		Name:            "qwen_realtime",
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

	service := tts.NewService("local-qwen-realtime-test", registry)

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

	sessionID := fmt.Sprintf("local_qwen_realtime_%d", time.Now().UnixNano())
	startedAt := time.Now()

	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID:    sessionID,
		Provider:     "qwen_realtime",
		Voice:        cfg.voice,
		Language:     cfg.language,
		GuidanceText: cfg.instructions,
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
		Language:     cfg.language,
		GuidanceText: cfg.instructions,
		IsLast:       true,
	}); err != nil {
		return err
	}
	if cfg.appendText != "" {
		if err := session.AppendText(ctx, &tts.SegmentRequest{
			SegmentID:    "seg_002",
			Text:         cfg.appendText,
			Voice:        cfg.voice,
			Language:     cfg.language,
			GuidanceText: cfg.instructions,
			IsLast:       true,
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
