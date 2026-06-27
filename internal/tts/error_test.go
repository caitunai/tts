package tts

import (
	"errors"
	"testing"
)

func TestErrorStringFallsBackToCode(t *testing.T) {
	err := &Error{Code: ErrUnsupportedProvider}

	if got := err.Error(); got != string(ErrUnsupportedProvider) {
		t.Fatalf("Error() = %q, want %q", got, ErrUnsupportedProvider)
	}
}

func TestErrorStringUsesMessage(t *testing.T) {
	err := &Error{Code: ErrInternal, Message: "provider exploded"}

	if got := err.Error(); got != "provider exploded" {
		t.Fatalf("Error() = %q, want message", got)
	}
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("network timeout")
	err := &Error{Code: ErrProviderTimeout, Cause: cause}

	if !errors.Is(err, cause) {
		t.Fatal("errors.Is did not match wrapped cause")
	}
}
