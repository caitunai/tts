# TTS Platform

TTS Platform 是一个 Go 语言的多 Provider TTS 接入层。它把不同 TTS 服务的 HTTP、SSE、WebSocket、PCM、MP3、Ogg Opus 等差异收敛到统一的 Service / Session / Event API，应用层只负责选择和注册需要的 Provider。

核心设计边界：

- Provider 路由由应用层负责，平台层不做自动路由。
- Provider 实现保留在 `internal` 中，应用层通过 public facade package 使用。
- 应用层消费统一事件流：`segment_start`、`audio_frame`、`segment_end`、`session_end`、`error`。
- Opus 音频在平台中按 48 kHz 处理。
- PCM 输出默认面向 16 kHz / mono / 20 ms frame。

## Installation

在其他 Go module 中使用：

```sh
go get github.com/caitunai/tts
```

如果本地开发还没有发布版本，可以在你的应用 module 中临时使用 `replace`：

```go
replace github.com/caitunai/tts => /path/to/tts
```

## Public Packages

应用层应只 import public package：

```text
github.com/caitunai/tts
github.com/caitunai/tts/audio
github.com/caitunai/tts/provider
github.com/caitunai/tts/providers/vllm
github.com/caitunai/tts/providers/qwenhttp
github.com/caitunai/tts/providers/qwenrealtime
github.com/caitunai/tts/providers/deepgram
github.com/caitunai/tts/providers/microsoft
github.com/caitunai/tts/providers/minimax
github.com/caitunai/tts/providers/elevenlabs
github.com/caitunai/tts/providers/doubao
github.com/caitunai/tts/providers/inworld
```

不要在外部应用中 import `github.com/caitunai/tts/internal/...`，Go 会阻止其他 module 访问 `internal` 包。

## Quick Start

下面示例展示如何注册 ElevenLabs Provider，打开一个实时 TTS Session，并消费音频事件。

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	tts "github.com/caitunai/tts"
	"github.com/caitunai/tts/audio"
	"github.com/caitunai/tts/provider"
	"github.com/caitunai/tts/providers/elevenlabs"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	registry := provider.NewRegistry()

	elevenProvider, err := elevenlabs.NewProvider(elevenlabs.Config{
		Name:         elevenlabs.ProviderName,
		Endpoint:     "wss://api.elevenlabs.io/v1/text-to-speech/:voice_id/stream-input",
		APIKey:       os.Getenv("ELEVENLABS_API_KEY"),
		Model:        "eleven_flash_v2_5",
		DefaultVoice: "your-voice-id",
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := registry.Register(elevenProvider); err != nil {
		log.Fatal(err)
	}

	service := tts.NewService("my-app-tts", registry)

	session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
		SessionID: "session-001",
		Provider:  elevenlabs.ProviderName,
		Voice:     "your-voice-id",
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
		log.Fatal(err)
	}
	defer session.Close()

	events := session.Events()

	if err := session.AppendText(ctx, &tts.SegmentRequest{
		SegmentID: "seg-001",
		Text:      "Hello, this is the first sentence.",
		Voice:     "your-voice-id",
	}); err != nil {
		log.Fatal(err)
	}
	if err := session.AppendText(ctx, &tts.SegmentRequest{
		SegmentID: "seg-002",
		Text:      "This is the second sentence.",
		Voice:     "your-voice-id",
		IsLast:    true,
	}); err != nil {
		log.Fatal(err)
	}
	if err := session.Finish(ctx); err != nil {
		log.Fatal(err)
	}

	for event := range events {
		switch event.Type {
		case tts.EventSegmentStart:
			fmt.Println("segment start:", event.SegmentID)
		case tts.EventAudioFrame:
			fmt.Printf("audio segment=%s seq=%d bytes=%d codec=%s sample_rate=%d\n",
				event.SegmentID,
				event.Audio.Seq,
				len(event.Audio.Data),
				event.Audio.Codec,
				event.Audio.SampleRate,
			)
		case tts.EventSegmentEnd:
			fmt.Println("segment end:", event.SegmentID)
		case tts.EventSessionEnd:
			fmt.Println("session end")
			return
		case tts.EventError:
			log.Fatal(event.Error)
		}
	}
}
```

## Provider Registration

Provider 的 `Name` 很重要。应用层 `OpenSessionRequest.Provider` 或 `SynthesizeRequest.Provider` 必须和注册时的 Provider name 一致。

每个 public Provider package 都提供了 `ProviderName` 常量。默认情况下推荐直接使用这个常量，避免不同应用方手写字符串不一致。如果你确实需要一个自定义名称，也可以覆盖 `Config.Name`，但请求里的 `Provider` 必须使用同一个名称。

```go
registry := provider.NewRegistry()

qwen, err := qwenhttp.NewProvider(qwenhttp.Config{
	Name:         qwenhttp.ProviderName,
	Endpoint:     "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
	Token:        os.Getenv("DASHSCOPE_API_KEY"),
	Model:        "qwen-tts",
	DefaultVoice: "Cherry",
})
if err != nil {
	return err
}
if err := registry.Register(qwen); err != nil {
	return err
}

