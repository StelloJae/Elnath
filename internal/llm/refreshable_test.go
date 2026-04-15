package llm

import (
	"context"
	"testing"
)

type mockRefreshableProvider struct {
	name         string
	refreshCalls int
	refreshErr   error
}

func (m *mockRefreshableProvider) Chat(_ context.Context, _ ChatRequest) (*ChatResponse, error) {
	return nil, nil
}

func (m *mockRefreshableProvider) Stream(_ context.Context, _ ChatRequest, _ func(StreamEvent)) error {
	return nil
}

func (m *mockRefreshableProvider) Name() string { return m.name }

func (m *mockRefreshableProvider) Models() []ModelInfo { return nil }

func (m *mockRefreshableProvider) Refresh(_ context.Context) error {
	m.refreshCalls++
	return m.refreshErr
}

func TestRefreshIfSupported_NonRefreshable(t *testing.T) {
	p := &mockProvider{name: "plain"}
	if err := RefreshIfSupported(context.Background(), p); err != nil {
		t.Fatalf("RefreshIfSupported(non-refreshable) = %v, want nil", err)
	}
}

func TestRefreshIfSupported_Refreshable(t *testing.T) {
	p := &mockRefreshableProvider{name: "codex"}
	if err := RefreshIfSupported(context.Background(), p); err != nil {
		t.Fatalf("RefreshIfSupported(refreshable) = %v, want nil", err)
	}
	if p.refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", p.refreshCalls)
	}
}

func TestCodexOAuthProvider_IsRefreshable(t *testing.T) {
	var p Provider = NewCodexOAuthProvider("o4-mini")
	if _, ok := p.(RefreshableProvider); !ok {
		t.Fatal("CodexOAuthProvider does not implement RefreshableProvider")
	}
}

func TestAnthropicProvider_NotRefreshable(t *testing.T) {
	var p Provider = NewAnthropicProvider("key", "claude-sonnet-4-6")
	if _, ok := p.(RefreshableProvider); ok {
		t.Fatal("AnthropicProvider unexpectedly implements RefreshableProvider")
	}
}
