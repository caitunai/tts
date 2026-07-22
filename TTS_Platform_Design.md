# TTS 平台架构与设计要求

## 1. 平台目标

当前 TTS 平台的核心目标是：**统一封装多个第三方语音合成服务，并向上层应用提供稳定、低延迟、可扩展的音频输出能力**。

需要支持的 TTS 服务包括：

```text
MiniMax
ElevenLabs
豆包 / Doubao
Microsoft Azure TTS
后续其他 HTTP / WebSocket TTS 服务
```

这些平台可能存在以下差异：

```text
1. 接入协议不同
   - HTTP API
   - WebSocket API

2. 输出音频格式不同
   - PCM
   - Opus
   - MP3
   - AAC
   - WAV
   - Ogg Opus

3. 流式能力不同
   - 非流式一次性返回完整音频
   - 流式返回音频 chunk
   - WebSocket 长连接持续追加文本

4. 结束事件不同
   - 有明确 segment end 事件
   - 有 is_final 标记
   - 需要根据 provider 的请求 ID 区分
   - 部分平台可能没有可靠结束事件
```

平台需要屏蔽这些差异，对上层应用提供统一接口。

---

# 2. 核心设计原则

当前设计采用以下原则：

```text
1. Provider 插件化
   每个 TTS 服务独立实现 Provider 接口。

2. Service 统一入口
   上层应用只调用 TTSService，不直接依赖具体厂商 SDK/API。

3. Session + Segment 模型
   支持 WebSocket 长连接追加多段文本，并能区分每段文本的合成结束。

4. Ogg Opus 解封装后透传
   当前 TTS 服务方提供的 Opus 都是经过 Ogg 封装的 Opus。
   平台负责将 Ogg Opus 解封装为裸 Opus packet 后输出，不解码、不重编码。

5. PCM 标准分帧
   如果 TTS 服务只支持 PCM，则平台层只把 PCM 拆分为“对应 Opus 一帧大小”的 PCMFrame，不在平台层编码 Opus。

6. 应用层负责 Opus 编码
   当平台输出 PCMFrame 时，由应用层根据自身场景编码为 Opus。

7. 统一事件流
   平台对上层输出统一的 TTSEvent 流，包括 session start、segment start、audio frame、segment end、session end、error。

8. 应用层负责 Provider 路由
   平台层不根据语言、音色、成本、延迟、稳定性做跨 Provider 路由。
   应用层选择 Provider，平台层只负责按 Provider 名称查找、调用和标准化输出。

9. 可扩展音频管线
   后续支持 MP3/AAC/WAV 解码、Ogg Opus 解封装、重采样、单声道转换等。
```

---

# 3. 总体架构

推荐总体架构如下：

```text
上层业务应用
    │
    │  TTSEvent / AudioFrame
    │
    ▼
TTSService 统一服务层
    │
    ├── ProviderRegistry
    │       ├── 注册 Provider
    │       ├── 根据应用层指定的 Provider 名称查找 Provider
    │       ├── 暴露 Provider 能力给应用层查询
    │       └── 校验 Provider 是否启用
    │
    ├── TTSSessionManager
    │       ├── 管理 WebSocket TTS 长连接
    │       ├── 管理 Segment 队列
    │       ├── 管理 Session 状态
    │       └── 管理取消、超时、关闭
    │
    ├── Provider Adapter 层
    │       ├── MiniMaxProvider
    │       ├── ElevenLabsProvider
    │       ├── DoubaoProvider
    │       ├── MicrosoftProvider
    │       └── FutureProvider
    │
    ├── AudioNormalizer 音频标准化层
    │       ├── OpusPacketNormalizer
    │       ├── PCMFrameSplitter
    │       ├── MP3Decoder
    │       ├── AACDecoder
    │       ├── WAVParser
    │       ├── OggOpusDemuxer
    │       ├── Resampler
    │       └── MonoMixer
    │
    └── EventStream
            ├── EventSessionStart
            ├── EventSegmentStart
            ├── EventAudioFrame
            ├── EventSegmentEnd
            ├── EventSessionEnd
            └── EventError
```

应用层需要在调用平台前完成 Provider 选择：

```text
上层业务应用
    ├── 根据语言、音色、场景选择 Provider
    ├── 根据静态成本、业务偏好、fallback 策略选择 Provider
    └── 将明确的 provider 名称传入 TTSService
```

---

# 4. 模块职责

## 4.1 TTSService

`TTSService` 是平台对上层应用暴露的统一入口。

主要职责：

```text
1. 提供单次文本合成接口
2. 提供 WebSocket 长连接 Session 接口
3. 根据应用层指定的 Provider 名称查找 Provider
4. 屏蔽不同厂商协议差异
5. 输出统一 TTSEvent 流
6. 管理请求生命周期
7. 管理超时、取消、错误封装
8. 决定输出 OpusFrame 还是 PCMFrame
```

核心接口：

```go
type TTSService interface {
	Name() string

	Capabilities(ctx context.Context) (*ServiceCapabilities, error)

	SynthesizeOnce(
		ctx context.Context,
		req *SynthesizeRequest,
	) (<-chan *TTSEvent, error)

	OpenSession(
		ctx context.Context,
		req *OpenSessionRequest,
	) (TTSSession, error)
}
```