service := tts.NewService("app", registry)
```

使用时：

```go
session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
	Provider: qwenhttp.ProviderName,
	Voice:    "Cherry",
	Language: "zh",
	Output:   audio.DefaultOutputConfig(),
})
```

## Output Modes

不同 Provider 的上游格式不同，平台会尽量统一为应用层要求的输出。

`OpenSessionRequest.Output` 和 `SynthesizeRequest.Output` 可以省略。省略时，Service 会根据 Provider 的 `Capabilities` 自动推导默认输出：

- Ogg Opus Provider 默认输出 raw Opus packet，采样率 48 kHz。
- PCM Provider 默认输出 16 kHz / mono / 20 ms PCM frame。
- Minimax 这类上游返回 MP3、平台负责解码的 Provider 默认输出 PCM frame。

应用层只有在需要明确指定输出编码、采样率或 frame 参数时才需要传 `Output`。

常见输出配置：

```go
// 16 kHz / mono / 20 ms PCM frame
pcmOutput := audio.OutputConfig{
	PreferCodec:         audio.CodecPCM,
	SampleRate:          audio.DefaultSampleRate,
	Channels:            audio.DefaultChannels,
	FrameMS:             audio.DefaultFrameMS,
	PCMFormat:           audio.PCMFormatS16LE,
	AllowPCMFrameOutput: true,
}

// Raw Opus packet, 48 kHz
opusOutput := audio.OutputConfig{
	PreferCodec:        audio.CodecOpus,
	SampleRate:         audio.OpusSampleRate,
	Channels:           audio.DefaultChannels,
	FrameMS:            audio.DefaultFrameMS,
	AllowOggOpusDemux:  true,
	AllowRawOpusOutput: true,
}
```

如果应用层需要把 raw Opus packet 保存成可播放文件，可以使用 `audio.OggOpusMuxer`：

```go
out, err := os.Create("tts.ogg")
if err != nil {
	return err
}
defer out.Close()

muxer := audio.NewOggOpusMuxer()
for event := range session.Events() {
	if event.Type == tts.EventAudioFrame && event.Audio.Codec == audio.CodecOpus {
		if err := muxer.WritePacket(out, event.Audio.Data); err != nil {
			return err
		}
	}
	if event.Type == tts.EventSessionEnd {
		return muxer.Finish(out)
	}
}
```

## Supported Providers

| Package | Transport | Upstream audio | Typical platform output |
| --- | --- | --- | --- |
| `providers/vllm` | HTTP chunked | 24 kHz PCM | 16 kHz mono PCM frames |
| `providers/qwenhttp` | HTTP SSE | base64 PCM | 16 kHz mono PCM frames |
| `providers/qwenrealtime` | WebSocket | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/deepgram` | HTTP | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/microsoft` | HTTP | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/minimax` | WebSocket | MP3 | 16 kHz mono PCM frames |
| `providers/elevenlabs` | WebSocket | base64 Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/doubao` | WebSocket | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/inworld` | WebSocket | base64 Ogg-wrapped Opus | raw Opus packets, 48 kHz |

## Provider Config Examples

### vLLM-compatible HTTP TTS

```go
provider, err := vllm.NewProvider(vllm.Config{
	Name:            vllm.ProviderName,
	Endpoint:        "http://127.0.0.1:9012/v1/audio/speech",
	Token:           os.Getenv("TTS_HTTP_TOKEN"),
	DefaultVoice:    "serena",
	DefaultLanguage: "Chinese",
})
```

### Qwen HTTP SSE TTS

```go
provider, err := qwenhttp.NewProvider(qwenhttp.Config{
	Name:         qwenhttp.ProviderName,
	Endpoint:     "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation",
	Token:        os.Getenv("DASHSCOPE_API_KEY"),
	Model:        "qwen-tts",
	DefaultVoice: "Cherry",
})
```

### Qwen Realtime WebSocket TTS

```go
provider, err := qwenrealtime.NewProvider(qwenrealtime.Config{
	Name:         qwenrealtime.ProviderName,
	Endpoint:     "wss://dashscope.aliyuncs.com/api-ws/v1/realtime",
	Token:        os.Getenv("DASHSCOPE_API_KEY"),
	Model:        "qwen3-tts-instruct-flash-realtime",
	DefaultVoice: "Cherry",
})
```

### Microsoft Azure Speech TTS

```go
provider, err := microsoft.NewProvider(microsoft.Config{
	Name:            microsoft.ProviderName,
	Endpoint:        "https://eastus.tts.speech.microsoft.com/cognitiveservices/v1",
	SubscriptionKey: os.Getenv("MICROSOFT_TTS_KEY"),
	DefaultVoice:    "zh-CN-XiaoxiaoNeural",
	DefaultLanguage: "zh-CN",
})
```

### Deepgram HTTP TTS

```go
provider, err := deepgram.NewProvider(deepgram.Config{
	Name:   deepgram.ProviderName,
	APIKey: os.Getenv("DEEPGRAM_API_KEY"),
	Model:  "aura-asteria-en",
})
```

