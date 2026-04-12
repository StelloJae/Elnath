package prompt

import (
	"context"
	testing "testing"
)

func TestDynamicBoundaryNodeRender(t *testing.T) {
	t.Parallel()

	got, err := NewDynamicBoundaryNode().Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != dynamicBoundary {
		t.Fatalf("Render = %q, want %q", got, dynamicBoundary)
	}
}

func TestDynamicBoundaryNodePriority(t *testing.T) {
	t.Parallel()

	if got := NewDynamicBoundaryNode().Priority(); got != 999 {
		t.Fatalf("Priority = %d, want 999", got)
	}
}
