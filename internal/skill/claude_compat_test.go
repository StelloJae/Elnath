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
		BaseDir:       skillDir,
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

func TestLoadClaudeSkillDirParsesConditionalPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "go-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: Review Go files
paths:
  - internal/**/*.go
  - docs/**
  - "**"
---
Review Go changes.
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
	want := []string{"internal/**/*.go", "docs"}
	if !reflect.DeepEqual(skills[0].Paths, want) {
		t.Fatalf("Paths = %#v, want %#v", skills[0].Paths, want)
	}
}

func TestLoadClaudeSkillDirParsesArgumentMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "review-pr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: Review PR
argument-hint: "<pr_number> <base>"
arguments:
  - pr_number
  - base
---
Review $pr_number against $base.
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
	if skills[0].Trigger != "/review-pr <pr_number> <base>" {
		t.Fatalf("Trigger = %q, want argument hint", skills[0].Trigger)
	}
	want := []string{"pr_number", "base"}
	if !reflect.DeepEqual(skills[0].ArgumentNames, want) {
		t.Fatalf("ArgumentNames = %#v, want %#v", skills[0].ArgumentNames, want)
	}
}

func TestLoadClaudeSkillDirRecordsSkillBaseDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "with-assets")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---
description: Use colocated files
---
Run scripts/check.sh from this skill directory.
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
	if skills[0].BaseDir != skillDir {
		t.Fatalf("BaseDir = %q, want %q", skills[0].BaseDir, skillDir)
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

func TestDefaultCompatibleSkillRootsIncludesCodexPluginCacheSkills(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	writeCompatSkill(t, filepath.Join(homeDir, ".codex", "plugins", "cache", "openai-bundled", "browser-use", "0.1.0", "skills", "browser"), "Browser")
	writeCompatSkill(t, filepath.Join(homeDir, ".codex", "plugins", "cache", "openai-curated", "github", "63976030", "skills", "github"), "GitHub")

	reg := NewRegistry()
	if err := reg.LoadCompatibleSkillRoots(DefaultCompatibleSkillRoots("", homeDir)); err != nil {
		t.Fatalf("LoadCompatibleSkillRoots: %v", err)
	}

	want := []string{"browser", "github"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	browser, _ := reg.Get("browser")
	if browser.Source != "codex-plugin-skill" {
		t.Fatalf("browser source = %q, want codex-plugin-skill", browser.Source)
	}
}

func TestDefaultCompatibleSkillRootsCanDisableCodexPluginCacheSkills(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	writeCompatSkill(t, filepath.Join(homeDir, ".codex", "skills", "user-codex"), "User Codex")
	writeCompatSkill(t, filepath.Join(homeDir, ".codex", "plugins", "cache", "openai-curated", "github", "63976030", "skills", "github"), "GitHub")

	reg := NewRegistry()
	roots := DefaultCompatibleSkillRootsWithOptions("", homeDir, CompatibleSkillRootOptions{DisablePluginCache: true})
	if err := reg.LoadCompatibleSkillRoots(roots); err != nil {
		t.Fatalf("LoadCompatibleSkillRoots: %v", err)
	}

	want := []string{"user-codex"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
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
