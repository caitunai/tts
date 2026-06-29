# TTS Platform Examples

This directory will contain runnable examples for the TTS platform.

Planned examples:

```text
1. synthesize_once_pcm
2. synthesize_once_ogg_opus
3. session_append_text
4. guidance_text
5. reference_audio
```

## local_http_tts

Run against a real local HTTP TTS service:

```sh
export TTS_HTTP_TOKEN="your-token"
go run ./examples/local_http_tts \
  -endpoint http://127.0.0.1:9012/v1/audio/speech \
  -voice serena \
  -language Chinese \
  -text "你好，今天天气怎么样呢？" \
  -out local_http_tts_16k_mono_s16le.pcm
```

Play the generated PCM:

```sh
ffplay -f s16le -ar 16000 -ac 1 local_http_tts_16k_mono_s16le.pcm
```

## local_qwen_tts

Run against Alibaba Cloud DashScope/Qwen TTS SSE API:

```sh
export DASHSCOPE_API_KEY="your-token"
go run ./examples/local_qwen_tts \
  -model qwen-tts \
  -voice Cherry \
  -language zh \
  -text "你好，今天天气怎么样呢？" \
  -out local_qwen_tts_16k_mono_s16le.pcm
```

Override the endpoint if needed:

```sh
go run ./examples/local_qwen_tts \
  -endpoint https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation
```

Play the generated PCM:

```sh
ffplay -f s16le -ar 16000 -ac 1 local_qwen_tts_16k_mono_s16le.pcm
```

## local_qwen_realtime_tts

Run against Alibaba Cloud DashScope/Qwen realtime WebSocket TTS API:

```sh
export DASHSCOPE_API_KEY="your-token"
go run ./examples/local_qwen_realtime_tts \
  -model qwen3-tts-instruct-flash-realtime \
  -voice Cherry \
  -language zh \
  -instructions "温暖自然的语气" \
  -text "你好，今天天气怎么样呢？" \
  -append-text "这是第二段追加文本，用来验证 append text 是否生效。" \
  -out local_qwen_realtime_tts.ogg
```

The example appends two text segments to the same realtime session by default.
The platform emits raw Opus packets to the application layer, and the example
wraps them back into an Ogg Opus file for playback:

```sh
ffplay local_qwen_realtime_tts.ogg
```

Qwen realtime Opus audio is treated as 48 kHz throughout the provider and platform pipeline.
