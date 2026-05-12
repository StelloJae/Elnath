package skill

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRegistryConditionalMatchesForPaths(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Add(&Skill{Name: "go-review", Paths: []string{"internal/**/*.go"}, Source: "claude-skill"})
	reg.Add(&Skill{Name: "docs-review", Paths: []string{"docs"}, Source: "codex-plugin-skill"})
	reg.Add(&Skill{Name: "always-on"})

	root := t.TempDir()
	matches := reg.ConditionalMatchesForPaths([]string{
		filepath.Join(root, "internal", "skill", "skill.go"),
		filepath.Join(root, "docs", "roadmap.md"),
		filepath.Join(root, "README.md"),
		filepath.Join(root, "..", "outside.go"),
	}, root)

	want := []ConditionalSkillMatch{
		{SkillName: "docs-review", Pattern: "docs", Path: "docs/roadmap.md", Source: "codex-plugin-skill", TrustLevel: "plugin_cache", External: true},
		{SkillName: "go-review", Pattern: "internal/**/*.go", Path: "internal/skill/skill.go", Source: "claude-skill", TrustLevel: "local_compatible", External: false},
	}
	if !reflect.DeepEqual(matches, want) {
		t.Fatalf("matches = %#v, want %#v", matches, want)
	}
}

func TestRegistryConditionalMatchesIgnoresUnconditionalAndUnsafePaths(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Add(&Skill{Name: "go-review", Paths: []string{"internal/**/*.go"}})
	reg.Add(&Skill{Name: "always-on"})

	matches := reg.ConditionalMatchesForPaths([]string{
		"",
		"../internal/skill/skill.go",
		"/tmp/internal/skill/skill.go",
		"README.md",
	}, "")
	if len(matches) != 0 {
		t.Fatalf("matches = %#v, want none", matches)
	}
}
