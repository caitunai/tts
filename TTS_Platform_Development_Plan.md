# TTS 平台开发计划

本文档基于 `TTS_Platform_Design.md`，用于指导第一阶段工程落地。

第一阶段目标不是做完整平台化治理，而是先完成统一接口、音频标准化、Session/Segment 事件流，以及 MiniMax、ElevenLabs、豆包 / Doubao、Microsoft Azure TTS 等 Provider 的可用对接。

---

# 1. 开发目标

## 1.1 第一阶段目标

```text
1. 建立稳定的 Go 包结构和核心接口。
2. 实现 TTSService / TTSProvider / ProviderRegistry / TTSSession 等核心抽象。
3. 实现统一 TTSEvent / ProviderEvent 事件流。
4. 实现 Ogg Opus 解封装为裸 Opus packet 的输出路径。
5. 实现 PCM 分帧路径。
6. 支持 GuidanceText 文本引导词。
7. 支持 ReferenceAudio 参考 wav 音频。
8. 完成首批 Provider 对接。
9. 提供可运行的示例和基础测试。
```

## 1.2 第一阶段不做

```text
1. 不做平台层 ProviderRouter。
2. 不做 Provider 运行时状态管理。
3. 不做平台层跨 Provider fallback。
4. 不做完整可观测性系统。
5. 不做声纹注册、长期音色管理、参考音频缓存。
6. 不做平台层 PCM 到 Opus 编码。
```

Provider 选择、跨 Provider fallback、成本优先级、限流策略由应用层负责。

---

# 2. 推荐开发顺序

```text
阶段 0：工程骨架
阶段 1：核心类型与接口
阶段 2：ProviderRegistry 与基础 Service
阶段 3：音频标准化管线
阶段 4：事件流与 Session/Segment 管理
阶段 5：Mock Provider 与端到端测试
阶段 6：真实 Provider 对接
阶段 7：示例、文档和稳定性补齐
```

建议严格按顺序推进。真实 Provider 对接不要太早开始，否则容易在接口还不稳定时反复返工。

---

# 3. 阶段 0：工程骨架

## 3.1 目标

建立目录结构、基础包边界和最小可编译工程。

## 3.2 任务

```text
1. 创建 internal/tts 核心包。
2. 创建 internal/provider Provider 抽象包。
3. 创建 internal/audio 音频类型与管线包。
4. 创建 internal/config 配置包。
5. 创建 examples 目录，用于放置最小调用示例。
6. 创建测试目录和基础测试入口。
```

推荐目录：

```text
internal/
  tts/
  provider/
  audio/
  config/
examples/
```

## 3.3 验收标准

```text
1. go test ./... 可以运行。
2. 所有包可以正常编译。
3. 暂时没有真实 Provider 时，工程仍然可测试。
```

---

# 4. 阶段 1：核心类型与接口

## 4.1 目标

把设计文档中的核心数据结构落成 Go 类型。

## 4.2 任务

```text
1. 定义 AudioCodec / AudioContainer / PCMFormat。
2. 定义 AudioFrame。
3. 定义 TTSEvent / TTSEventType。
4. 定义 ProviderEvent / ProviderEventType / ProviderAudioChunk。
5. 定义 SynthesizeRequest / OpenSessionRequest / TTSSegmentRequest。
6. 定义 TTSReferenceAudio。
7. 定义 AudioOutputConfig。
8. 定义 ProviderCapabilities / ServiceCapabilities。
9. 定义 TTSError / TTSErrorCode。
10. 定义 TTSService / TTSProvider / TTSSession / ProviderTTSSession 接口。
```

## 4.3 关键要求

```text
1. Provider 字段必须由应用层显式传入。
2. Provider="auto" 不作为平台内置语义。
3. GuidanceText 不能拼接进 Text。
4. ReferenceAudio 第一阶段只要求支持 wav。
5. ProviderEventAudio.Data 保留 Provider 原始音频 chunk。
6. AudioFrame{Codec: opus} 输出裸 Opus packet。
```

## 4.4 验收标准

```text
1. 核心类型不依赖具体 Provider。
2. 类型命名和设计文档保持一致。
3. 单元测试覆盖错误码、默认输出配置、基础能力声明。
```

