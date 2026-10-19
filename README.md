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
github.com/caitunai/tts/providers/qwenaudio
github.com/caitunai/tts/providers/qwenaudiohttp
github.com/caitunai/tts/providers/cartesia
github.com/caitunai/tts/providers/deepgram
github.com/caitunai/tts/providers/microsoft
github.com/caitunai/tts/providers/minimax
github.com/caitunai/tts/providers/elevenlabs
github.com/caitunai/tts/providers/doubao
github.com/caitunai/tts/providers/inworld
github.com/caitunai/tts/providers/fishaudio
github.com/caitunai/tts/providers/openai
github.com/caitunai/tts/providers/gemini
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

Provider capabilities 默认不声明有限语种或音色列表，平台层不会根据 `Language`
或 `Voice` 提前拒绝请求。语言和音色兼容性交给具体上游 Provider/API 处理，
避免第三方服务新增语种、音色或工作区自定义音色后被平台静态列表误拦截。
`DefaultVoice` 只作为请求未传 `Voice` 时的缺省值，不代表平台层的允许列表。

`Language` 输入支持常见 ISO-639-1、ISO-639-3、BCP-47、下划线格式和常见英文名称。
Service 会先归一化为平台内部 canonical code，例如 `eng` / `en_US` / `English`
会归一化为 `en` 或 `en-US`，`zho` / `Chinese` 会归一化为 `zh`，`cmn-CN`
会保留为 `cmn-CN`。Provider adapter 在最终请求前再转换为厂商协议需要的格式，例如
Qwen 的 `Chinese` / `English`、Microsoft 的 `en-US` / `zh-CN`。Doubao 不发送
明确语种，而是启用上游自动语种检测。
应用层也可以调用 `tts.NormalizeLanguage` / `tts.PrimaryLanguage` 复用同一套规则。
需要读取 ISO 和 BCP-47 组成部分或比较语种时，可以使用 `tts.ParseLanguage`：

```go
lang := tts.ParseLanguage("eng-Latn-US")
fmt.Println(lang.String())   // en-Latn-US
fmt.Println(lang.ISO6391())  // en
fmt.Println(lang.ISO6393())  // eng
fmt.Println(lang.Script())   // Latn
fmt.Println(lang.Region())   // US

sameLanguage := lang.Matches(
	tts.ParseLanguage("en-GB"),
	tts.LanguageMatchLanguage,
)
```

Provider 的语种转换使用同一个 `Language` 类型和声明式映射器。映射规则可按完整标签、
语种加 script、语种加 region 或仅语种四种粒度匹配。仅语种匹配会自动把 `en`、`eng`
和 `English` 视为同一 ISO 语种，同时忽略 BCP-47 的 script 和 region；`zh`、`cmn`、
`yue` 是不同的 ISO 语种，需要在同一条厂商映射规则中显式组成语种集合。

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

## Timeouts And Reconnection

所有 HTTP 和 WebSocket Provider 的 `Config` 都支持 `Timeouts tts.TimeoutConfig`。
不配置时也有有限超时，不再依赖应用一定传入带 deadline 的 context。
零值使用默认值，负值配置会被拒绝。

| 配置 | 默认值 | 作用 |
| --- | --- | --- |
| `RequestTimeout` | 60 秒 | 每次 HTTP 请求，包含读取响应体；音频 URL 下载独立计时 |
| `ConnectTimeout` | 10 秒 | 默认 HTTP TCP/TLS 连接及每次 WebSocket 握手 |
| `OpenTimeout` | 15 秒 | WebSocket 建连重试与同步协议初始化；默认 HTTP transport 的响应头等待 |
| `ReadTimeout` | 30 秒 | HTTP 响应体 / WebSocket 底层读取无新数据 |
| `WriteTimeout` | 10 秒 | WebSocket 写操作 |
| `SegmentTimeout` | 120 秒 | WebSocket 分段自发送至 `segment_end`，不会被心跳或音频块续期 |
| `FinishTimeout` | 15 秒 | Finish 后所有待合成分段结束，再等待 `session_end` 的时间 |

