package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/stello/elnath/internal/core"
)

type mockProvider struct{ name string }

func (m *mockProvider) Chat(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return nil, nil
}
func (m *mockProvider) Stream(_ context.Context, _ ChatRequest, _ func(StreamEvent)) error {
	return nil
}
func (m *mockProvider) Name() string      { return m.name }
func (m *mockProvider) Models() []ModelInfo { return nil }

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := &mockProvider{name: "anthropic"}
	r.Register("anthropic", p)

	got, err := r.Get("anthropic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name() != "anthropic" {
		t.Errorf("got name %q, want %q", got.Name(), "anthropic")
	}
}

func TestRegistryDefault(t *testing.T) {
	r := NewRegistry()
	r.Register("anthropic", &mockProvider{name: "anthropic"})
	r.Register("openai", &mockProvider{name: "openai"})

	def, err := r.Default()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Name() != "anthropic" {
		t.Errorf("got default %q, want %q", def.Name(), "anthropic")
	}
}

func TestRegistrySetDefault(t *testing.T) {
	r := NewRegistry()
	r.Register("anthropic", &mockProvider{name: "anthropic"})
	r.Register("openai", &mockProvider{name: "openai"})

	if err := r.SetDefault("openai"); err != nil {
		t.Fatalf("SetDefault error: %v", err)
	}

	def, err := r.Default()
	if err != nil {
		t.Fatalf("Default error: %v", err)
	}
	if def.Name() != "openai" {
		t.Errorf("got default %q, want %q", def.Name(), "openai")
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := NewRegistry()

	_, err := r.Get("missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("expected core.ErrNotFound in error chain, got %v", err)
	}
}

func TestResolveModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"opus", "claude-opus-4-6"},
		{"sonnet", "claude-sonnet-4-6"},
		{"haiku", "claude-haiku-4-5-20251213"},
		{"unknown", "unknown"},
		{"claude-opus-4-6", "claude-opus-4-6"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			got := ResolveModel(tc.input)
			if got != tc.want {
				t.Errorf("ResolveModel(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"claude-sonnet-4-6", "anthropic"},
		{"claude-opus-4-6", "anthropic"},
		{"gpt-4o", "openai"},
		{"grok-3", "xai"},
		{"o1-mini", "openai"},
		{"openai/gpt-4.1", "openai"},
		// Ollama-routed models
		{"llama3.2", "ollama"},
		{"llama3.1:70b", "ollama"},
		{"mistral-7b", "ollama"},
		{"codellama:13b", "ollama"},
		{"deepseek-coder:6.7b", "ollama"},
		{"qwen2:7b", "ollama"},
		{"gemma2:9b", "ollama"},
		{"phi3:mini", "ollama"},
		{"ollama/custom-model", "ollama"},
		{"unknown-model", ""},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.model, func(t *testing.T) {
			got := DetectProvider(tc.model)
			if got != tc.want {
				t.Errorf("DetectProvider(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

func TestRegistrySetDefaultNotFound(t *testing.T) {
	r := NewRegistry()
	err := r.SetDefault("nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("expected core.ErrNotFound, got %v", err)
	}
}

func TestRegistryDefaultEmpty(t *testing.T) {
	r := NewRegistry()
	_, err := r.Default()
	if err == nil {
		t.Fatal("expected error for empty registry, got nil")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Register("a", &mockProvider{name: "a"})
	r.Register("b", &mockProvider{name: "b"})

	names := r.List()
	if len(names) != 2 {
		t.Errorf("List() returned %d names, want 2", len(names))
	}
}

func TestOllamaProviderName(t *testing.T) {
	p := NewOllamaProvider("", "llama3.2")
	if p.Name() != "ollama" {
		t.Errorf("Name() = %q, want %q", p.Name(), "ollama")
	}
	models := p.Models()
	if len(models) != 1 || models[0].ID != "llama3.2" {
		t.Errorf("Models() = %+v, want [{ID:llama3.2}]", models)
	}
}

func TestForModel(t *testing.T) {
	r := NewRegistry()
	anthropic := &mockProvider{name: "anthropic"}
	openai := &mockProvider{name: "openai"}
	r.Register("anthropic", anthropic)
	r.Register("openai", openai)

	tests := []struct {
		model        string
		wantProvider string
		wantCanonical string
	}{
		{"claude-sonnet-4-6", "anthropic", "claude-sonnet-4-6"},
		{"sonnet", "anthropic", "claude-sonnet-4-6"},
		{"gpt-4o", "openai", "gpt-4o"},
		// unknown model falls back to default (anthropic, registered first)
		{"unknown-xyz", "anthropic", "unknown-xyz"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.model, func(t *testing.T) {
			p, canonical, err := r.ForModel(tc.model)
			if err != nil {
				t.Fatalf("ForModel(%q) error: %v", tc.model, err)
			}
			if p.Name() != tc.wantProvider {
				t.Errorf("provider = %q, want %q", p.Name(), tc.wantProvider)
			}
			if canonical != tc.wantCanonical {
				t.Errorf("canonical = %q, want %q", canonical, tc.wantCanonical)
			}
		})
	}
}
