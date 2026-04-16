package prompt

import (
	"context"
	"strings"
	testing "testing"
)

func TestSkillGuidanceNodeRender(t *testing.T) {
	t.Parallel()

	node := NewSkillGuidanceNode(64)
	got, err := node.Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got == "" {
		t.Fatal("Render() = empty string, want guidance")
	}
	checks := []string{
		"You have a create_skill tool.",
		"You notice a repeated pattern across sessions",
		"The user says \"make this a skill\" or similar",
		"Do not suggest skills for one-time tasks.",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("Render() missing %q\n%s", want, got)
		}
	}
	if got := node.Priority(); got != 64 {
		t.Fatalf("Priority() = %d, want 64", got)
	}
	if got := node.Name(); got != "skill_guidance" {
		t.Fatalf("Name() = %q, want %q", got, "skill_guidance")
	}
}

func TestSkillGuidanceNodeBenchmarkMode(t *testing.T) {
	t.Parallel()

	node := NewSkillGuidanceNode(64)
	got, err := node.Render(context.Background(), &RenderState{BenchmarkMode: true})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if got != "" {
		t.Fatalf("Render() = %q, want empty string", got)
	}
}