HTTP 自定义 `resty.Client` 不会被修改，但仍有单次请求 context 超时和响应体读超时。
自定义 transport/dialer 必须自行保证建连操作响应 context；已有的更短 deadline 仍优先。
`OpenTimeout` 的 HTTP 响应头限制只应用于平台新建的默认 transport。
HTTP 不自动重试合成 POST 请求。

```go
p, err := qwenaudio.NewProvider(qwenaudio.Config{
	Endpoint:     os.Getenv("QWEN_AUDIO_TTS_ENDPOINT"),
	APIKey:       os.Getenv("DASHSCOPE_API_KEY"),
	DefaultVoice: "longanhuan_v3.1",
	Model:        qwenaudio.ModelQwenAudio31TTSFlash,
	Timeouts: tts.TimeoutConfig{
		ReadTimeout:    20 * time.Second,
		WriteTimeout:   5 * time.Second,
		SegmentTimeout: 90 * time.Second,
	},
	Reconnect: tts.ReconnectConfig{
		MaxAttempts:    3, // 包含第一次连接；设为 1 禁止重试
		InitialBackoff: 200 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
	},
})
```

WebSocket 默认最多尝试建连 3 次，退避从约 200ms 开始，指数增长并加入抖动，最大
2 秒，整个建连过程受 `OpenTimeout` 限制。仅网络瞬态故障、HTTP 429/5xx 等建连失败
可重试；鉴权失败和普通 4xx 不重试。WebSocket 已连接后，协议初始化或合成失败不会
自动重放 `run-task`、`AppendText`、commit 或 finish 消息。

WebSocket 超时后旧连接不可复用，会返回 `ErrProviderTimeout` 并关闭 Session。
连接级错误不会再推进队列，把下一段文本发到失效连接上。
`Error.Reconnectable` 表示应用可以新建 Session；**不代表可以自动重播当前分段**。
`Error.PartialAudio` 表示该分段已经交付过上游音频块（可能包括容器头），此时
`Retryable=false`，避免应用自动重试造成重复播报。即便尚未收到音频，文本也可能已经
被上游接受，因此平台一律不自动重新合成已发送文本。

应用收到 `EventError` 后可调用下面的函数。取得新 Session 后需要切换到新的
`Events()` channel，只发送后续明确需要合成的新文本；不要自动迁移旧 Session 的队列。
Session 的并发读写和替换应由应用的同一个调度循环协调。

```go
func reopenSession(ctx context.Context, service tts.Service, old tts.Session,
	req tts.OpenSessionRequest, failure *tts.Error) (tts.Session, error) {
	if old != nil {
		_ = old.Close()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if failure == nil {
		return nil, fmt.Errorf("missing session failure")
	}
	if !failure.Reconnectable {
		return nil, failure
	}
	req.SessionID = "" // 新连接、新任务；不重放旧任务
	return service.OpenSession(ctx, &req)
}
```

`OpenSession` 的 context 控制整个 Session 生命周期，返回后不要立即取消；应用退出或
放弃事件流时，应取消 context 或调用 `Close`。`AppendText` 的 context 用于本次提交，
整个合成分段的完成等待由 `SegmentTimeout` 限制。
WebSocket 空闲时也受读超时影响，长时间没有服务端消息会断开；需要跨轮次长时间等待的
应用应按需新建 Session 或调大 `ReadTimeout`，不能在收到超时后继续使用原连接。

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
| `providers/qwenaudio` | WebSocket | Ogg-wrapped Opus binary frames | raw Opus packets, 48 kHz |
| `providers/qwenaudiohttp` | HTTP SSE / JSON + audio URL | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/cartesia` | WebSocket | base64 raw PCM | 16 kHz mono PCM frames |
| `providers/deepgram` | HTTP | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/microsoft` | HTTP | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/minimax` | WebSocket | MP3 | 16 kHz mono PCM frames |
| `providers/elevenlabs` | WebSocket | base64 Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/doubao` | WebSocket | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/inworld` | WebSocket | base64 Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/fishaudio` | WebSocket | MessagePack Ogg Opus chunks | raw Opus packets, 48 kHz |
| `providers/openai` | HTTP | Ogg-wrapped Opus | raw Opus packets, 48 kHz |
| `providers/gemini` | HTTP streaming | 24 kHz raw PCM | 16 kHz mono PCM frames |

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