请求约束：

```go
type SynthesizeRequest struct {
	RequestID string

	Provider string // 必填，由应用层选择

	Text     string
	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *TTSReferenceAudio

	Output AudioOutputConfig

	Options map[string]any
}

type OpenSessionRequest struct {
	SessionID string

	Provider string // 必填，由应用层选择

	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *TTSReferenceAudio

	Output AudioOutputConfig

	Options map[string]any
}
```

```text
1. Provider 为空时，平台返回 ErrUnsupportedProvider 或参数错误。
2. Provider="auto" 不作为平台内置语义；如果应用需要 auto，应在调用平台前解析成具体 Provider。
3. 平台可以通过 Capabilities 暴露 Provider 能力，但不替应用层做选择。
4. GuidanceText 和 ReferenceAudio 属于可选高级输入；Provider 不支持时返回 ErrUnsupportedFeature。
```

高级输入结构：

```go
type TTSReferenceAudio struct {
	ID string

	Codec     AudioCodec
	Container AudioContainer

	SampleRate int
	Channels   int

	Data []byte
	URL  string

	Text string

	Options map[string]any
}
```

字段说明：

```text
GuidanceText:
  文本引导词，用于影响 TTS 合成风格、语气、角色、朗读方式等。
  GuidanceText 不等同于待合成文本，平台不得把它拼接进 Text。

ReferenceAudio:
  参考音频，用于让支持该能力的 TTS 服务参考一段声音合成目标音色。
  第一阶段只要求支持 WAV 输入。

ReferenceAudio.Data:
  直接传入 wav 文件内容。

ReferenceAudio.URL:
  传入 wav 文件地址。
  是否支持 URL 由具体 Provider 决定。

ReferenceAudio.Text:
  参考音频对应的文本。
  有些 Provider 需要参考音频和对应文本一起传入；不需要时可以为空。
```

---

## 4.2 TTSProvider

`TTSProvider` 是每个厂商服务需要实现的接口。

例如：

```text
MiniMaxProvider
ElevenLabsProvider
DoubaoProvider
MicrosoftProvider
```

主要职责：

```text
1. 负责具体厂商 API 鉴权
2. 负责 HTTP / WebSocket 请求
3. 负责厂商参数映射
4. 负责解析厂商返回事件
5. 负责把厂商事件转换为 ProviderEvent
6. 负责暴露自身能力 Capabilities
7. 负责 GuidanceText / ReferenceAudio 到厂商私有参数的映射
```

核心接口：

```go
type TTSProvider interface {
	Name() string

	Capabilities(ctx context.Context) (*ProviderCapabilities, error)

	SynthesizeOnce(
		ctx context.Context,
		req *ProviderSynthesizeRequest,
	) (<-chan *ProviderEvent, error)

	OpenSession(
		ctx context.Context,
		req *ProviderOpenSessionRequest,
	) (ProviderTTSSession, error)
}
```

---

## 4.3 ProviderRegistry

`ProviderRegistry` 是平台内部的 Provider 注册表，不负责智能路由。

主要职责：

```text
1. 注册和管理 Provider 实例
2. 按应用层传入的 provider 名称查找 Provider
3. 暴露 Provider 能力给应用层查询
4. 在 Provider 不存在或未启用时返回明确错误
```

核心接口：

```go
type ProviderRegistry interface {
	Register(provider TTSProvider) error

	Get(name string) (TTSProvider, bool)

	List() []TTSProvider

	Capabilities(ctx context.Context) ([]*ProviderCapabilities, error)
}
```

平台层不做：

```text
1. 不根据语言自动选择 Provider
2. 不根据音色自动选择 Provider
3. 不根据成本、延迟、稳定性自动切换 Provider
4. 不在已开始输出音频后自动 fallback 到其他 Provider
```

---

## 4.4 TTSSession

`TTSSession` 用于支持 WebSocket TTS 长连接和追加文本。

主要职责：

```text
1. 维护一个 TTS 长连接会话
2. 支持多次 AppendText
3. 每次 AppendText 对应一个 Segment
4. 输出当前 Session 内所有事件
5. 支持 Finish 优雅结束
6. 支持 Close 强制关闭
```

核心接口：

```go
type TTSSession interface {
	ID() string

	ProviderName() string

	Output() AudioOutputConfig

	AppendText(
		ctx context.Context,
		segment *TTSSegmentRequest,
	) error

	Finish(ctx context.Context) error

	Events() <-chan *TTSEvent

	Close() error
}
```

---

## 4.5 ProviderTTSSession

`ProviderTTSSession` 是具体厂商 WebSocket Session 的抽象。

主要职责：

```text
1. 对接具体厂商 WebSocket
2. 发送文本段到厂商
3. 接收厂商音频数据
4. 接收厂商结束事件
5. 转换为 ProviderEvent
```

核心接口：

```go
type ProviderTTSSession interface {
	ID() string

	AppendText(
		ctx context.Context,
		segment *ProviderSegmentRequest,
	) error

	Finish(ctx context.Context) error

	Events() <-chan *ProviderEvent

	Close() error
}
```

