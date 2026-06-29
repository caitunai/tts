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
