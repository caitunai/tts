package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caitunai/tts/internal/tts"
	"github.com/go-resty/resty/v2"
)

func Resolve(cfg tts.TimeoutConfig) (tts.TimeoutConfig, error) {
	defaults := tts.DefaultTimeoutConfig()
	for name, pair := range map[string][2]*time.Duration{
		"request": {&cfg.RequestTimeout, &defaults.RequestTimeout},
		"connect": {&cfg.ConnectTimeout, &defaults.ConnectTimeout},
		"open":    {&cfg.OpenTimeout, &defaults.OpenTimeout},
		"read":    {&cfg.ReadTimeout, &defaults.ReadTimeout},
		"write":   {&cfg.WriteTimeout, &defaults.WriteTimeout},
		"segment": {&cfg.SegmentTimeout, &defaults.SegmentTimeout},
		"finish":  {&cfg.FinishTimeout, &defaults.FinishTimeout},
	} {
		if *pair[0] < 0 {
			return cfg, fmt.Errorf("%s timeout must not be negative", name)
		}
		if *pair[0] == 0 {
			*pair[0] = *pair[1]
		}
	}
	return cfg, nil
}

// HTTPClient preserves supplied clients and their transports without mutation.
// RequestContext and GuardHTTPBody enforce limits for supplied clients too.
func HTTPClient(client *resty.Client, cfg tts.TimeoutConfig) *resty.Client {
	if client != nil {
		return client
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return resty.NewWithClient(&http.Client{Transport: http.DefaultTransport, Timeout: cfg.RequestTimeout})
	}
	transport := base.Clone()
	transport.DialContext = (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = cfg.ConnectTimeout
	transport.ResponseHeaderTimeout = cfg.OpenTimeout
	return resty.NewWithClient(&http.Client{Transport: transport, Timeout: cfg.RequestTimeout})
}

func RequestContext(ctx context.Context, cfg tts.TimeoutConfig) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, cfg.RequestTimeout)
}

func GuardHTTPBody(resp *resty.Response, cfg tts.TimeoutConfig) {
	if resp != nil && resp.RawBody() != nil {
		resp.RawResponse.Body = &idleBody{ReadCloser: resp.RawBody(), timeout: cfg.ReadTimeout}
	}
}

type idleBody struct {
	io.ReadCloser
	timeout  time.Duration
	closed   sync.Once
	closeErr error
}

func (b *idleBody) Close() error {
	b.closed.Do(func() { b.closeErr = b.ReadCloser.Close() })
	return b.closeErr
}

func (b *idleBody) Read(data []byte) (int, error) {
	var expired atomic.Bool
	timer := time.AfterFunc(b.timeout, func() { expired.Store(true); _ = b.Close() })
	n, err := b.ReadCloser.Read(data)
	timer.Stop()
	if expired.Load() {
		return n, context.DeadlineExceeded
	}
	return n, err
}