---

# 5. 阶段 2：ProviderRegistry 与基础 Service

## 5.1 目标

实现平台统一入口和 Provider 注册查询能力。

## 5.2 任务

```text
1. 实现 ProviderRegistry。
2. 支持 Register / Get / List / Capabilities。
3. 实现默认 TTSService。
4. 在 SynthesizeOnce 中根据 req.Provider 查找 Provider。
5. 在 OpenSession 中根据 req.Provider 查找 Provider。
6. Provider 不存在时返回 ErrUnsupportedProvider。
7. Provider 不支持特性时返回 ErrUnsupportedFeature / ErrUnsupportedCodec 等错误。
```

## 5.3 验收标准

```text
1. 注册多个 Mock Provider 后可以按名称获取。
2. Provider 为空时请求失败。
3. Provider="auto" 不触发自动选择。
4. Capabilities 能汇总已注册 Provider 的能力。
```

---

# 6. 阶段 3：音频标准化管线

## 6.1 目标

完成 Provider 原始音频到平台 AudioFrame 的标准化路径。

## 6.2 Ogg Opus 路径

任务：

```text
1. 实现 OggOpusDemuxer。
2. 支持跨 chunk 缓冲。
3. 支持一个 Provider chunk 中包含多个 Ogg page。
4. 支持一个 Ogg page 被拆成多个 Provider chunk。
5. 输出裸 Opus packet。
6. 生成 AudioFrame{Codec: opus, Container: raw}。
7. 填充 Seq / GlobalSeq / PacketDurationMS / TimestampMS。
```

验收标准：

```text
1. 输入 Ogg Opus 数据后能输出裸 Opus packet。
2. 对拆包、粘包场景有单元测试。
3. 不进行 Opus 解码、不重采样、不重新编码。
```

## 6.3 PCM 路径

任务：

```text
1. 实现 PCMFrameSplitter。
2. 支持跨 chunk 缓冲。
3. 按 sample_rate / channels / frame_ms / bytes_per_sample 计算 frameBytes。
4. 默认尾帧策略为 pad_silence。
5. 支持 Options 配置丢弃不足一帧的尾部数据。
6. 输出 AudioFrame{Codec: pcm}。
```

验收标准：

```text
1. 16kHz / mono / 20ms 输出 640 bytes PCM frame。
2. 24kHz / mono / 20ms 输出 960 bytes PCM frame。
3. 48kHz / mono / 20ms 输出 1920 bytes PCM frame。
4. 尾帧补静音测试通过。
```

## 6.4 MP3 / AAC / WAV 路径

第一阶段建议先实现 WAV 解析，MP3 / AAC 可作为后续增强。

任务：

```text
1. 实现 WAVParser。
2. WAV 解码为 PCM 后进入 PCMFrameSplitter。
3. MP3 / AAC 先保留接口和错误码。
```

验收标准：

```text
1. wav 输入可以输出 PCMFrame。
2. 未实现的 MP3 / AAC 返回 ErrUnsupportedCodec 或 ErrAudioDecodeFailed。
```

---

# 7. 阶段 4：事件流与 Session/Segment 管理

## 7.1 目标

实现统一事件流、FIFO Segment 队列和 channel 生命周期。

## 7.2 任务

```text
1. 实现 SynthesizeOnce 的 ProviderEvent 到 TTSEvent 转换。
2. 实现 TTSSessionManager。
3. 实现 FIFO Segment 队列。
4. 保证同一 Session 内只有一个 active Segment。
5. 保证不同 Segment 的 audio_frame 不交错输出。
6. 实现 Finish。
7. 实现 Close。
8. 实现 ctx cancel 处理。
9. 实现 EventError 输出和 channel 关闭。
```

## 7.3 验收标准

```text
1. segment_start / audio_frame / segment_end 顺序稳定。
2. 后一个 segment_start 不早于前一个 segment_end 或 EventError。
3. Finish 后不再接受 AppendText。
4. Close 可重复调用且不 panic。
5. 消费者停止读取时，ctx cancel 或 Close 能释放 goroutine。
```

---

# 8. 阶段 5：Mock Provider 与端到端测试