Qwen HTTP SSE 的音频支持 raw PCM 和带 RIFF/WAVE header 的流式 s16le PCM。
Provider 自动识别并剥离 WAV header，从 header 读取采样率与声道数，再交给平台
输出 PCM frame；header 可以跨多个 SSE 事件分片。
调用 `SynthesizeOnce` 后如果提前停止读取事件，应取消传入的 context。
Qwen Provider 和应用层事件转发会退出阻塞发送并关闭 HTTP 响应体与事件 channel。

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

### Qwen Audio 3.0 / 3.1 Realtime WebSocket TTS

```go
provider, err := qwenaudio.NewProvider(qwenaudio.Config{
	Name:            qwenaudio.ProviderName,
	Endpoint:        os.Getenv("QWEN_AUDIO_TTS_ENDPOINT"),
	APIKey:          os.Getenv("DASHSCOPE_API_KEY"),
	Model:           qwenaudio.ModelQwenAudio31TTSFlash,
	DefaultVoice:    "longanhuan_v3.1",
	DefaultLanguage: "zh",
	BitRate:         32,
})
```

Qwen Audio 3.0 和 3.1 复用同一个 `providers/qwenaudio` Provider，使用
`run-task` / `continue-task` / `finish-task` 的实时
WebSocket 协议。Provider 请求 `format=opus`、`sample_rate=48000`，服务端
`result-generated` 事件后的 binary audio frame 会作为 Ogg-wrapped Opus
交给平台 demux，应用层拿到 raw Opus packets。`OpenSessionRequest.GuidanceText`
会映射到 Qwen Audio 的 `instruction` 参数。

此 Provider 支持在上一段音频尚未结束时发送后续 `AppendText`，`Finish` 也会立即
通知上游刷新缓存文本；不会通过提前发送 `segment_end` 来推进队列。
对外事件仍按 `segment_start → audio_frame → segment_end` 串行输出，最后一段
等待 `task-finished`，确保尾部音频在 `segment_end` 之前交付。一次输入包含多个
句子时，会等待整段文本完成。建议按完整句子追加；上游把跨段文本合并为同一句时，
该句音频归属于最早的未完成段，不会为匹配输入边界强行拆分 Opus packet。
若上游缺少或改写了用于定位边界的 `original_text`，无法可靠确认的段会等到
`task-finished` 后依次结束。

