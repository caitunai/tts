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

## local_minimax_tts

Run against Minimax realtime WebSocket TTS API:

```sh
export MINIMAX_TTS_TOKEN="your-token"
export MINIMAX_TTS_ENDPOINT="wss://your-minimax-tts-websocket-endpoint"
go run ./examples/local_minimax_tts \
  -model speech-2.8-hd \
  -voice male-qn-qingse \
  -language zh \
  -emotion happy \
  -text "你好，今天天气怎么样呢？" \
  -append-text "这是第二段追加文本，用来验证 Minimax append text 是否生效。" \
  -out local_minimax_tts_16k_mono_s16le.pcm
```

The Minimax provider receives MP3 chunks from the upstream service, while the
platform decodes them into 16 kHz / mono / 20 ms PCM frames for the application
layer:

```sh
ffplay -f s16le -ar 16000 -ac 1 local_minimax_tts_16k_mono_s16le.pcm
```

## local_microsoft_tts

Run against Microsoft Azure Speech TTS HTTP API:

```sh
export MICROSOFT_TTS_KEY="your-subscription-key"
export MICROSOFT_TTS_ENDPOINT="https://eastus.tts.speech.microsoft.com/cognitiveservices/v1"
go run ./examples/local_microsoft_tts \
  -voice zh-CN-XiaoxiaoNeural \
  -language zh-CN \
  -text "你好，今天天气怎么样呢？" \
  -out local_microsoft_tts.ogg
```

Microsoft returns Ogg-wrapped Opus at 48 kHz. The platform demuxes it into raw
Opus packets for the application layer, and this example wraps those packets
back into an Ogg Opus file for playback:

```sh
ffplay local_microsoft_tts.ogg
```

## local_deepgram_tts

Run against Deepgram HTTP TTS API:

```sh
export DEEPGRAM_API_KEY="your-api-key"
go run ./examples/local_deepgram_tts \
  -model aura-asteria-en \
  -text "Hello, welcome to Deepgram text to speech." \
  -out local_deepgram_tts.ogg
```

Deepgram returns Ogg-wrapped Opus at 48 kHz. The platform demuxes it into raw
Opus packets for the application layer, and this example wraps those packets
back into an Ogg Opus file for playback:

```sh
ffplay local_deepgram_tts.ogg
```

## local_cartesia_tts

Run against Cartesia WebSocket TTS API:

```sh
export CARTESIA_API_KEY="your-api-key"
export CARTESIA_TTS_VOICE="your-voice-id"
go run ./examples/local_cartesia_tts \
  -model sonic-3.5 \
  -language en \
  -text "Hello, this is Cartesia text to speech." \
  -append-text "This is a second segment in the same websocket session." \
  -out local_cartesia_tts_16k_mono_s16le.pcm
```

Cartesia returns base64 raw PCM over WebSocket. The platform emits
16 kHz / mono / 20 ms PCM frames by default:

```sh
ffplay -f s16le -ar 16000 -ac 1 local_cartesia_tts_16k_mono_s16le.pcm
```

## local_elevenlabs_tts

Run against ElevenLabs realtime WebSocket TTS API:

```sh
export ELEVENLABS_API_KEY="your-api-key"
go run ./examples/local_elevenlabs_tts \
  -voice 21m00Tcm4TlvDq8ikWAM \
  -model eleven_multilingual_v2 \
  -text "Hello, how is the weather today?" \
  -out local_elevenlabs_tts.ogg
```

ElevenLabs realtime returns base64 Ogg-wrapped Opus at 48 kHz. The platform
demuxes it into raw Opus packets for the application layer, and this example
wraps those packets back into an Ogg Opus file for playback:

```sh
ffplay local_elevenlabs_tts.ogg
```

## local_doubao_tts

Run against Doubao bidirectional WebSocket TTS API:

```sh
export DOUBAO_TTS_API_KEY="your-api-key"
export DOUBAO_TTS_VOICE="your-speaker-id"
go run ./examples/local_doubao_tts \
  -resource-id seed-tts-2.0 \
  -language zh \
  -guidance "温暖自然地说话" \
  -text "你好，今天天气怎么样呢？" \
  -append-text "这是第二段追加文本，用来验证豆包 append text 是否生效。" \
  -out local_doubao_tts.ogg
```

Doubao returns Ogg-wrapped Opus. The platform treats Opus as 48 kHz, demuxes it
into raw Opus packets for the application layer, and this example wraps those
packets back into an Ogg Opus file for playback:

```sh
ffplay local_doubao_tts.ogg
```

## local_inworld_tts

Run against Inworld AI bidirectional WebSocket TTS API:

```sh
export INWORLD_API_KEY="your-api-key"
export INWORLD_TTS_VOICE="your-voice-id"
go run ./examples/local_inworld_tts \
  -model inworld-tts-2 \
  -language en-US \
  -text "Hello, how is the weather today?" \
  -append-text "This is the second text segment, used to verify append text." \
  -out local_inworld_tts.ogg
```

Inworld returns Ogg-wrapped Opus. The provider exposes raw Opus packets at
48 kHz to the application layer, and this example wraps those packets back into
an Ogg Opus file for playback:

```sh
ffplay local_inworld_tts.ogg
```

## local_fishaudio_tts

Run against Fish Audio WebSocket TTS API:

```sh
export FISHAUDIO_API_KEY="your-api-key"
export FISHAUDIO_TTS_VOICE="your-reference-id"
go run ./examples/local_fishaudio_tts \
  -model s1 \
  -language en \
  -text "Hello, this is Fish Audio text to speech." \
  -append-text "This is the second segment, used to verify append text." \
  -out local_fishaudio_tts.ogg
```

Fish Audio uses MessagePack over WebSocket and returns Opus chunks that form a
continuous Ogg Opus stream. The platform demuxes those chunks into raw Opus
packets at 48 kHz for the application layer, and this example wraps the packets
back into an Ogg Opus file for playback:

```sh
ffplay local_fishaudio_tts.ogg
```

## local_openai_tts

Run against OpenAI Speech API:

```sh
export OPENAI_API_KEY="your-api-key"
go run ./examples/local_openai_tts \
  -model gpt-4o-mini-tts \
  -voice coral \
  -instructions "Speak in a cheerful and positive tone." \
  -text "Hello, this is OpenAI text to speech." \
  -out local_openai_tts.ogg
```

The OpenAI provider requests `response_format=opus` and `stream_format=audio`.
The platform demuxes the returned Ogg Opus stream into raw Opus packets at
48 kHz, and this example wraps those packets back into an Ogg Opus file:

```sh
ffplay local_openai_tts.ogg
```
