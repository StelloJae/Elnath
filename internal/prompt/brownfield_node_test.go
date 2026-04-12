package prompt

import (
	"context"
	"strings"
	testing "testing"
)

func TestBrownfieldNodeSkipsWhenNotExistingCode(t *testing.T) {
	t.Parallel()

	got, err := NewBrownfieldNode(40).Render(context.Background(), &RenderState{VerifyHint: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestBrownfieldNodeRendersGuidance(t *testing.T) {
	t.Parallel()

	got, err := NewBrownfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{"# Execution Discipline", "Make the smallest correct change.", "Run the repo test suite before finishing."} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
}

func TestBrownfieldNodeIncludesVerificationHint(t *testing.T) {
	t.Parallel()

	got, err := NewBrownfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: true, VerifyHint: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "## Verification (ant P2)") {
		t.Fatalf("Render = %q, want verification section", got)
	}
}

func TestBrownfieldNodeContainsVerificationDiscipline(t *testing.T) {
	t.Parallel()

	got, err := NewBrownfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{"Report outcomes faithfully", "Do not hedge confirmed results"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
}

func TestBrownfieldNodeGoGuidance(t *testing.T) {
	t.Parallel()

	got, err := NewBrownfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: true, TaskLanguage: "go"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "go test") {
		t.Fatalf("Render = %q, want go-specific guidance", got)
	}
}

func TestBrownfieldNodeTSGuidance(t *testing.T) {
	t.Parallel()

	got, err := NewBrownfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: true, TaskLanguage: "typescript"})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "npm test") {
		t.Fatalf("Render = %q, want TypeScript-specific guidance", got)
	}
}
