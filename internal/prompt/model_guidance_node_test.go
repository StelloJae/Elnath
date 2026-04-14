package prompt

import (
	"context"
	"strings"
	testing "testing"
)

func TestModelGuidanceNodeAnthropic(t *testing.T) {
	t.Parallel()

	got, err := NewModelGuidanceNode(70).Render(context.Background(), &RenderState{Provider: "anthropic", Model: "claude-sonnet-4-6"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(strings.ToLower(got), "xml") {
		t.Fatalf("Render = %q, want XML guidance", got)
	}
}

func TestModelGuidanceNodeOllama(t *testing.T) {
	t.Parallel()

	got, err := NewModelGuidanceNode(70).Render(context.Background(), &RenderState{Provider: "ollama", Model: "llama3.2"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(strings.ToLower(got), "concise") {
		t.Fatalf("Render = %q, want concise guidance", got)
	}
}

func TestModelGuidanceNodeUnknownProvider(t *testing.T) {
	t.Parallel()

	got, err := NewModelGuidanceNode(70).Render(context.Background(), &RenderState{Provider: "unknown", Model: "mystery"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}