---

## 4.6 ProviderEvent

`ProviderEvent` 是 Provider Adapter 输出给平台标准化层的内部事件。

Provider Adapter 必须把厂商私有事件转换为 `ProviderEvent`，平台标准化层再把 `ProviderEvent` 转换为对外的 `TTSEvent`。

```go
type ProviderEventType string

const (
	ProviderEventSessionStart ProviderEventType = "session_start"
	ProviderEventSegmentStart ProviderEventType = "segment_start"
	ProviderEventAudio        ProviderEventType = "audio"
	ProviderEventSegmentEnd   ProviderEventType = "segment_end"
	ProviderEventSessionEnd   ProviderEventType = "session_end"
	ProviderEventError        ProviderEventType = "error"
)
```

推荐结构：

```go
type ProviderEvent struct {
	Type ProviderEventType

	Provider string

	RequestID string
	SessionID string
	SegmentID string

	ProviderRequestID string
	ProviderTaskID    string

	Audio *ProviderAudioChunk

	Final bool

	RawMeta map[string]any
	Error   *TTSError
}

type ProviderAudioChunk struct {
	Codec     AudioCodec
	Container AudioContainer

	SampleRate int
	Channels   int
	Format     PCMFormat

	Data []byte
}
```

约束：

```text
1. ProviderEventAudio.Data 保留 Provider 原始音频 chunk。
2. 当前 Provider 返回 Opus 时，Container 必须标记为 ogg。
3. 裸 Opus packet 只能由 AudioNormalizer 输出到 AudioFrame，不应由 Provider 直接伪造。
4. ProviderEventSegmentEnd 必须携带 SegmentID。
5. 如果厂商只返回 request_id / task_id，Provider Adapter 必须映射到 SegmentID。
```

---

# 5. Session 与 Segment 设计

## 5.1 Session

Session 表示一次 TTS 长连接会话。

一个 Session 中可以包含多个 Segment。

```text
session_001
    ├── segment_001: "你好，欢迎使用柠译。"
    ├── segment_002: "现在我们开始实时翻译。"
    └── segment_003: "请继续说话。"
```

Session 适合以下场景：

```text
1. WebSocket TTS
2. 实时追加文本
3. AI 对话播报
4. 实时翻译播报
5. 多句连续播报
```

---

## 5.2 Segment

Segment 表示一次追加的 TTS 文本。

每个 Segment 必须有独立的生命周期：

```text
segment_start
    ↓
audio_frame...
    ↓
segment_end
```

每段文本都需要有独立的 `SegmentID`。

```go
type TTSSegmentRequest struct {
	SegmentID string

	Text     string
	Language string
	Voice    string

	GuidanceText   string
	ReferenceAudio *TTSReferenceAudio

	Speed   float64
	Pitch   float64
	Volume  float64
	Emotion string

	IsLast bool

	Options map[string]any
}
```

Segment 高级输入规则：

```text
1. TTSSegmentRequest.GuidanceText 可以覆盖 OpenSessionRequest.GuidanceText。
2. TTSSegmentRequest.ReferenceAudio 可以覆盖 OpenSessionRequest.ReferenceAudio。
3. 如果 Segment 未设置 GuidanceText / ReferenceAudio，则继承 Session 创建时的默认值。
4. 如果 Provider 只支持 Session 级参考音频，不支持 Segment 级覆盖，平台需要在 AppendText 时返回 ErrUnsupportedFeature。
5. GuidanceText 不参与 SegmentID 的生成，也不作为待合成文本的一部分。
```

---

## 5.3 Segment 结束判断

每段文本是否合成结束，需要由 Provider Adapter 负责准确判断。

支持以下判断方式：

### 方式一：Provider 有明确结束事件

最理想。

```json
{
  "event": "sentence_end",
  "segment_id": "seg_001"
}
```

映射为：

```text
ProviderEventSegmentEnd
```

---

### 方式二：Provider 音频包带 final 标记

例如：

```json
{
  "audio": "...",
  "is_final": true
}
```

处理方式：

```text
先输出最后一个 audio frame
再输出 segment_end
```

---

### 方式三：Provider 有 request_id / task_id

如果厂商使用自己的请求 ID 标记每段文本：

```text
provider_request_id → segment_id
```

Provider Adapter 需要维护映射关系。

---

### 方式四：无可靠结束事件

不推荐用于实时追加文本场景。

可以作为兜底策略：

```text
超过一段时间没有收到音频，推断 segment 结束
```

但该方式容易把句中停顿误判为结束，因此只适合作为 fallback。

---

## 5.4 Segment 顺序与并发语义

第一阶段平台采用确定性的 FIFO Segment 模型。

```text
1. 一个 Session 内可以多次调用 AppendText。
2. AppendText 只表示把 Segment 加入 Session 队列。
3. 平台对外保证同一个 Session 内同一时刻只有一个 active Segment。
4. 后一个 Segment 的 segment_start 必须出现在前一个 Segment 的 segment_end 或 EventError 之后。
5. 对外 TTSEvent 不允许不同 Segment 的 audio_frame 交错输出。
6. Seq 在 Segment 内从 0 递增，GlobalSeq 在 Session 内全局递增。
```