可选模型常量为 `ModelQwenAudio30TTSFlash`、`ModelQwenAudio30TTSPlus` 和
`ModelQwenAudio31TTSFlash`。为兼容已有调用，Provider 未配置 `Model` 时仍默认
使用 3.0 Flash；真实测试例程默认使用 3.1 Flash。切换模型时必须同时使用该模型支持的
音色，例如 3.1 的 `longanhuan_v3.1`，不能直接沿用 3.0 音色。
完整列表见[官方音色文档](https://help.aliyun.com/zh/model-studio/qwen-audio-tts-voice-list)。
`instruction` 支持实时流式合成；文本内的情感与富语言标签按文档仅支持单向流式模式，
不作为本 Provider 的双向 append-text 功能承诺。

### Qwen Audio Non-Realtime HTTP TTS

`providers/qwenaudiohttp` 对接 `SpeechSynthesizer` HTTP 协议，与
`providers/qwenaudio`（WebSocket）和 `providers/qwenhttp`（旧版 Qwen HTTP）相互独立。
支持 Qwen Audio 3.0 Flash/Plus 和 3.1 Flash，默认模型为 3.1 Flash。
当前音频范围为 Opus：固定请求 `format=opus`、`sample_rate=48000`，不请求 MP3/WAV/PCM。

```go
p, err := qwenaudiohttp.NewProvider(qwenaudiohttp.Config{
	Endpoint:     os.Getenv("QWEN_AUDIO_HTTP_TTS_ENDPOINT"),
	APIKey:       os.Getenv("DASHSCOPE_API_KEY"),
	Model:        qwenaudiohttp.ModelQwenAudio31TTSFlash,
	DefaultVoice: "longanhuan_v3.1",
	BitRate:      32,
	NonStreaming: false, // false: SSE; true: JSON response + signed audio URL
})
```

北京 Endpoint 为 `https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/api/v1/services/audio/tts/SpeechSynthesizer`，
新加坡则使用 `ap-southeast-1`。API Key 与业务空间必须属于同一地域，音色须匹配模型版本。
注册此 Provider 后，通过 `Service.SynthesizeOnce` 调用，Provider 名称为
`qwenaudiohttp.ProviderName`。可省略 `Output`，由 Provider 能力推导出 48 kHz Opus 输出。
HTTP 不支持 `OpenSession` / `AppendText`。

`SynthesizeRequest.Language` 会归一化为一个 `input.language_hints`，支持 ISO-639-1、
ISO-639-3 和 BCP-47；不填则不发送提示，让上游自动判断。`GuidanceText` 映射为
`input.instruction`。声音复刻/设计所得的 voice ID 可以直接传入，不设置平台音色白名单；
此接口不直接接收参考音频。

`SynthesizeRequest.Options` 支持以下上游 `input` 参数（不能覆盖 text、voice、format、sample_rate）：

| Key | Type | Notes |
| --- | --- | --- |
| `volume` | integer | 0–100，允许显式静音 |
| `rate`, `pitch` | number | 0.5–2.0 |
| `bit_rate` | integer | 6–510，覆盖配置的码率 |
| `enable_ssml` | boolean | SSML 输入需设为 true；LaTeX 原样传入 |
| `word_timestamp_enabled` | boolean | 仅 SSE 模式可开启 |
| `seed` | integer | 0–65535 |
| `enable_aigc_tag` | boolean | 上游音频水印开关 |
| `aigc_propagator`, `aigc_propagate_id` | string | 水印传播信息 |
| `hot_fix` | object | pronunciation / replace 字符串映射数组 |

SSE 复用 `SSEParser`，按序处理 Base64 音频，不把 `sentence-end` 当作请求结束。
只有处理完 `finish_reason=stop` 中可能携带的最后一块音频后才发送 `segment_end`，
不会再等待 HTTP 连接 EOF。缺少完成标记的断流会返回错误。
非流式模式使用单独、默认无鉴权的 `AudioClient` 下载 URL；自定义该客户端时也不要配置 API Key。
SSE 已收到音频时不会再下载最终 URL，避免重复音频。

`segment_end` 的 `Event.Meta` 保留上游 `request_id`、`usage`、音频 URL/ID/过期时间，
以及 SSE `sentence-end` 收集的 `sentences`（含字级时间戳）。3.0 使用 characters，
3.1 使用 input_tokens / output_tokens / total_tokens。
字级时间戳需模型和音色支持。平台输出 raw Opus 后重新封装的 Ogg 文件不保证保留上游
容器级 AIGC 标识；需要保留原始标识的场景应使用最终 URL 下载的原文件。

真实例程见 `examples/local_qwen_audio_http_tts`，运行命令见 [Examples](examples/README.md#local_qwen_audio_http_tts)。

### Microsoft Azure Speech TTS

```go
provider, err := microsoft.NewProvider(microsoft.Config{
	Name:            microsoft.ProviderName,
	Endpoint:        "https://eastus.tts.speech.microsoft.com/cognitiveservices/v1",
	SubscriptionKey: os.Getenv("MICROSOFT_TTS_KEY"),
	DefaultVoice:    "zh-CN-XiaoxiaoNeural",
})
```

Microsoft SSML 要求根节点包含 `xml:lang`。请求没有指定 `Language` 时，Provider
会优先从标准 voice 名称提取 locale，例如 `zh-CN-XiaoxiaoNeural` 得到 `zh-CN`；
无法提取时再使用 `DefaultLanguage`，最后兜底为 `en-US`。

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

### Cartesia WebSocket TTS

```go
provider, err := cartesia.NewProvider(cartesia.Config{
	Name:            cartesia.ProviderName,
	APIKey:          os.Getenv("CARTESIA_API_KEY"),
	Model:           "sonic-3.5",
	DefaultVoice:    "your-voice-id",
	DefaultLanguage: "en",
})
```

The Cartesia provider requests `container=raw`, `encoding=pcm_s16le`, and emits
16 kHz / mono / 20 ms PCM frames by default.

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

### Fish Audio WebSocket TTS

```go
provider, err := fishaudio.NewProvider(fishaudio.Config{
	Name:            fishaudio.ProviderName,
	APIKey:          os.Getenv("FISHAUDIO_API_KEY"),
	Model:           "s1",
	DefaultVoice:    "your-reference-id",
	DefaultLanguage: "en",
	Latency:         "normal",
})
```

The Fish Audio provider uses MessagePack over WebSocket and requests `format=opus`.
Fish Audio sends Opus audio chunks that form a continuous Ogg Opus stream, and
the platform demuxes them into raw Opus packets at 48 kHz for the application
layer.

### OpenAI Speech API TTS

```go
provider, err := openai.NewProvider(openai.Config{
	Name:         openai.ProviderName,
	APIKey:       os.Getenv("OPENAI_API_KEY"),
	Model:        "gpt-4o-mini-tts",
	DefaultVoice: "coral",
})
```

The OpenAI provider calls `POST /v1/audio/speech` with `response_format=opus`
and `stream_format=audio`. The platform treats the returned Opus audio as
48 kHz Ogg Opus chunks and demuxes them into raw Opus packets for the
application layer. `GuidanceText` is mapped to OpenAI's `instructions` field
for models that support it.

### Gemini TTS

```go
provider, err := gemini.NewProvider(gemini.Config{
	Name:         gemini.ProviderName,
	APIKey:       os.Getenv("GEMINI_API_KEY"),
	Model:        "gemini-3.1-flash-tts-preview",
	DefaultVoice: "Kore",
	// Optional: finish shortly after the last audio chunk if the HTTP stream
	// stays open without a completion event.
	AudioIdleTimeout: 500 * time.Millisecond,
})
```

Gemini's exact TTS capability is exposed through the Interactions HTTP API. The
current TTS docs describe streaming audio as base64 24 kHz PCM chunks; Gemini
Live has WebSocket audio, but it is designed for interactive conversation and
also outputs raw PCM rather than Opus. The platform therefore normalizes Gemini
TTS into PCM frames for the application layer.

## Guidance Text

部分 Provider 支持合成引导词。可以在 session 级别或 segment 级别传入：

```go
session, err := service.OpenSession(ctx, &tts.OpenSessionRequest{
	Provider:     doubao.ProviderName,
	Voice:        "your-speaker-id",
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
go run ./examples/local_cartesia_tts
go run ./examples/local_deepgram_tts
go run ./examples/local_minimax_tts
go run ./examples/local_microsoft_tts
go run ./examples/local_elevenlabs_tts
go run ./examples/local_doubao_tts
go run ./examples/local_inworld_tts
go run ./examples/local_fishaudio_tts
go run ./examples/local_openai_tts
go run ./examples/local_gemini_tts
```

## Development Checks

常用检查命令：

```sh
go test ./...
go test -run '^$' ./...
```

当前仓库里 Provider 真实调用程序依赖外部服务和环境变量，普通单元测试不会访问真实 TTS 服务。
