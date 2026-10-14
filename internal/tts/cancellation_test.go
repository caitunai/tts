package tts

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestSynthesizeCancellationClosesEventForwarder(t *testing.T) {
	for _, fillBuffer := range []bool{false, true} {
		name := "waiting for upstream"
		if fillBuffer {
			name = "blocked on output"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				upstream := make(chan *ProviderEvent, defaultEventBufferSize+1)
				if fillBuffer {
					for range cap(upstream) {
						upstream <- &ProviderEvent{Type: ProviderEventSegmentStart}
					}
				}
				provider := &fakeServiceProvider{name: "mock", synthEvents: upstream}
				service := NewService("test", fakeRegistry{providers: map[string]Provider{"mock": provider}})
				events, err := service.SynthesizeOnce(ctx, &SynthesizeRequest{Provider: "mock", Text: "hello"})
				if err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if fillBuffer && len(events) != defaultEventBufferSize {
					t.Fatal("output buffer did not fill")
				}
				cancel()
				synctest.Wait()
				// Upstream stays open and no output is consumed until cancellation
				// has completed; forwarding must not depend on either operation.
				for range len(events) {
					<-events
				}
				select {
				case _, ok := <-events:
					if ok {
						t.Fatal("unexpected event after cancellation")
					}
				default:
					t.Fatal("output channel did not close after cancellation")
				}
			})
		})
	}
}