如果某个 Provider 原生支持“前一段未结束时继续追加文本”，Provider Adapter 可以内部提前发送，但平台对外仍然保持 FIFO 事件顺序。

如果未来需要支持并行 Segment，需要显式新增能力：

```text
SupportsConcurrentSegments bool
```

并且上层应用必须能处理不同 Segment 的音频交错输出。第一阶段不支持该模式。

---

## 5.5 Event Channel 生命周期与背压

`SynthesizeOnce` 和 `Events()` 返回的 channel 必须遵守统一生命周期。

```text
1. 正常完成时：
   输出必要的 segment_end / session_end 后关闭 channel。

2. ctx 被取消时：
   停止读取 Provider，尽快关闭底层连接，输出 EventError 后关闭 channel。

3. Close 被调用时：
   强制关闭底层连接，关闭 channel。

4. Finish 被调用时：
   不再接受新的 AppendText，等待已入队 Segment 完成后输出 session_end 并关闭 channel。

5. Provider 内部失败时：
   输出 EventError；如果 Session 已不可恢复，则随后关闭 channel。
```

实现要求：

```text
1. Close 必须是幂等的。
2. Finish 后再次 AppendText 必须返回 ErrSessionClosed 或 ErrUnsupportedFeature。
3. 事件 channel 建议使用有限缓冲，避免无限内存增长。
4. 当消费者长期不读取事件导致 channel 阻塞时，平台应受 ctx / Close 控制退出，不能泄漏 goroutine。
5. EventError 是对外错误事件；接口返回 error 只用于启动失败、参数错误、Provider 不存在等无法创建事件流的情况。
```

---

# 6. 音频输出策略

平台输出统一的 `AudioFrame`，但 `AudioFrame.Codec` 可以是：

```text
opus
pcm
```

## 6.1 Provider 支持 Ogg Opus 输出

当前阶段假设：TTS 服务方提供的 Opus 都是经过 Ogg 封装的 Opus。

如果 Provider 原生支持 Ogg Opus，并且可以稳定解封装，则平台直接输出：

```text
AudioFrame{Codec: opus}
```

这里的 `AudioFrame{Codec: opus}` 表示裸 Opus packet，不包含 Ogg page / Ogg container。

处理路径：

```text
Provider Ogg Opus Chunk
    ↓
OggOpusDemuxer
    ↓
OpusPacketNormalizer
    ↓
AudioFrame{Codec: opus}
    ↓
上层应用直接使用
```

平台层不做：

```text
不解码
不重采样
不重新编码
不把裸 Opus 重新封装回 Ogg
```

但是平台需要检查：

```text
1. Ogg page 是否完整
2. 一个 Provider chunk 是否包含多个 Ogg page
3. 一个 Ogg page 是否被拆成多个 Provider chunk
4. Ogg granule position / page sequence 是否连续
5. Opus packet 边界是否能准确恢复
6. sample_rate 是否符合要求
7. channels 是否符合要求
8. packet duration / frame_ms 是否符合要求
9. 是否能稳定透传
```

输出约束：

```text
Ogg Opus → OggOpusDemuxer → Opus Packet → AudioFrame
```

---

## 6.2 Provider 只支持 PCM 输出

如果 Provider 只支持 PCM，则平台输出：

```text
AudioFrame{Codec: pcm}
```

处理路径：

```text
Provider PCM Chunk
    ↓
Resample
    ↓
MonoMixer
    ↓
PCMFrameSplitter
    ↓
AudioFrame{Codec: pcm}
    ↓
应用层 OpusEncoder
```

平台只负责把 PCM 拆成 Opus 一帧对应的大小，不在平台层编码 Opus。

---

## 6.3 Provider 输出 MP3 / AAC / WAV

如果 Provider 只支持 MP3、AAC、WAV 等格式，则平台需要转成 PCMFrame。

处理路径：

```text
Provider MP3 / AAC / WAV
    ↓
DecodeToPCM
    ↓
Resample
    ↓
MonoMixer
    ↓
PCMFrameSplitter
    ↓
AudioFrame{Codec: pcm}
```

---

# 7. PCM 分帧要求

PCMFrameSplitter 是当前平台的核心组件之一。

目标是：**把连续 PCM 流拆成应用层 Opus 编码器可以直接消费的一帧一帧 PCM 数据**。

计算公式：

```go
frameBytes := sampleRate * frameMS / 1000 * channels * bytesPerSample
```

默认 PCM 格式：

```text
s16le
```

即：

```text
signed 16-bit little-endian
```

所以：

```text
bytesPerSample = 2
```

常见帧大小：

```text
16kHz / mono / 20ms = 640 bytes
24kHz / mono / 20ms = 960 bytes
48kHz / mono / 20ms = 1920 bytes
```

分帧规则：

```text
1. PCMFrameSplitter 必须维护跨 chunk 缓冲区。
2. 收到不足一帧的 PCM 数据时先缓存，不立即输出 partial frame。
3. 缓冲区达到 frameBytes 时输出一个完整 PCMFrame。
4. Segment 结束时，如果缓冲区仍有不足一帧的数据，默认补静音到完整一帧后输出。
5. 补静音后的最后一帧需要标记 SegmentFinal。
6. 如果应用层显式要求严格无补齐，可以通过 Options 配置丢弃尾部不足一帧的数据。
```

