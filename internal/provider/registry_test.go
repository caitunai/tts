package provider

import (
	"context"
	"testing"

	"github.com/caitunai/tts/internal/tts"
)

type fakeProvider struct {
	name string
	caps *tts.ProviderCapabilities
}

func (p fakeProvider) Name() string {
	return p.name
}

func (p fakeProvider) Capabilities(context.Context) (*tts.ProviderCapabilities, error) {
	return p.caps, nil
}

func (p fakeProvider) SynthesizeOnce(context.Context, *tts.ProviderSynthesizeRequest) (<-chan *tts.ProviderEvent, error) {
	events := make(chan *tts.ProviderEvent)
	close(events)
	return events, nil
}

func (p fakeProvider) OpenSession(context.Context, *tts.ProviderOpenSessionRequest) (tts.ProviderSession, error) {
	return nil, nil
}

func TestRegistryRegisterGetAndList(t *testing.T) {
	registry := NewRegistry()

	if err := registry.Register(fakeProvider{name: "zeta"}); err != nil {
		t.Fatalf("register zeta: %v", err)
	}
	if err := registry.Register(fakeProvider{name: "alpha"}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}

	got, ok := registry.Get("alpha")
	if !ok {
		t.Fatal("expected alpha provider")
	}
	if got.Name() != "alpha" {
		t.Fatalf("provider name = %q, want alpha", got.Name())
	}

	list := registry.List()
	if len(list) != 2 {
		t.Fatalf("list length = %d, want 2", len(list))
	}
	if list[0].Name() != "alpha" || list[1].Name() != "zeta" {
		t.Fatalf("list order = [%s %s], want [alpha zeta]", list[0].Name(), list[1].Name())
	}
}

func TestRegistryRejectsInvalidProviders(t *testing.T) {
	registry := NewRegistry()

	if err := registry.Register(nil); err == nil {
		t.Fatal("expected nil provider error")
	}
	if err := registry.Register(fakeProvider{}); err == nil {
		t.Fatal("expected empty provider name error")
	}
	if err := registry.Register(fakeProvider{name: "dup"}); err != nil {
		t.Fatalf("register dup: %v", err)
	}
	if err := registry.Register(fakeProvider{name: "dup"}); err == nil {
		t.Fatal("expected duplicate provider error")
	}
}

func TestRegistryZeroValueCanRegister(t *testing.T) {
	var registry Registry

	if err := registry.Register(fakeProvider{name: "zero"}); err != nil {
		t.Fatalf("register on zero registry: %v", err)
	}
	if _, ok := registry.Get("zero"); !ok {
		t.Fatal("expected provider registered on zero registry")
	}
}

func TestRegistryCapabilities(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(fakeProvider{
		name: "alpha",
		caps: &tts.ProviderCapabilities{Name: "alpha"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(fakeProvider{name: "beta"}); err != nil {
		t.Fatalf("register: %v", err)
	}

	caps, err := registry.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(caps) != 2 {
		t.Fatalf("capabilities length = %d, want 2", len(caps))
	}
	if caps[0].Name != "alpha" {
		t.Fatalf("caps[0].Name = %q, want alpha", caps[0].Name)
	}
	if caps[1].Name != "beta" {
		t.Fatalf("caps[1].Name = %q, want beta", caps[1].Name)
	}
}
