package prompt

import (
	"context"
	"strings"
	testing "testing"

	"github.com/stello/elnath/internal/skill"
)

func TestSkillCatalogNodeRenderNilRegistry(t *testing.T) {
	t.Parallel()

	got, err := NewSkillCatalogNode(65, nil).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestSkillCatalogNodeRenderEmptyRegistry(t *testing.T) {
	t.Parallel()

	got, err := NewSkillCatalogNode(65, skill.NewRegistry()).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestSkillCatalogNodeRenderListsSkills(t *testing.T) {
	t.Parallel()

	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{Name: "pr-review", Trigger: "/pr-review <pr_number>", Description: "Review PR with security focus"})
	reg.Add(&skill.Skill{Name: "audit-security", Trigger: "/audit-security", Description: "Audit codebase"})

	got, err := NewSkillCatalogNode(65, reg).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	checks := []string{
		"Available skills (invoke via /name):",
		"/pr-review <pr_number> — Review PR with security focus",
		"/audit-security — Audit codebase",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("Render missing %q\n%s", want, got)
		}
	}
}

func TestSkillCatalogNodeRenderBenchmarkMode(t *testing.T) {
	t.Parallel()

	reg := skill.NewRegistry()
	reg.Add(&skill.Skill{Name: "pr-review", Description: "Review PR"})

	got, err := NewSkillCatalogNode(65, reg).Render(context.Background(), &RenderState{BenchmarkMode: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestSkillCatalogNodeName(t *testing.T) {
	t.Parallel()

	if got := NewSkillCatalogNode(65, nil).Name(); got != "skill_catalog" {
		t.Fatalf("Name = %q, want %q", got, "skill_catalog")
	}
}

func TestSkillCatalogNodePriority(t *testing.T) {
	t.Parallel()

	if got := NewSkillCatalogNode(65, nil).Priority(); got != 65 {
		t.Fatalf("Priority = %d, want 65", got)
	}
}