第一阶段推荐默认策略：

```text
tail_policy: pad_silence
```

推荐默认配置：

```text
sample_rate: 16000
channels: 1
frame_ms: 20
pcm_format: s16le
```

适合：

```text
AI 翻译耳机
实时通话翻译
低延迟 WebSocket 传输
移动端播放
BLE 音频传输前编码
```

---

# 8. AudioFrame 设计

基础音频类型：

```go
type AudioCodec string

const (
	CodecAuto AudioCodec = "auto"
	CodecOpus AudioCodec = "opus"
	CodecPCM  AudioCodec = "pcm"
	CodecMP3  AudioCodec = "mp3"
	CodecAAC  AudioCodec = "aac"
	CodecWAV  AudioCodec = "wav"
)

type AudioContainer string

const (
	ContainerNone AudioContainer = ""
	ContainerRaw  AudioContainer = "raw"
	ContainerOgg  AudioContainer = "ogg"
	ContainerWAV  AudioContainer = "wav"
)
```

平台最终输出的音频帧结构：

```go
type AudioFrame struct {
	RequestID  string
	SessionID  string
	SegmentID string

	Codec     AudioCodec
	Container AudioContainer

	SampleRate       int
	Channels         int
	FrameMS          int
	PacketDurationMS int
	Format           PCMFormat

	Seq       uint32
	GlobalSeq uint64

	TimestampMS int64

	Data []byte

	SegmentFinal bool
	SessionFinal bool
}
```

字段说明：

```text
RequestID:
  单次合成请求 ID。

SessionID:
  WebSocket 长连接会话 ID。

SegmentID:
  当前音频属于哪一段文本。

Codec:
  opus 或 pcm。

Container:
  AudioFrame 的数据容器。
  Codec=opus 时，对外输出应为 raw，表示裸 Opus packet。
  Provider 输入的 Ogg Opus 不直接透出给应用层。

SampleRate:
  采样率，例如 16000 / 24000 / 48000。

Channels:
  通道数，默认 1。

FrameMS:
  PCM 帧长，默认 20ms。

PacketDurationMS:
  Opus packet 实际时长。
  对 PCMFrame 通常等于 FrameMS。

Format:
  PCM 时通常是 s16le。

Seq:
  Segment 内部序号，建议每个 Segment 从 0 开始。

GlobalSeq:
  Session 内全局递增序号。

TimestampMS:
  当前音频帧在 Segment 内的时间戳。

Data:
  如果 Codec=opus，则是裸 Opus packet。
  如果 Codec=pcm，则是固定大小 PCM frame。

SegmentFinal:
  当前帧是否是当前 Segment 的最后一帧。

SessionFinal:
  当前帧是否是整个 Session 的最后一帧。
```

---

# 9. TTSEvent 事件流设计

平台对上层应用输出统一事件：

```go
type TTSEventType string

const (
	EventSessionStart TTSEventType = "session_start"
	EventSegmentStart TTSEventType = "segment_start"
	EventAudioFrame   TTSEventType = "audio_frame"
	EventSegmentEnd   TTSEventType = "segment_end"
	EventSessionEnd   TTSEventType = "session_end"
	EventError        TTSEventType = "error"
)
```

事件结构：

```go
type TTSEvent struct {
	Type TTSEventType

	RequestID string
	SessionID string
	SegmentID string

	Audio *AudioFrame

	Meta  map[string]any
	Error *TTSError
}
```

事件顺序示例：

```text
session_start

segment_start seg_001
audio_frame seg_001 seq=0
audio_frame seg_001 seq=1
audio_frame seg_001 seq=2
segment_end seg_001

segment_start seg_002
audio_frame seg_002 seq=0
audio_frame seg_002 seq=1
segment_end seg_002

session_end
```

---

# 10. 输出配置要求

`AudioOutputConfig` 控制平台最终输出 OpusFrame 还是 PCMFrame。

```go
type AudioOutputConfig struct {
	PreferCodec AudioCodec
	ActualCodec AudioCodec

	SampleRate int
	Channels   int
	FrameMS    int

	PCMFormat PCMFormat

	AllowOggOpusDemux     bool
	AllowRawOpusOutput   bool
	AllowPCMFrameOutput  bool
}
```

## 10.1 PreferCodec

支持：

```text
auto
opus
pcm
```

`auto` 使用基础音频类型中的 `CodecAuto`。

### auto

默认模式。

```text
1. 如果当前 Provider 支持 Ogg Opus，则解封装后输出 OpusFrame
2. 否则如果 Provider 支持 PCM，则输出 PCMFrame
3. 否则 MP3/AAC/WAV 解码成 PCMFrame
```

### opus

优先使用当前 Provider 原生 Ogg Opus。

但是平台不负责把 PCM 编码成 Opus，所以：

```text
如果当前 Provider 不支持 Ogg Opus，实际输出会 fallback 到 PCMFrame
这里的 fallback 只发生在当前 Provider 的 codec 层，不切换到其他 Provider。
```

