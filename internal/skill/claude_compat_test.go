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

func TestRegistryLoadCompatibleSkillRootsIncludesCodexRoots(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	homeDir := t.TempDir()
	writeCompatSkill(t, filepath.Join(projectRoot, ".claude", "skills", "project-claude"), "Project Claude")
	writeCompatSkill(t, filepath.Join(projectRoot, ".codex", "skills", "project-codex"), "Project Codex")
	writeCompatSkill(t, filepath.Join(homeDir, ".codex", "skills", "user-codex"), "User Codex")
	writeCompatSkill(t, filepath.Join(homeDir, ".agents", "skills", "agent-skill"), "Agent Skill")

	reg := NewRegistry()
	if err := reg.LoadCompatibleSkillRoots(DefaultCompatibleSkillRoots(projectRoot, homeDir)); err != nil {
		t.Fatalf("LoadCompatibleSkillRoots: %v", err)
	}

	want := []string{"agent-skill", "project-claude", "project-codex", "user-codex"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	project, _ := reg.Get("project-codex")
	if project.Source != "codex-skill" {
		t.Fatalf("project-codex source = %q, want codex-skill", project.Source)
	}
	claude, _ := reg.Get("project-claude")
	if claude.Source != "claude-skill" {
		t.Fatalf("project-claude source = %q, want claude-skill", claude.Source)
	}
}

func TestRegistryLoadCompatibleSkillRootsIncludesLegacyCommandSkills(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	commandsDir := filepath.Join(projectRoot, ".claude", "commands")
	if err := os.MkdirAll(filepath.Join(commandsDir, "deploy-check"), 0o755); err != nil {
		t.Fatal(err)
	}
	rawReview := `---
description: Review code
allowed-tools: Read, Grep
---
Review the changed files.
`
	if err := os.WriteFile(filepath.Join(commandsDir, "review-code.md"), []byte(rawReview), 0o644); err != nil {
		t.Fatal(err)
	}
	rawDeploy := `---
description: Check deploy
---
Check deployment readiness.
`
	if err := os.WriteFile(filepath.Join(commandsDir, "deploy-check", "SKILL.md"), []byte(rawDeploy), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	if err := reg.LoadCompatibleSkillRoots(DefaultCompatibleSkillRoots(projectRoot, "")); err != nil {
		t.Fatalf("LoadCompatibleSkillRoots: %v", err)
	}

	want := []string{"deploy-check", "review-code"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	review, _ := reg.Get("review-code")
	if review.Source != "claude-command-skill" {
		t.Fatalf("review source = %q, want claude-command-skill", review.Source)
	}
	if review.Trigger != "/review-code" || review.Description != "Review code" {
		t.Fatalf("review metadata = trigger %q description %q", review.Trigger, review.Description)
	}
	if wantTools := []string{"read_file", "grep"}; !reflect.DeepEqual(review.RequiredTools, wantTools) {
		t.Fatalf("review tools = %v, want %v", review.RequiredTools, wantTools)
	}
	deploy, _ := reg.Get("deploy-check")
	if deploy.Prompt != "Check deployment readiness." {
		t.Fatalf("deploy prompt = %q", deploy.Prompt)
	}
}

func TestRegistryLoadCompatibleSkillRootsAcceptsPlainLegacyCommandMarkdown(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	commandsDir := filepath.Join(projectRoot, ".claude", "commands")
	if err := os.MkdirAll(commandsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "plain-check.md"), []byte("Check the plain command.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	if err := reg.LoadCompatibleSkillRoots(DefaultCompatibleSkillRoots(projectRoot, "")); err != nil {
		t.Fatalf("LoadCompatibleSkillRoots: %v", err)
	}
	sk, ok := reg.Get("plain-check")
	if !ok {
		t.Fatal("plain-check skill missing")
	}
	if sk.Description != "Custom command" || sk.Prompt != "Check the plain command." {
		t.Fatalf("plain command = description %q prompt %q", sk.Description, sk.Prompt)
	}
}

func writeCompatSkill(t *testing.T, dir, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: ` + description + `
---
Do the work.
`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}
