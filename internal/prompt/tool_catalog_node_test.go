package prompt

import (
	"context"
	"strings"
	testing "testing"
)

func TestToolCatalogNodeRendersToolNames(t *testing.T) {
	t.Parallel()

	got, err := NewToolCatalogNode(80).Render(context.Background(), &RenderState{ToolNames: []string{"bash", "read_file", "git"}})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{"You have access to tools", "bash", "read_file", "git"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
}

func TestToolCatalogNodeHandlesEmptyToolList(t *testing.T) {
	t.Parallel()

	got, err := NewToolCatalogNode(80).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "You have access to tools") {
		t.Fatalf("Render = %q, want generic tools guidance", got)
	}
	if strings.Contains(got, "Available tools:") {
		t.Fatalf("Render = %q, did not want tool list header", got)
	}
}