### pcm

强制输出 PCMFrame。

即使 Provider 支持 Ogg Opus，也可以解码为 PCMFrame 输出。

适合需要统一后处理的场景，例如：

```text
音量分析
混音
波形展示
应用层统一编码参数
```

---

# 11. Provider 能力要求

每个 Provider 必须声明自身能力。

```go
type ProviderCapabilities struct {
	Name string

	Transports []TransportType

	SupportsStreaming       bool
	SupportsAppendText      bool
	SupportsSSML            bool
	SupportsVoiceClone      bool
	SupportsGuidanceText    bool
	SupportsReferenceAudio  bool
	SupportsEmotion         bool
	SupportsSpeed           bool
	SupportsPitch           bool
	SupportsVolume          bool

	OutputCodecs      []AudioCodec
	OutputContainers  []AudioContainer
	OutputSampleRates []int
	OutputChannels    []int

	ReferenceAudioCodecs              []AudioCodec
	ReferenceAudioContainers          []AudioContainer
	MaxReferenceAudioBytes            int64
	MinReferenceAudioMS               int
	MaxReferenceAudioMS               int
	RequiresReferenceText             bool
	SupportsReferenceAudioURL         bool
	SupportsSegmentLevelGuidance      bool
	SupportsSegmentLevelReferenceAudio bool

	SupportsSegmentEndEvent bool

	SupportsOggOpusOutput bool

	SupportsPCMOutput bool

	Voices    []VoiceInfo
	Languages []LanguageInfo
}
```

其中 `Voices` 和 `Languages` 只有在 Provider 能提供完整、稳定的有限列表时才填写。
为空表示平台层不做语种或音色 allowlist 校验，并不表示 Provider 不支持语种或音色。
这类兼容性错误应由具体上游 Provider/API 返回，避免第三方服务新增语种、音色或自定义音色后被平台静态列表误拦截。

这些能力主要用于：

```text
1. 应用层选择 Provider
2. 平台层校验请求是否能由指定 Provider 执行
3. 平台层决定当前 Provider 内的音频标准化路径
4. 平台层校验 GuidanceText / ReferenceAudio 是否可用于指定 Provider
```

---

## 11.1 高级输入能力

部分 TTS 服务支持文本引导词和参考音频。

### GuidanceText

`GuidanceText` 表示合成引导词，用于影响合成风格，但不作为待朗读文本。

常见用途：

```text
1. 指定说话风格，例如温柔、正式、兴奋、低声。
2. 指定角色或场景，例如客服播报、儿童故事、新闻播报。
3. 指定发音或韵律倾向。
4. 为 Provider 私有 prompt / instruction 字段提供统一入口。
```

Provider Adapter 要求：

```text
1. 如果 Provider 支持类似 prompt / instruction / style_prompt 字段，映射 GuidanceText。
2. 如果 Provider 不支持 GuidanceText，但请求传入了 GuidanceText，返回 ErrUnsupportedFeature。
3. GuidanceText 不能拼接到 Text 中，避免引导词被真实朗读。
4. 对 WebSocket Session，如果 Provider 只支持创建 Session 时设置引导词，则 Segment 级 GuidanceText 覆盖应返回 ErrUnsupportedFeature。
```

### ReferenceAudio

`ReferenceAudio` 表示参考音频，用于让支持该能力的 TTS 服务参考一段 wav 声音合成目标音色。

第一阶段要求：

```text
1. 平台统一把该能力称为 ReferenceAudio，不直接暴露厂商私有命名。
2. ReferenceAudio 第一阶段只要求支持 WAV。
3. ReferenceAudio 可以通过 Data 或 URL 提供。
4. 如果 Provider 需要参考音频对应文本，则 RequiresReferenceText=true，且 ReferenceAudio.Text 必填。
5. 平台不在第一阶段实现声纹注册、长期音色管理或跨请求缓存。
6. Provider Adapter 负责把 ReferenceAudio 映射到厂商的 voice reference / prompt audio / zero-shot voice 字段。
```

校验规则：

```text
1. 请求传入 ReferenceAudio，但 Provider 不支持时，返回 ErrUnsupportedFeature。
2. ReferenceAudio.Container 必须在 ProviderCapabilities.ReferenceAudioContainers 中。
3. ReferenceAudio.Codec 必须在 ProviderCapabilities.ReferenceAudioCodecs 中。
4. ReferenceAudio.Data 和 ReferenceAudio.URL 至少提供一个。
5. Provider 不支持 URL 时，如果只传 URL，返回 ErrUnsupportedFeature。
6. 超过 MaxReferenceAudioBytes 时返回 ErrReferenceAudioTooLarge。
7. 音频时长不在 MinReferenceAudioMS / MaxReferenceAudioMS 范围内时返回 ErrInvalidReferenceAudio。
8. Provider 需要 ReferenceAudio.Text 但未传时返回 ErrInvalidReferenceAudio。
```

推荐默认能力声明：

```go
ReferenceAudioCodecs:     []AudioCodec{CodecWAV},
ReferenceAudioContainers: []AudioContainer{ContainerWAV},
```

