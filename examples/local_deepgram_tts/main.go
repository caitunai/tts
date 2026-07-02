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
	"github.com/caitunai/tts/providers/deepgram"
)

const (
	defaultEndpoint = "https://api.deepgram.com/v1/speak"
	defaultModel    = "aura-asteria-en"
	defaultText     = "Hello, welcome to Deepgram text to speech."
	defaultOutput   = "local_deepgram_tts.ogg"
)

func main() {
	var (
		endpoint      = flag.String("endpoint", envOrDefault("DEEPGRAM_TTS_ENDPOINT", defaultEndpoint), "Deepgram TTS HTTP endpoint")
		apiKey        = flag.String("key", firstEnv("DEEPGRAM_API_KEY", "DEEPGRAM_TTS_API_KEY"), "API key; defaults to DEEPGRAM_API_KEY or DEEPGRAM_TTS_API_KEY")
		authorization = flag.String("authorization", envOrDefault("DEEPGRAM_TTS_AUTHORIZATION", ""), "full Authorization header, for example Token <key>; overrides -key")
		model         = flag.String("model", envOrDefault("DEEPGRAM_TTS_MODEL", defaultModel), "Deepgram model id")
		text          = flag.String("text", defaultText, "text to synthesize")
		outPath       = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout       = flag.Duration("timeout", 60*time.Second, "request timeout")
		speed         = flag.Float64("speed", 0, "optional Deepgram speaking speed; 0 omits the parameter")
		tag           = flag.String("tag", envOrDefault("DEEPGRAM_TTS_TAG", ""), "optional Deepgram usage tag")
		mipOptOut     = flag.Bool("mip-opt-out", false, "set Deepgram mip_opt_out=true")
	)
	flag.Parse()

	if *authorization == "" && *apiKey == "" {
		log.Fatal("missing api key: pass -key, -authorization, or set DEEPGRAM_API_KEY")
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
		text:          *text,
		outPath:       *outPath,
		speed:         *speed,
		tag:           *tag,
		mipOptOut:     *mipOptOut,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint      string
	apiKey        string
	authorization string
	model         string
	text          string
	outPath       string
	speed         float64
	tag           string
	mipOptOut     bool
}

func run(ctx context.Context, cfg config) error {
	deepgramProvider, err := deepgram.NewProvider(deepgram.Config{
		Name:          deepgram.ProviderName,
		Endpoint:      cfg.endpoint,
		APIKey:        cfg.apiKey,
		Authorization: cfg.authorization,
		Model:         cfg.model,
		Speed:         cfg.speed,
		Tag:           cfg.tag,
		MIPOptOut:     cfg.mipOptOut,
	})
	if err != nil {
		return err
	}

	registry := provider.NewRegistry()
	if err := registry.Register(deepgramProvider); err != nil {
		return err
	}

	service := tts.NewService("local-deepgram-test", registry)

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

	requestID := fmt.Sprintf("local_deepgram_%d", time.Now().UnixNano())
	startedAt := time.Now()

	events, err := service.SynthesizeOnce(ctx, &tts.SynthesizeRequest{
		RequestID: requestID,
		Provider:  deepgram.ProviderName,
		Text:      cfg.text,
		Voice:     cfg.model,
	})
	if err != nil {
		return err
	}

	fmt.Printf("request_id=%s endpoint=%s model=%s sample_rate=%d\n", requestID, cfg.endpoint, cfg.model, audio.OpusSampleRate)

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