The Deepgram provider requests `encoding=opus` and `container=ogg`. Deepgram
does not accept `sample_rate` when `encoding=opus`; the platform still treats
the returned Opus audio as 48 kHz. Request `Voice` can be used to override the
Deepgram `model` query parameter for a single synthesis call.

### Minimax Realtime TTS

```go
provider, err := minimax.NewProvider(minimax.Config{
	Name:            minimax.ProviderName,
	Endpoint:        os.Getenv("MINIMAX_TTS_ENDPOINT"),
	Token:           os.Getenv("MINIMAX_TTS_TOKEN"),
	Model:           "speech-2.8-turbo",
	DefaultVoice:    "male-qn-qingse",
	DefaultLanguage: "zh",
})
```

### ElevenLabs Realtime TTS

```go
provider, err := elevenlabs.NewProvider(elevenlabs.Config{
	Name:         elevenlabs.ProviderName,
	Endpoint:     "wss://api.elevenlabs.io/v1/text-to-speech/:voice_id/stream-input",
	APIKey:       os.Getenv("ELEVENLABS_API_KEY"),
	Model:        "eleven_flash_v2_5",
	DefaultVoice: "your-voice-id",
})
```

### Doubao Bidirectional WebSocket TTS

```go
provider, err := doubao.NewProvider(doubao.Config{
	Name:            doubao.ProviderName,
	APIKey:          os.Getenv("DOUBAO_TTS_API_KEY"),
	ResourceID:      "seed-tts-2.0",
	DefaultVoice:    os.Getenv("DOUBAO_TTS_VOICE"),
	DefaultLanguage: "zh",
})
```

### Inworld AI Bidirectional WebSocket TTS

```go
provider, err := inworld.NewProvider(inworld.Config{
	Name:            inworld.ProviderName,
	APIKey:          os.Getenv("INWORLD_API_KEY"),
	Model:           "inworld-tts-2",
	DefaultVoice:    "Dennis",
	DefaultLanguage: "en-US",
	AutoMode:        true,
})
```

The Inworld provider requests `OGG_OPUS` at 48 kHz. The API key is sent as the
websocket query parameter `authorization=Basic ...`, matching the Inworld TTS
WebSocket documentation.

## Guidance Text

部分 Provider 支持合成引导词。可以在 session 级别或 segment 级别传入：

```go
session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
	Provider:     doubao.ProviderName,
	Voice:        "your-speaker-id",
	Language:     "zh",
	GuidanceText: "温暖、自然、语速稍慢",
	Output:       opusOutput,
})
```

```go
err := session.AppendText(ctx, &tts.SegmentRequest{
	SegmentID:    "seg-001",
	Text:         "你好，欢迎使用语音合成服务。",
	GuidanceText: "更亲切一点",
	IsLast:       true,
})
```

如果 Provider 不支持 guidance text，Service 会返回 `ErrUnsupportedFeature`。

## Reference Audio

平台核心类型已经包含 `ReferenceAudio`，用于支持“参考音频克隆/相似音色”能力：

```go
req := &tts.SegmentRequest{
	SegmentID: "seg-001",
	Text:      "请根据参考音频合成这句话。",
	ReferenceAudio: &tts.ReferenceAudio{
		Codec:      audio.CodecWAV,
		Container:  audio.ContainerWAV,
		SampleRate: 16000,
		Channels:   1,
		Data:       wavBytes,
		Text:       "参考音频对应文本",
	},
}
```

当前只有声明支持 `SupportsReferenceAudio` 的 Provider 才能接收此字段。

## Error Handling

同步调用错误和事件流错误都会使用统一的 `tts.Error`：

```go
if err != nil {
	if ttsErr, ok := err.(*tts.Error); ok {
		fmt.Println(ttsErr.Code, ttsErr.Provider, ttsErr.Message)
	}
}
```

事件流中：

```go
if event.Type == tts.EventError {
	log.Println(event.Error.Code, event.Error.Message)
}
```

常见错误码包括：

- `ErrUnsupportedProvider`
- `ErrUnsupportedFeature`
- `ErrProviderUnavailable`
- `ErrSessionClosed`
- `ErrSegmentFailed`
- `ErrAudioDecodeFailed`
- `ErrAudioNormalizeFailed`

## Real Provider Test Programs

真实服务测试程序在 [examples](./examples) 目录中，详细命令见 [examples/README.md](./examples/README.md)。

```sh
go run ./examples/local_qwen_tts
go run ./examples/local_qwen_realtime_tts
go run ./examples/local_deepgram_tts
go run ./examples/local_minimax_tts
go run ./examples/local_microsoft_tts
go run ./examples/local_elevenlabs_tts
go run ./examples/local_doubao_tts
go run ./examples/local_inworld_tts
```

## Development Checks

常用检查命令：

```sh
go test ./...
go test -run '^$' ./...
```

当前仓库里 Provider 真实调用程序依赖外部服务和环境变量，普通单元测试不会访问真实 TTS 服务。
