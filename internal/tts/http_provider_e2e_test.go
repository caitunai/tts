package tts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/provider/vllmtts"
	"github.com/caitunai/tts/internal/tts"
)

func TestHTTPProviderEndToEnd(t *testing.T) {
	var gotRequest struct {
		Input          string `json:"input"`
		Voice          string `json:"voice"`
		Stream         bool   `json:"stream"`
		ResponseFormat string `json:"response_format"`
		Language       string `json:"language"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer local-token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(bytes.Repeat([]byte{1}, 960))
	}))
	defer server.Close()

	httpProvider, err := vllmtts.NewProvider(vllmtts.Config{
		Name:            "local_http",
		Endpoint:        server.URL,
		Token:           "local-token",
		DefaultVoice:    "serena",
		DefaultLanguage: "Chinese",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	registry := registryprovider.NewRegistry()
	if err := registry.Register(httpProvider); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	service := tts.NewService("test", registry)
	events, err := service.SynthesizeOnce(context.Background(), &tts.SynthesizeRequest{
		RequestID: "req_local",
		Provider:  "local_http",
		Text:      "你好，今天天气怎么样呢？",
		Voice:     "serena",
		Language:  "Chinese",
		Output:    audio.DefaultOutputConfig(),
	})
	if err != nil {
		t.Fatalf("SynthesizeOnce: %v", err)
	}

	got := collectEvents(t, events)
	requireEventSequence(t, got, []tts.EventType{
		tts.EventSegmentStart,
		tts.EventAudioFrame,
		tts.EventSegmentEnd,
	})
	if got[1].Audio == nil {
		t.Fatal("audio frame is nil")
	}
	if got[1].Audio.Codec != audio.CodecPCM {
		t.Fatalf("codec = %q, want pcm", got[1].Audio.Codec)
	}
	if len(got[1].Audio.Data) != 640 {
		t.Fatalf("PCM frame length = %d, want 640", len(got[1].Audio.Data))
	}

	if gotRequest.Input != "你好，今天天气怎么样呢？" {
		t.Fatalf("input = %q", gotRequest.Input)
	}
	if gotRequest.Voice != "serena" {
		t.Fatalf("voice = %q, want serena", gotRequest.Voice)
	}
	if !gotRequest.Stream {
		t.Fatal("stream = false, want true")
	}
	if gotRequest.ResponseFormat != "pcm" {
		t.Fatalf("response_format = %q, want pcm", gotRequest.ResponseFormat)
	}
	if gotRequest.Language != "Chinese" {
		t.Fatalf("language = %q, want Chinese", gotRequest.Language)
	}
}
