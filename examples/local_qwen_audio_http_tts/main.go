package main

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	tts "github.com/caitunai/tts"
	"github.com/caitunai/tts/audio"
	"github.com/caitunai/tts/provider"
	"github.com/caitunai/tts/providers/qwenaudiohttp"
)

func main() {
	cfg := qwenaudiohttp.Config{}
	flag.StringVar(&cfg.Endpoint, "endpoint", os.Getenv("QWEN_AUDIO_HTTP_TTS_ENDPOINT"), "HTTP SpeechSynthesizer endpoint including workspace and region")
	flag.StringVar(&cfg.APIKey, "key", cmp.Or(os.Getenv("DASHSCOPE_API_KEY"), os.Getenv("QWEN_AUDIO_TTS_API_KEY")), "API key (prefer environment variables)")
	flag.StringVar(&cfg.Model, "model", cmp.Or(os.Getenv("QWEN_AUDIO_HTTP_TTS_MODEL"), qwenaudiohttp.ModelQwenAudio31TTSFlash), "Qwen Audio model")
	flag.StringVar(&cfg.DefaultVoice, "voice", cmp.Or(os.Getenv("QWEN_AUDIO_HTTP_TTS_VOICE"), "longanhuan_v3.1"), "voice matching the model version")
	flag.StringVar(&cfg.DefaultLanguage, "language", "zh", "optional language hint")
	flag.StringVar(&cfg.DefaultInstructions, "instruction", "", "optional synthesis instruction")
	flag.IntVar(&cfg.BitRate, "bit-rate", 32, "Opus bit rate in kbps")
	flag.BoolVar(&cfg.NonStreaming, "non-streaming", false, "use JSON synthesis and download the returned audio URL instead of SSE")
	text := flag.String("text", "你好，这是千问语音合成 HTTP 协议测试。", "text, SSML, or LaTeX to synthesize")
	out := flag.String("out", "local_qwen_audio_http_tts.ogg", "Ogg Opus output path; empty disables file output")
	timeout := flag.Duration("timeout", 60*time.Second, "total timeout, including audio download")
	rate := flag.Float64("rate", 1, "speech rate, 0.5-2.0")
	pitch := flag.Float64("pitch", 1, "pitch, 0.5-2.0")
	volume := flag.Int("volume", 50, "volume, 0-100")
	ssml := flag.Bool("ssml", false, "enable SSML input")
	wordTimestamps := flag.Bool("word-timestamps", false, "request word timestamps (SSE only)")
	seed := flag.Int("seed", -1, "optional seed, 0-65535; -1 omits it")
	flag.Parse()
	if cfg.APIKey == "" {
		log.Fatal("set DASHSCOPE_API_KEY or QWEN_AUDIO_TTS_API_KEY")
	}
	if cfg.Endpoint == "" {
		log.Fatal("set QWEN_AUDIO_HTTP_TTS_ENDPOINT or pass -endpoint")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	options := map[string]any{"rate": *rate, "pitch": *pitch, "volume": *volume,
		"enable_ssml": *ssml, "word_timestamp_enabled": *wordTimestamps}
	if *seed >= 0 {
		options["seed"] = *seed
	}
	if err := run(ctx, cfg, *text, *out, options); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, cfg qwenaudiohttp.Config, text, outPath string, options map[string]any) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, err := qwenaudiohttp.NewProvider(cfg)
	if err != nil {
		return err
	}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		return err
	}
	var out *os.File
	if outPath != "" {
		out, err = os.Create(outPath)
		if err != nil {
			return err
		}
		defer out.Close()
	}
	muxer := audio.NewOggOpusMuxer()
	started := time.Now()
	events, err := tts.NewService("local-qwen-audio-http", registry).SynthesizeOnce(ctx, &tts.SynthesizeRequest{
		RequestID: fmt.Sprintf("local_qwen_audio_http_%d", started.UnixNano()), Provider: qwenaudiohttp.ProviderName,
		Text: text, Options: options,
	})
	if err != nil {
		return err
	}
	fmt.Printf("model=%s voice=%s non_streaming=%v sample_rate=48000\n", cfg.Model, cfg.DefaultVoice, cfg.NonStreaming)
	packets, audioBytes, ended := 0, 0, false
	for event := range events {
		switch event.Type {
		case tts.EventSegmentStart:
			fmt.Printf("segment_start segment_id=%s\n", event.SegmentID)
		case tts.EventAudioFrame:
			if event.Audio == nil || event.Audio.Codec != audio.CodecOpus || event.Audio.SampleRate != audio.OpusSampleRate || event.Audio.Container != audio.ContainerRaw {
				return fmt.Errorf("unexpected normalized audio frame: %#v", event.Audio)
			}
			if packets == 0 {
				fmt.Printf("first_audio_latency=%s\n", time.Since(started).Round(time.Millisecond))
			}
			packets++
			audioBytes += len(event.Audio.Data)
			if out != nil {
				if err := muxer.WritePacket(out, event.Audio.Data); err != nil {
					return err
				}
			}
			fmt.Printf("opus_packet segment_id=%s seq=%d global_seq=%d bytes=%d\n", event.SegmentID, event.Audio.Seq, event.Audio.GlobalSeq, len(event.Audio.Data))
		case tts.EventSegmentEnd:
			ended = true
			fmt.Printf("segment_end segment_id=%s\n", event.SegmentID)
			for _, key := range []string{"request_id", "usage", "sentences"} {
				if value := event.Meta[key]; value != nil {
					data, err := json.Marshal(value)
					if err != nil {
						return err
					}
					fmt.Printf("%s=%s\n", key, data)
				}
			}
		case tts.EventError:
			if event.Error != nil {
				return event.Error
			}
			return fmt.Errorf("unknown TTS error")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ended || packets == 0 {
		return fmt.Errorf("synthesis ended without complete audio")
	}
	if out != nil {
		if err := muxer.Finish(out); err != nil {
			return err
		}
		fmt.Printf("wrote_ogg_opus=%s\nplayback: ffplay %s\n", outPath, outPath)
	}
	fmt.Printf("done opus_packets=%d audio_bytes=%d elapsed=%s\n", packets, audioBytes, time.Since(started).Round(time.Millisecond))
	return nil
}
