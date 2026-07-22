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
	"github.com/caitunai/tts/providers/qwenaudio"
)

const (
	defaultModel    = "qwen-audio-3.0-tts-flash"
	defaultVoice    = "longanlingxi"
	defaultText     = "你好，这是 Qwen Audio 三点零的实时语音合成测试。"
	defaultLanguage = "zh"
	defaultOutput   = "local_qwen_audio_tts.ogg"
)

func main() {
	var (
		endpoint       = flag.String("endpoint", envOrDefault("QWEN_AUDIO_TTS_ENDPOINT", ""), "Qwen Audio websocket endpoint, for example wss://{workspace}.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference")
		apiKey         = flag.String("key", firstEnv("DASHSCOPE_API_KEY", "QWEN_AUDIO_TTS_API_KEY"), "API key; defaults to DASHSCOPE_API_KEY or QWEN_AUDIO_TTS_API_KEY")
		model          = flag.String("model", envOrDefault("QWEN_AUDIO_TTS_MODEL", defaultModel), "Qwen Audio TTS model")
		voice          = flag.String("voice", envOrDefault("QWEN_AUDIO_TTS_VOICE", defaultVoice), "Qwen Audio voice")
		language       = flag.String("language", envOrDefault("QWEN_AUDIO_TTS_LANGUAGE", defaultLanguage), "language hint")
		instruction    = flag.String("instruction", envOrDefault("QWEN_AUDIO_TTS_INSTRUCTION", ""), "optional synthesis instruction / guidance text")
		text           = flag.String("text", defaultText, "text to synthesize")
		appendText     = flag.String("append-text", "", "second text segment appended to the same websocket session; empty disables the second append")
		outPath        = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout        = flag.Duration("timeout", 60*time.Second, "session timeout")
		bitRate        = flag.Int("bit-rate", 32, "opus bit_rate in kbps")
		volume         = flag.Int("volume", 50, "volume from 0 to 100")
		rate           = flag.Float64("rate", 1.0, "speech rate from 0.5 to 2.0")
		pitch          = flag.Float64("pitch", 1.0, "pitch from 0.5 to 2.0")
		dataInspection = flag.String("data-inspection", "enable", "X-DashScope-DataInspection header value")
		finishDelay    = flag.Duration("finish-delay", -1, "delay before sending finish-task after the last text segment; -1 uses provider default, 0 sends immediately")
	)
	flag.Parse()

	if *endpoint == "" {
		log.Fatal("missing endpoint: pass -endpoint or set QWEN_AUDIO_TTS_ENDPOINT")
	}
	if *apiKey == "" {
		log.Fatal("missing api key: pass -key or set DASHSCOPE_API_KEY")
	}
	if *voice == "" {
		log.Fatal("missing voice: pass -voice or set QWEN_AUDIO_TTS_VOICE")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:       *endpoint,
		apiKey:         *apiKey,
		model:          *model,
		voice:          *voice,
		language:       *language,
		instruction:    *instruction,
		text:           *text,
		appendText:     *appendText,
		outPath:        *outPath,
		bitRate:        *bitRate,
		volume:         *volume,
		rate:           *rate,
		pitch:          *pitch,
		dataInspection: *dataInspection,
		finishDelay:    *finishDelay,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint       string
	apiKey         string
	model          string
	voice          string
	language       string
	instruction    string
	text           string
	appendText     string
	outPath        string
	bitRate        int
	volume         int
	rate           float64
	pitch          float64
	dataInspection string
	finishDelay    time.Duration
}

func run(ctx context.Context, cfg config) error {
	qwenProvider, err := qwenaudio.NewProvider(qwenaudio.Config{
		Name:                qwenaudio.ProviderName,
		Endpoint:            cfg.endpoint,
		APIKey:              cfg.apiKey,
		Model:               cfg.model,
		DefaultVoice:        cfg.voice,
		DefaultLanguage:     cfg.language,
		DefaultInstructions: cfg.instruction,
		BitRate:             cfg.bitRate,
		Volume:              cfg.volume,
		Rate:                cfg.rate,
		Pitch:               cfg.pitch,
		DataInspection:      cfg.dataInspection,
		FinishDelay:         cfg.finishDelay,
	})
	if err != nil {
		return err
	}

	registry := provider.NewRegistry()
	if err := registry.Register(qwenProvider); err != nil {
		return err
	}
	service := tts.NewService("local-qwen-audio-test", registry)

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

	sessionID := fmt.Sprintf("local_qwen_audio_%d", time.Now().UnixNano())
	startedAt := time.Now()
	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID:    sessionID,
		Provider:     qwenaudio.ProviderName,
		Voice:        cfg.voice,
		Language:     cfg.language,
		GuidanceText: cfg.instruction,
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
	if value := os.Getenv(name); value != "" {
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
