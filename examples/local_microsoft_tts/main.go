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
	"github.com/caitunai/tts/internal/provider/microsofttts"
	"github.com/caitunai/tts/internal/tts"
)

const (
	defaultText     = "你好，今天天气怎么样呢？"
	defaultVoice    = "zh-CN-XiaoxiaoNeural"
	defaultLanguage = "zh-CN"
	defaultOutput   = "local_microsoft_tts.ogg"
)

func main() {
	var (
		endpoint = flag.String("endpoint", envOrDefault("MICROSOFT_TTS_ENDPOINT", ""), "Microsoft TTS endpoint, e.g. https://eastus.tts.speech.microsoft.com/cognitiveservices/v1")
		key      = flag.String("key", firstEnv("MICROSOFT_TTS_KEY", "AZURE_SPEECH_KEY"), "subscription key; defaults to MICROSOFT_TTS_KEY or AZURE_SPEECH_KEY")
		text     = flag.String("text", defaultText, "text to synthesize")
		voice    = flag.String("voice", defaultVoice, "voice name")
		language = flag.String("language", defaultLanguage, "language code, e.g. zh-CN/en-US")
		outPath  = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout  = flag.Duration("timeout", 60*time.Second, "request timeout")
	)
	flag.Parse()

	if *endpoint == "" {
		log.Fatal("missing endpoint: pass -endpoint or set MICROSOFT_TTS_ENDPOINT")
	}
	if *key == "" {
		log.Fatal("missing subscription key: pass -key or set MICROSOFT_TTS_KEY")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint: *endpoint,
		key:      *key,
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
	key      string
	text     string
	voice    string
	language string
	outPath  string
}

func run(ctx context.Context, cfg config) error {
	microsoftProvider, err := microsofttts.NewProvider(microsofttts.Config{
		Name:            "microsoft",
		Endpoint:        cfg.endpoint,
		SubscriptionKey: cfg.key,
		DefaultVoice:    cfg.voice,
		DefaultLanguage: cfg.language,
	})
	if err != nil {
		return err
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(microsoftProvider); err != nil {
		return err
	}

	service := tts.NewService("local-microsoft-test", registry)

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

	requestID := fmt.Sprintf("local_microsoft_%d", time.Now().UnixNano())
	startedAt := time.Now()

	events, err := service.SynthesizeOnce(ctx, &tts.SynthesizeRequest{
		RequestID: requestID,
		Provider:  "microsoft",
		Text:      cfg.text,
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

	fmt.Printf("request_id=%s endpoint=%s voice=%s language=%s sample_rate=%d\n", requestID, cfg.endpoint, cfg.voice, cfg.language, audio.OpusSampleRate)

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