---

# 12. Provider 选择边界

Provider 选择由应用层负责，平台层不内置跨 Provider 路由器。

应用层可以根据以下维度选择 Provider：

```text
1. 用户是否指定 provider
2. Provider 是否启用
3. 是否支持请求语言
4. 是否支持请求音色
5. 是否支持 HTTP / WebSocket
6. 是否支持追加文本
7. 是否支持流式输出
8. 是否支持 Ogg Opus 输出
9. 是否支持 PCM 输出
10. 是否支持 GuidanceText
11. 是否支持 ReferenceAudio
12. 静态成本配置
13. 应用层 fallback 策略
```

第一阶段不在平台层维护 Provider 运行时状态，例如当前错误率、实时限流状态、熔断状态、P95 延迟等。

平台层只负责以下行为：

```text
1. 根据请求中的 Provider 字段查找 Provider。
2. Provider 不存在时返回 ErrUnsupportedProvider。
3. Provider 不支持请求特性时返回 ErrUnsupportedFeature / ErrUnsupportedCodec / ErrUnsupportedVoice / ErrUnsupportedLanguage。
4. 在当前 Provider 内选择音频标准化路径。
5. 不跨 Provider 重试或 fallback。
```

实时追加文本场景，应用层推荐优先选择：

```text
1. WebSocket + 支持 AppendText + 明确 SegmentEnd
2. 支持 Ogg Opus 输出，平台可解封装为裸 Opus packet
3. 支持 PCM 流式输出
4. 支持 MP3/AAC 流式输出
```

单次合成场景，应用层推荐优先选择：

```text
1. 支持流式输出
2. 支持 Ogg Opus 输出，平台可解封装为裸 Opus packet
3. 支持 PCM 输出
4. 支持 MP3/WAV 输出
```

低延迟翻译场景，应用层推荐优先选择：

```text
1. WebSocket
2. 首包延迟低
3. 支持 16k mono 20ms
4. 支持可靠 segment end
```

---

# 13. WebSocket 追加文本要求

对于支持追加文本的 TTS 服务，平台需要提供长连接能力。

## 13.1 创建 Session

```go
session, err := ttsService.OpenSession(ctx, &OpenSessionRequest{
	SessionID: "sess_001",
	Provider:  "doubao",
	Language:  "zh",
	Voice:     "female_01",
	Output:    DefaultAudioOutputConfig(),
})
```

## 13.2 追加文本

```go
err = session.AppendText(ctx, &TTSSegmentRequest{
	SegmentID: "seg_001",
	Text:      "你好，欢迎使用柠译。",
})
```

## 13.3 继续追加文本

```go
err = session.AppendText(ctx, &TTSSegmentRequest{
	SegmentID: "seg_002",
	Text:      "现在我们开始实时翻译。",
})
```

## 13.4 优雅结束 Session

```go
err = session.Finish(ctx)
```

## 13.5 强制关闭 Session

```go
err = session.Close()
```

---

# 14. Session 状态机

Session 状态建议：

```text
Idle
  ↓
Opening
  ↓
Ready
  ↓
Synthesizing
  ↓
Ready
  ↓
Finishing
  ↓
Closed
```

异常情况下：

```text
Opening → Failed
Ready → Closed
Synthesizing → Failed
Finishing → Failed
```

---

# 15. Segment 状态机

Segment 状态建议：

```text
Pending
  ↓
SentToProvider
  ↓
ReceivingAudio
  ↓
Ended
```

异常情况下：

```text
Pending → Failed
SentToProvider → Failed
ReceivingAudio → Failed
```

每个 Segment 必须最终进入：

```text
Ended
```

或者：

```text
Failed
```

不能无限悬挂。

---

# 16. 错误处理要求

统一错误结构：

```go
type TTSError struct {
	Code TTSErrorCode

	Message string

	Provider  string
	SessionID string
	SegmentID string

	Cause error

	Retryable bool
}
```

错误码建议：

```go
type TTSErrorCode string

const (
	ErrUnsupportedProvider    TTSErrorCode = "unsupported_provider"
	ErrUnsupportedFeature     TTSErrorCode = "unsupported_feature"
	ErrUnsupportedCodec       TTSErrorCode = "unsupported_codec"
	ErrUnsupportedVoice       TTSErrorCode = "unsupported_voice"
	ErrUnsupportedLanguage    TTSErrorCode = "unsupported_language"
	ErrInvalidGuidanceText    TTSErrorCode = "invalid_guidance_text"
	ErrInvalidReferenceAudio  TTSErrorCode = "invalid_reference_audio"
	ErrReferenceAudioTooLarge TTSErrorCode = "reference_audio_too_large"

	ErrProviderUnavailable TTSErrorCode = "provider_unavailable"
	ErrProviderTimeout     TTSErrorCode = "provider_timeout"
	ErrProviderAuthFailed  TTSErrorCode = "provider_auth_failed"
	ErrProviderRateLimited TTSErrorCode = "provider_rate_limited"

	ErrSessionClosed TTSErrorCode = "session_closed"
	ErrSegmentFailed TTSErrorCode = "segment_failed"

	ErrAudioDecodeFailed    TTSErrorCode = "audio_decode_failed"
	ErrAudioNormalizeFailed TTSErrorCode = "audio_normalize_failed"

	ErrInternal             TTSErrorCode = "internal_error"
)
```

