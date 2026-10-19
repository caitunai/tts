package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/caitunai/tts/internal/tts"
	"github.com/gorilla/websocket"
)

func ResolveReconnect(cfg tts.ReconnectConfig) (tts.ReconnectConfig, error) {
	if cfg.MaxAttempts < 0 || cfg.InitialBackoff < 0 || cfg.MaxBackoff < 0 {
		return cfg, fmt.Errorf("websocket reconnect settings must not be negative")
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialBackoff == 0 {
		cfg.InitialBackoff = 200 * time.Millisecond
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 2 * time.Second
	}
	if cfg.InitialBackoff > cfg.MaxBackoff {
		return cfg, fmt.Errorf("initial reconnect backoff exceeds maximum")
	}
	return cfg, nil
}

// Dial retries only TCP/TLS/WebSocket establishment. No provider text is sent
// here. Protocol initialization errors after a successful handshake are terminal.
func Dial(ctx context.Context, original *websocket.Dialer, endpoint string, headers http.Header, cfg tts.TimeoutConfig, retry tts.ReconnectConfig) (*websocket.Conn, *http.Response, error) {
	if original == nil {
		original = websocket.DefaultDialer
	}
	dialer := *original
	if dialer.HandshakeTimeout <= 0 || dialer.HandshakeTimeout > cfg.ConnectTimeout {
		dialer.HandshakeTimeout = cfg.ConnectTimeout
	}
	wrap := func(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
		return func(dialCtx context.Context, network, address string) (net.Conn, error) {
			conn, err := dial(dialCtx, network, address)
			if err != nil {
				return nil, err
			}
			bounded := &deadlineConn{Conn: conn, readTimeout: cfg.ReadTimeout, writeTimeout: cfg.WriteTimeout}
			bounded.watch(ctx)
			return bounded, nil
		}
	}
	netDial := dialer.NetDialContext
	if netDial == nil && dialer.NetDial != nil {
		legacy := dialer.NetDial
		netDial = func(_ context.Context, network, address string) (net.Conn, error) { return legacy(network, address) }
	}
	if netDial == nil {
		netDial = (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
	}
	dialer.NetDialContext = wrap(netDial)
	if dialer.NetDialTLSContext != nil {
		dialer.NetDialTLSContext = wrap(dialer.NetDialTLSContext)
	}
	openCtx, cancel := context.WithTimeout(ctx, cfg.OpenTimeout)
	defer cancel()
	deadline, _ := openCtx.Deadline()
	backoff := retry.InitialBackoff
	for attempt := 1; ; attempt++ {
		conn, response, err := dialer.DialContext(openCtx, endpoint, headers)
		if err == nil {
			// Covers provider-specific startup reads until GuardSession takes
			// ownership and clears this absolute initialization deadline.
			_ = conn.NetConn().SetReadDeadline(deadline)
			return conn, response, nil
		}
		if response != nil && response.Body != nil {
			response.Body = &idleBody{ReadCloser: response.Body, timeout: cfg.ReadTimeout}
		}
		if attempt >= retry.MaxAttempts || !retryHandshake(err, response) || openCtx.Err() != nil {
			return nil, response, err
		}
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		delay := time.Duration(float64(backoff) * (0.8 + rand.Float64()*0.2))
		timer := time.NewTimer(delay)
		select {
		case <-openCtx.Done():
			timer.Stop()
			return nil, nil, openCtx.Err()
		case <-timer.C:
		}
		if backoff >= retry.MaxBackoff/2 {
			backoff = retry.MaxBackoff
		} else {
			backoff *= 2
		}
	}
}

func retryHandshake(err error, response *http.Response) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if response != nil {
		return response.StatusCode == 429 || response.StatusCode >= 500
	}
	if dns, ok := errors.AsType[*net.DNSError](err); ok && dns.IsNotFound {
		return false
	}
	_, network := errors.AsType[net.Error](err)
	return network || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func OpenError(err error, response *http.Response, provider string) *tts.Error {
	failure := &tts.Error{Code: tts.ErrProviderUnavailable, Provider: provider,
		Message: err.Error(), Cause: err, Retryable: retryHandshake(err, response)}
	if response != nil {
		switch response.StatusCode {
		case 401, 403:
			failure.Code = tts.ErrProviderAuthFailed
		case 429:
			failure.Code = tts.ErrProviderRateLimited
		}
		if response.Body != nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if len(body) > 0 {
				failure.Message += ": " + string(body)
			}
		}
	}
	failure = tts.NormalizeError(failure)
	failure.Reconnectable = failure.Retryable && !errors.Is(err, context.Canceled)
	return failure
}

func StartError(ctx context.Context, err error, provider, sessionID string) *tts.Error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	failure, ok := errors.AsType[*tts.Error](err)
	if !ok {
		failure = &tts.Error{Code: tts.ErrProviderUnavailable, Message: err.Error(), Cause: err, Retryable: true}
	}
	failure = tts.NormalizeError(failure)
	failure.Provider, failure.SessionID = provider, sessionID
	failure.Reconnectable = canReconnect(failure) && !errors.Is(err, context.Canceled)
	return failure
}

// A network-level wrapper covers JSON, binary and provider protocol helpers
// alike, including callers that do not set a gorilla write deadline.
type deadlineConn struct {
	net.Conn
	readTimeout, writeTimeout   time.Duration
	mu                          sync.Mutex
	readDeadline, writeDeadline time.Time
	stopContext                 func() bool
	closed                      bool
}

func (c *deadlineConn) watch(ctx context.Context) {
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		stop()
		return
	}
	c.stopContext = stop
	c.mu.Unlock()
}

func (c *deadlineConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	stop := c.stopContext
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
	return c.Conn.Close()
}

func earlier(deadline time.Time, limit time.Duration) time.Time {
	bounded := time.Now().Add(limit)
	if !deadline.IsZero() && deadline.Before(bounded) {
		return deadline
	}
	return bounded
}

func (c *deadlineConn) Read(data []byte) (int, error) {
	c.mu.Lock()
	err := c.Conn.SetReadDeadline(earlier(c.readDeadline, c.readTimeout))
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return c.Conn.Read(data)
}

func (c *deadlineConn) Write(data []byte) (int, error) {
	c.mu.Lock()
	err := c.Conn.SetWriteDeadline(earlier(c.writeDeadline, c.writeTimeout))
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return c.Conn.Write(data)
}

func (c *deadlineConn) SetReadDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = deadline
	return c.Conn.SetReadDeadline(earlier(deadline, c.readTimeout))
}

func (c *deadlineConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = deadline
	return c.Conn.SetWriteDeadline(earlier(deadline, c.writeTimeout))
}

func (c *deadlineConn) SetDeadline(deadline time.Time) error {
	return errors.Join(c.SetReadDeadline(deadline), c.SetWriteDeadline(deadline))
}
