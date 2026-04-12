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
	for _, want := range []string{"Brownfield execution guidance:", "Inspect existing files, tests, and nearby patterns before editing.", "Keep scope bounded to the smallest correct change."} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "prioritize proving the change") {
		t.Fatalf("Render = %q, did not want verification hint", got)
	}
}

func TestBrownfieldNodeIncludesVerificationHint(t *testing.T) {
	t.Parallel()

	got, err := NewBrownfieldNode(40).Render(context.Background(), &RenderState{ExistingCode: true, VerifyHint: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "prioritize proving the change with tests or repo-native checks") {
		t.Fatalf("Render = %q, want verification hint", got)
	}
}
