package qwentts

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"testing/synctest"

	"github.com/caitunai/tts/internal/audio"
	registryprovider "github.com/caitunai/tts/internal/provider"
	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

func TestServiceCancellationReleasesQwenStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pcm := bytes.Repeat([]byte{1, 0}, 480)
		wav := makeTestWAV(pcm, 24000, 1, false)
		encodeEvent := func(data []byte) string {
			return "data: {\"output\":{\"audio\":{\"data\":\"" + base64.StdEncoding.EncodeToString(data) + "\"}}}\n\n"
		}
		body := &trackedResponseBody{
			Reader: strings.NewReader(encodeEvent(wav) + strings.Repeat(encodeEvent(pcm), 64)),
			closed: make(chan struct{}),
		}
		client := resty.NewWithClient(&http.Client{Transport: qwenRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
		})})
		provider, err := NewProvider(Config{Endpoint: "https://example.test/tts", Client: client})
		if err != nil {
			t.Fatal(err)
		}
		registry := registryprovider.NewRegistry()
		if err := registry.Register(provider); err != nil {
			t.Fatal(err)
		}
		service := tts.NewService("test", registry)
		events, err := service.SynthesizeOnce(ctx, &tts.SynthesizeRequest{
			Provider: provider.Name(),
			Text:     "hello",
			Output: audio.OutputConfig{
				PreferCodec: audio.CodecPCM,
				SampleRate:  24000,
				Channels:    1,
				FrameMS:     20,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if len(events) != cap(events) {
			t.Fatal("application event buffer did not fill")
		}
		select {
		case <-body.closed:
			t.Fatal("HTTP stream closed before cancellation")
		default:
		}
		cancel()
		synctest.Wait()
		select {
		case <-body.closed:
		default:
			t.Fatal("cancelling the service did not release the Qwen HTTP stream")
		}
		for range len(events) {
			<-events
		}
		select {
		case _, ok := <-events:
			if ok {
				t.Fatal("unexpected event after cancellation")
			}
		default:
			t.Fatal("application event channel did not close")
		}
	})
}

func TestStreamCancellationReleasesBlockedEventSends(t *testing.T) {
	const pcmEvent = "data: {\"output\":{\"audio\":{\"data\":\"AQACAA==\"}}}\n\n"
	tests := []struct {
		name       string
		prefill    int
		status     int
		body       string
		readErr    bool
		requestErr bool
	}{
		{name: "audio", status: http.StatusOK, body: strings.Repeat(pcmEvent, 32)},
		{name: "segment start", prefill: 16, status: http.StatusOK},
		{name: "segment end", prefill: 15, status: http.StatusOK},
		{name: "audio decode error", prefill: 15, status: http.StatusOK, body: "data: {\"output\":{\"audio\":{\"data\":\"invalid-base64\"}}}\n\n"},
		{name: "truncated WAV error", prefill: 15, status: http.StatusOK, body: "data: {\"output\":{\"audio\":{\"data\":\"UklGRg==\"}}}\n\n"},
		{name: "HTTP status error", prefill: 16, status: http.StatusUnauthorized},
		{name: "response read error", prefill: 15, status: http.StatusOK, readErr: true},
		{name: "request error", prefill: 16, requestErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var reader io.Reader = strings.NewReader(tt.body)
				if tt.readErr {
					reader = iotest.ErrReader(errors.New("test read failure"))
				}
				body := &trackedResponseBody{Reader: reader, closed: make(chan struct{})}
				client := resty.NewWithClient(&http.Client{Transport: qwenRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					if tt.requestErr {
						return nil, errors.New("test request failure")
					}
					return &http.Response{
						StatusCode: tt.status,
						Header:     make(http.Header),
						Body:       body,
						Request:    req,
					}, nil
				})})
				provider, err := NewProvider(Config{Endpoint: "https://example.test/tts", Client: client})
				if err != nil {
					t.Fatal(err)
				}
				events := make(chan *tts.ProviderEvent, 16)
				for range tt.prefill {
					events <- &tts.ProviderEvent{Type: tts.ProviderEventAudio}
				}
				done := make(chan struct{})
				go func() {
					defer close(done)
					provider.stream(ctx, &tts.ProviderSynthesizeRequest{Text: "hello"}, events)
				}()
				synctest.Wait()
				if len(events) != cap(events) {
					t.Fatal("event buffer did not fill")
				}
				select {
				case <-done:
					t.Fatal("stream exited before cancellation")
				default:
				}

				cancel()
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Fatal("stream did not exit after cancellation without consuming events")
				}
				if !tt.requestErr {
					select {
					case <-body.closed:
					default:
						t.Fatal("HTTP response body was not closed")
					}
				}
				for range cap(events) {
					<-events
				}
				if _, ok := <-events; ok {
					t.Fatal("event channel did not close")
				}
			})
		})
	}
}

type qwenRoundTripFunc func(*http.Request) (*http.Response, error)

func (f qwenRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackedResponseBody struct {
	io.Reader
	closed    chan struct{}
	closeOnce sync.Once
}

func (b *trackedResponseBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}
