package prompt

import (
	"context"
	"strings"
	testing "testing"
)

func TestGreenfieldNodeSkipsWhenExistingCode(t *testing.T) {
	t.Parallel()

	got, err := NewGreenfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestGreenfieldNodeSkipsWhenNilState(t *testing.T) {
	t.Parallel()

	got, err := NewGreenfieldNode(40).Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestGreenfieldNodeSkipsInBenchmarkMode(t *testing.T) {
	t.Parallel()

	got, err := NewGreenfieldNode(40).Render(context.Background(), &RenderState{BenchmarkMode: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestGreenfieldNodeRendersForNewProject(t *testing.T) {
	t.Parallel()

	got, err := NewGreenfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: false})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{"# New Project Guidance", "Write a failing test first", "simplest architecture"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
}

func TestGreenfieldNodeGoSpecific(t *testing.T) {
	t.Parallel()

	got, err := NewGreenfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: false, TaskLanguage: "go"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "go mod init") {
		t.Fatalf("Render = %q, want go-specific guidance", got)
	}
}

func TestGreenfieldNodeTypeScriptSpecific(t *testing.T) {
	t.Parallel()

	got, err := NewGreenfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: false, TaskLanguage: "typescript"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "strict: true") {
		t.Fatalf("Render = %q, want TypeScript-specific guidance", got)
	}
}

func TestGreenfieldNodePythonSpecific(t *testing.T) {
	t.Parallel()

	got, err := NewGreenfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: false, TaskLanguage: "python"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "pyproject.toml") {
		t.Fatalf("Render = %q, want Python-specific guidance", got)
	}
}
