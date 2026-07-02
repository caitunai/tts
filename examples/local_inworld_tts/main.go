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
	"github.com/caitunai/tts/providers/inworld"
)

const (
	defaultEndpoint = "wss://api.inworld.ai/tts/v1/voice:streamBidirectional"
	defaultModel    = "inworld-tts-2"
	defaultText     = "Hello, how is the weather today?"
	defaultAppend   = ""
	defaultLanguage = "en-US"
	defaultOutput   = "local_inworld_tts.ogg"
)

func main() {
	var (
		endpoint               = flag.String("endpoint", envOrDefault("INWORLD_TTS_ENDPOINT", defaultEndpoint), "Inworld AI bidirectional TTS websocket endpoint")
		apiKey                 = flag.String("key", firstEnv("INWORLD_API_KEY", "INWORLD_TTS_API_KEY"), "API key; defaults to INWORLD_API_KEY or INWORLD_TTS_API_KEY")
		authorization          = flag.String("authorization", envOrDefault("INWORLD_TTS_AUTHORIZATION", ""), "full authorization query value, for example Basic <token>; overrides -key")
		model                  = flag.String("model", envOrDefault("INWORLD_TTS_MODEL", defaultModel), "Inworld model id")
		voice                  = flag.String("voice", envOrDefault("INWORLD_TTS_VOICE", ""), "voice id")
		language               = flag.String("language", envOrDefault("INWORLD_TTS_LANGUAGE", defaultLanguage), "language code carried in create context")
		contextID              = flag.String("context-id", envOrDefault("INWORLD_TTS_CONTEXT_ID", ""), "optional explicit Inworld context id")
		text                   = flag.String("text", defaultText, "text to synthesize")
		appendText             = flag.String("append-text", defaultAppend, "second text segment appended to the same realtime session; empty disables the second append")
		outPath                = flag.String("out", defaultOutput, "Ogg Opus output path; empty disables file output")
		timeout                = flag.Duration("timeout", 60*time.Second, "session timeout")
		autoMode               = flag.Bool("auto-mode", true, "set Inworld create.autoMode")
		bufferCharThreshold    = flag.Int("buffer-char-threshold", 0, "optional Inworld bufferCharThreshold; 0 omits it")
		maxBufferDelayMS       = flag.Int("max-buffer-delay-ms", 0, "optional Inworld maxBufferDelayMs; 0 omits it")
		deliveryMode           = flag.String("delivery-mode", envOrDefault("INWORLD_TTS_DELIVERY_MODE", ""), "optional Inworld deliveryMode")
		textNormalization      = flag.String("text-normalization", envOrDefault("INWORLD_TTS_TEXT_NORMALIZATION", ""), "optional Inworld applyTextNormalization")
		timestampType          = flag.String("timestamp-type", envOrDefault("INWORLD_TTS_TIMESTAMP_TYPE", ""), "optional Inworld timestampType")
		timestampTransportMode = flag.String("timestamp-transport", envOrDefault("INWORLD_TTS_TIMESTAMP_TRANSPORT", ""), "optional Inworld timestampTransportStrategy")
	)
	flag.Parse()

	if *authorization == "" && *apiKey == "" {
		log.Fatal("missing credentials: pass -key, -authorization, or set INWORLD_API_KEY")
	}
	if *voice == "" {
		log.Fatal("missing voice: pass -voice or set INWORLD_TTS_VOICE")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, config{
		endpoint:                   *endpoint,
		apiKey:                     *apiKey,
		authorization:              *authorization,
		model:                      *model,
		voice:                      *voice,
		language:                   *language,
		contextID:                  *contextID,
		text:                       *text,
		appendText:                 *appendText,
		outPath:                    *outPath,
		autoMode:                   *autoMode,
		bufferCharThreshold:        *bufferCharThreshold,
		maxBufferDelayMS:           *maxBufferDelayMS,
		deliveryMode:               *deliveryMode,
		applyTextNormalization:     *textNormalization,
		timestampType:              *timestampType,
		timestampTransportStrategy: *timestampTransportMode,
	}); err != nil {
		log.Fatal(err)
	}
}

type config struct {
	endpoint      string
	apiKey        string
	authorization string

	model     string
	voice     string
	language  string
	contextID string

	text       string
	appendText string
	outPath    string

	autoMode                   bool
	bufferCharThreshold        int
	maxBufferDelayMS           int
	deliveryMode               string
	applyTextNormalization     string
	timestampType              string
	timestampTransportStrategy string
}

func run(ctx context.Context, cfg config) error {
	inworldProvider, err := inworld.NewProvider(inworld.Config{
		Name:                       inworld.ProviderName,
		Endpoint:                   cfg.endpoint,
		APIKey:                     cfg.apiKey,
		Authorization:              cfg.authorization,
		Model:                      cfg.model,
		DefaultVoice:               cfg.voice,
		DefaultLanguage:            cfg.language,
		ContextID:                  cfg.contextID,
		AutoMode:                   cfg.autoMode,
		BufferCharThreshold:        cfg.bufferCharThreshold,
		MaxBufferDelayMS:           cfg.maxBufferDelayMS,
		DeliveryMode:               cfg.deliveryMode,
		ApplyTextNormalization:     cfg.applyTextNormalization,
		TimestampType:              cfg.timestampType,
		TimestampTransportStrategy: cfg.timestampTransportStrategy,
	})
	if err != nil {
		return err
	}

	registry := provider.NewRegistry()
	if err := registry.Register(inworldProvider); err != nil {
		return err
	}

	service := tts.NewService("local-inworld-test", registry)

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

	sessionID := fmt.Sprintf("local_inworld_%d", time.Now().UnixNano())
	startedAt := time.Now()

	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID: sessionID,
		Provider:  inworld.ProviderName,
		Voice:     cfg.voice,
		Language:  cfg.language,
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

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
