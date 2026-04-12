package prompt

import (
	"context"
	testing "testing"
)

func TestPersonaNodeRendersExtra(t *testing.T) {
	t.Parallel()

	got, err := NewPersonaNode(90).Render(context.Background(), &RenderState{PersonaExtra: "Focus on research: generate hypotheses."})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "Focus on research: generate hypotheses." {
		t.Fatalf("Render = %q", got)
	}
}

func TestPersonaNodeEmptyExtra(t *testing.T) {
	t.Parallel()

	got, err := NewPersonaNode(90).Render(context.Background(), &RenderState{PersonaExtra: "   "})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}
