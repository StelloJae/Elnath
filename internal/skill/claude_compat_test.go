package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadClaudeSkillDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "review-pr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: Review pull requests
when_to_use: When the user asks for PR review
allowed-tools:
  - read_file
  - grep
model: gpt-5.5
effort: high
---
Review the pull request using $ARGUMENTS.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadClaudeSkillDir(root)
	if err != nil {
		t.Fatalf("LoadClaudeSkillDir: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(skills))
	}

	want := &Skill{
		Name:          "review-pr",
		Description:   "Review pull requests - When the user asks for PR review",
		Trigger:       "/review-pr",
		RequiredTools: []string{"read_file", "grep"},
		Model:         "gpt-5.5",
		Effort:        "high",
		Prompt:        "Review the pull request using $ARGUMENTS.",
		Status:        "active",
		Source:        "claude-skill",
	}
	if !reflect.DeepEqual(skills[0], want) {
		t.Fatalf("skill = %#v, want %#v", skills[0], want)
	}
}

func TestLoadClaudeSkillDirMissingIsNoop(t *testing.T) {
	t.Parallel()

	skills, err := LoadClaudeSkillDir(t.TempDir())
	if err != nil {
		t.Fatalf("LoadClaudeSkillDir: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("len(skills) = %d, want 0", len(skills))
	}
}

func TestLoadClaudeSkillDirAcceptsCommaSeparatedAllowedTools(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "fix-tests")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: Fix tests
allowed-tools: bash, read_file, grep
---
Fix the tests.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadClaudeSkillDir(root)
	if err != nil {
		t.Fatalf("LoadClaudeSkillDir: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(skills))
	}
	want := []string{"bash", "read_file", "grep"}
	if !reflect.DeepEqual(skills[0].RequiredTools, want) {
		t.Fatalf("RequiredTools = %#v, want %#v", skills[0].RequiredTools, want)
	}
}

func TestLoadClaudeSkillDirNormalizesClaudeToolNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "ship-pr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: Ship PR
allowed-tools:
  - Bash(git:*)
  - Read
  - Edit
  - ToolSearch
---
Ship the PR.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadClaudeSkillDir(root)
	if err != nil {
		t.Fatalf("LoadClaudeSkillDir: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(skills))
	}
	want := []string{"bash", "read_file", "edit_file", "tool_search"}
	if !reflect.DeepEqual(skills[0].RequiredTools, want) {
		t.Fatalf("RequiredTools = %#v, want %#v", skills[0].RequiredTools, want)
	}
}

func TestRegistryLoadClaudeSkills(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "audit")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: Audit the repo
---
Audit carefully.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	if err := reg.LoadClaudeSkills(root); err != nil {
		t.Fatalf("LoadClaudeSkills: %v", err)
	}
	sk, ok := reg.Get("audit")
	if !ok {
		t.Fatal("audit skill missing")
	}
	if sk.Source != "claude-skill" {
		t.Fatalf("Source = %q, want claude-skill", sk.Source)
	}
}