## 8.1 目标

在接真实厂商前，用 Mock Provider 验证平台契约。

## 8.2 Mock Provider 类型

```text
1. MockPCMProvider：
   输出 PCM chunk，用于验证 PCMFrameSplitter。

2. MockOggOpusProvider：
   输出 Ogg Opus chunk，用于验证 OggOpusDemuxer。

3. MockSessionProvider：
   支持 AppendText，用于验证 Session/Segment。

4. MockErrorProvider：
   模拟超时、限流、鉴权失败、音频解码失败等错误。

5. MockAdvancedInputProvider：
   验证 GuidanceText / ReferenceAudio 能力校验和参数传递。
```

## 8.3 验收标准

```text
1. SynthesizeOnce 端到端测试通过。
2. OpenSession + AppendText + Finish 端到端测试通过。
3. GuidanceText 不被拼接到 Text。
4. ReferenceAudio 不支持时返回 ErrUnsupportedFeature。
5. ReferenceAudio 超限时返回 ErrReferenceAudioTooLarge。
6. 已输出音频后不做平台层跨 Provider fallback。
```

---

# 9. 阶段 6：真实 Provider 对接

## 9.1 接入顺序建议

```text
1. 先接一个 HTTP 单次合成 Provider。
2. 再接一个 WebSocket 流式 Provider。
3. 再接支持 Ogg Opus 输出的 Provider。
4. 最后接支持 GuidanceText / ReferenceAudio 的 Provider。
```

如果某个真实 Provider 同时支持多项能力，可以优先接它，用来验证整体设计。

## 9.2 每个 Provider 的标准任务

```text
1. 实现 provider.go。
2. 实现 capabilities。
3. 实现请求参数映射。
4. 实现鉴权。
5. 实现 HTTP / WebSocket 调用。
6. 实现厂商事件解析。
7. 转换为 ProviderEvent。
8. 映射 GuidanceText。
9. 映射 ReferenceAudio。
10. 处理厂商错误码到 TTSError。
11. 编写单元测试和最小集成示例。
```

## 9.3 MiniMax Provider

计划任务：

```text
1. 确认支持的传输协议。
2. 确认输出音频格式是否为 Ogg Opus / PCM / MP3 / WAV。
3. 确认是否支持 streaming。
4. 确认是否支持 AppendText。
5. 确认是否支持 GuidanceText。
6. 确认是否支持 ReferenceAudio wav。
7. 实现请求映射和事件转换。
```

## 9.4 ElevenLabs Provider

计划任务：

```text
1. 确认 HTTP / WebSocket 接口形态。
2. 确认音频输出容器。
3. 确认 voice / model / stability / similarity 等参数映射。
4. 确认 GuidanceText 对应字段。
5. 确认 ReferenceAudio 或 voice clone / prompt audio 支持方式。
6. 实现基础合成和流式输出。
```

## 9.5 Doubao Provider

计划任务：

```text
1. 确认 WebSocket 流式协议。
2. 确认是否支持长连接 AppendText。
3. 确认 SegmentEnd 判断方式。
4. 确认 Ogg Opus 输出格式。
5. 确认 GuidanceText / ReferenceAudio 支持情况。
6. 实现 Session 模式。
```

## 9.6 Microsoft Azure TTS Provider

计划任务：

```text
1. 确认使用 REST、WebSocket 或 SDK。
2. 确认 SSML 支持方式。
3. 确认输出音频格式和容器。
4. 确认是否支持 GuidanceText。
5. 确认是否支持 ReferenceAudio。
6. 实现单次合成路径。
```

## 9.7 Provider 验收标准

每个真实 Provider 至少满足：

```text
1. Capabilities 声明准确。
2. SynthesizeOnce 可运行。
3. 如果支持 WebSocket，则 OpenSession 可运行。
4. 音频输出能进入统一 AudioFrame。
5. 错误能转换为 TTSError。
6. 不支持的高级能力会明确失败。
7. 有一个 examples 示例。
```

---

# 10. 阶段 7：示例、文档和稳定性补齐

## 10.1 示例

建议提供：

```text
1. examples/synthesize_once_pcm
2. examples/synthesize_once_ogg_opus
3. examples/session_append_text
4. examples/guidance_text
5. examples/reference_audio
```