错误处理策略：

```text
1. Provider 鉴权失败：
   不重试，直接 EventError。

2. Provider 超时：
   输出 EventError。
   平台层不跨 Provider fallback。

3. Provider 限流：
   输出 EventError。
   是否改用其他 Provider 由应用层决定。

4. Segment 合成失败：
   输出 EventError，并标明 SegmentID。

5. Session 断开：
   输出 EventError，然后关闭事件流。

6. 音频解码失败：
   输出 EventError。
   如果当前 Provider 还支持其他 codec，平台可以在尚未输出任何音频前尝试当前 Provider 内的 codec fallback。
   已经输出音频后，不再切换 codec 或 Provider。
```

流式输出的失败边界：

```text
1. 如果尚未输出任何 audio_frame，可以返回启动错误或输出 EventError 后结束。
2. 如果已经输出当前 Segment 的部分 audio_frame，平台不能自动重试同一文本。
3. 如果应用层需要跨 Provider fallback，应重新发起新的请求，并自行处理重复播报、音色变化和上下文一致性。
```

---

# 17. 应用层职责

平台层不负责最终业务播放和 Opus 编码策略。

应用层职责：

```text
1. 如果收到 AudioFrame{Codec: opus}
   - 直接发送
   - 直接播放
   - 直接转发
   - 直接写入网络协议

2. 如果收到 AudioFrame{Codec: pcm}
   - 根据自己的码率、帧长、application 编码为 Opus
   - 再发送或播放

3. 控制 Opus 编码参数
   - bitrate
   - complexity
   - application: voip / audio / lowdelay
   - FEC
   - DTX

4. 根据传输方式做封包
   - WebSocket
   - BLE
   - HTTP Stream
   - 本地播放
```

---

# 18. 推荐默认音频参数

针对你的实时翻译、AI 耳机、WebSocket + Opus 场景，推荐默认参数：

```text
sample_rate: 16000
channels: 1
frame_ms: 20
pcm_format: s16le
prefer_codec: auto
allow_ogg_opus_demux: true
allow_raw_opus_output: true
allow_pcm_frame_output: true
```

应用层 Opus 编码建议：

```text
sample_rate: 16000
channels: 1
frame_ms: 20
bitrate: 16000 ~ 24000 bps
application: voip
```

普通 APP 播放可以使用：

```text
sample_rate: 24000
channels: 1
frame_ms: 20
bitrate: 24000 ~ 48000 bps
application: audio
```

---

# 19. 推荐目录结构

```text
tts-platform/
  internal/
    tts/
      service.go
      session.go
      request.go
      event.go
      audio.go
      error.go
      capability.go
      registry.go

    provider/
      provider.go
      registry.go
      capability.go

      minimax/
        provider.go
        session.go
        websocket.go
        http.go
        mapper.go

      elevenlabs/
        provider.go
        session.go
        websocket.go
        http.go
        mapper.go

      doubao/
        provider.go
        session.go
        websocket.go
        mapper.go

      microsoft/
        provider.go
        speechsdk.go
        rest.go
        mapper.go

    audio/
      types.go

      normalize/
        normalizer.go

      pcm/
        splitter.go
        buffer.go
        silence.go

      opus/
        packet_normalizer.go
        ogg_demuxer.go

      decoder/
        mp3.go
        aac.go
        wav.go
        opus.go

      resample/
        resampler.go

      mixer/
        mono.go

    transport/
      websocket/
        server.go
        protocol.go

      http/
        stream.go

    config/
      providers.yaml
      voices.yaml
```

---

# 20. 最终架构总结

当前 TTS 平台可以总结为：

```text
1. TTSService 是统一入口。
2. TTSProvider 是厂商接入规范。
3. TTSSession 支持 WebSocket 长连接追加文本。
4. TTSSegmentRequest 表示一次追加文本。
5. 每个 Segment 必须有明确的开始和结束事件。
6. Provider 支持 Ogg Opus 时，平台解封装为裸 Opus packet 后透传。
7. Provider 只支持 PCM 时，平台拆分成固定大小 PCMFrame。
8. 平台不负责把 PCM 编码成 Opus。
9. 应用层根据场景自行编码 Opus。
10. ProviderEvent 是厂商侧标准事件。
11. TTSEvent 是平台对上层输出的统一事件。
12. AudioNormalizer 负责 Ogg Opus 解封装、Opus packet 整理、PCM 分帧、MP3/AAC/WAV 解码等。
13. Provider 选择由应用层负责，平台通过 ProviderRegistry 按名称查找 Provider。
14. GuidanceText 为文本引导词能力，平台只校验和透传，不拼接到待合成文本。
15. ReferenceAudio 为参考 wav 音频能力，用于支持根据参考声音合成音色的 Provider。
16. 后续新增 MiniMax、ElevenLabs、豆包、Microsoft 等服务，只需要实现 TTSProvider 和可选的 ProviderTTSSession。
```
