package tts

import (
	"context"
	"errors"
	"net"
	"time"
)

// TimeoutConfig bounds network operations even when callers use Background.
// Zero values select defaults; negative values are invalid.
type TimeoutConfig struct {
	RequestTimeout time.Duration // HTTP request including its response body.
	ConnectTimeout time.Duration // TCP/TLS/WebSocket handshake per attempt.
	OpenTimeout    time.Duration // WebSocket connect attempts and protocol setup.
	ReadTimeout    time.Duration // Inactivity while reading HTTP/WS data.
	WriteTimeout   time.Duration // WebSocket network writes.
	SegmentTimeout time.Duration // Sent text to segment_end, including streaming.
	FinishTimeout  time.Duration // Last segment_end to session_end after Finish.
}

func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		RequestTimeout: 60 * time.Second, ConnectTimeout: 10 * time.Second,
		OpenTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 10 * time.Second, SegmentTimeout: 120 * time.Second,
		FinishTimeout: 15 * time.Second,
	}
}

// ReconnectConfig retries only the initial WebSocket handshake. It never
// resends synthesis text. MaxAttempts=1 disables retries; zero selects 3.
type ReconnectConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// NormalizeError preserves provider information while recognizing wrapped
// context and network deadlines. The returned error does not mutate its input.
func NormalizeError(err *Error) *Error {
	if err == nil {
		return nil
	}
	copy := *err
	if network, ok := errors.AsType[net.Error](err); errors.Is(err, context.DeadlineExceeded) || ok && network.Timeout() {
		copy.Code = ErrProviderTimeout
		copy.Retryable = true
	}
	return &copy
}