## 10.2 文档

建议补充：

```text
1. Provider 接入指南。
2. AudioFrame 消费指南。
3. GuidanceText 使用说明。
4. ReferenceAudio 使用说明。
5. 常见错误码说明。
```

## 10.3 稳定性检查

```text
1. go test ./...
2. go test -race ./...
3. 长文本合成测试。
4. 多 Segment 连续追加测试。
5. ctx cancel 测试。
6. Close 幂等测试。
7. 大 ReferenceAudio 拒绝测试。
```

---

# 11. 里程碑计划

## M1：核心类型可编译

交付物：

```text
1. 核心类型。
2. 核心接口。
3. 错误码。
4. 默认配置。
```

验收：

```text
go test ./...
```

## M2：Mock Provider 端到端跑通

交付物：

```text
1. ProviderRegistry。
2. 默认 TTSService。
3. MockPCMProvider。
4. MockSessionProvider。
5. 基础 TTSEvent 输出。
```

验收：

```text
1. 单次合成端到端测试通过。
2. Session 追加文本端到端测试通过。
```

## M3：音频管线可用

交付物：

```text
1. PCMFrameSplitter。
2. OggOpusDemuxer。
3. WAVParser。
4. AudioNormalizer。
```

验收：

```text
1. PCM 固定帧输出正确。
2. Ogg Opus 能输出裸 Opus packet。
3. WAV 能转换为 PCMFrame。
```

## M4：首个真实 Provider 可用

交付物：

```text
1. 一个真实 Provider 的 SynthesizeOnce。
2. 对应示例。
3. 基础错误映射。
```

验收：

```text
1. 可真实调用并收到 AudioFrame。
2. 不支持的能力有明确错误。
```

## M5：WebSocket Session 可用

交付物：

```text
1. 一个支持 WebSocket 的真实 Provider。
2. OpenSession / AppendText / Finish / Close。
3. Segment FIFO 输出。
```

验收：

```text
1. 多 Segment 顺序输出。
2. SegmentEnd 可靠。
3. 取消和关闭不泄漏 goroutine。
```

## M6：高级输入可用

交付物：

```text
1. GuidanceText Provider 映射。
2. ReferenceAudio Provider 映射。
3. 对应示例。
4. 能力校验测试。
```

验收：

```text
1. GuidanceText 不被朗读。
2. ReferenceAudio wav 可被支持的 Provider 使用。
3. 不支持的 Provider 返回 ErrUnsupportedFeature。
```

---

# 12. 风险与处理策略

## 12.1 Ogg Opus 解封装复杂度

风险：

```text
Provider chunk 可能拆分 Ogg page，也可能粘连多个 page。
```

处理：

```text
先用 MockOggOpusProvider 构造拆包和粘包测试，再接真实 Provider。
```

## 12.2 SegmentEnd 不可靠

风险：

```text
部分 Provider 没有可靠的 segment end。
```

处理：

```text
第一阶段不推荐用于实时追加文本场景。
只能作为 SynthesizeOnce 或非严格实时场景的 fallback。
```

## 12.3 ReferenceAudio 厂商差异大

风险：

```text
有些 Provider 要 wav 数据，有些要 URL，有些还要参考文本。
```

处理：

```text
统一抽象为 TTSReferenceAudio，由 ProviderCapabilities 声明细节。
Provider Adapter 负责私有参数映射。
```

## 12.4 WebSocket 生命周期容易泄漏

风险：

```text
ctx cancel、Close、Finish、Provider 断开可能交错发生。
```

处理：

```text
Close 必须幂等。
事件 channel 必须有关闭规则。
用 race test 和 goroutine 泄漏测试覆盖。
```

---

# 13. 推荐当前下一步

建议马上开始：

```text
1. 创建 internal/tts、internal/provider、internal/audio 目录。
2. 先实现核心类型和接口。
3. 写 MockPCMProvider。
4. 跑通 SynthesizeOnce 的最小端到端链路。
```

完成这一步后，再实现 Ogg Opus 和 Session。这样可以尽快得到一个可运行的骨架，后续接真实 Provider 时不会悬空。
