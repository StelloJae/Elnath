package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/wiki"
)

func writeSkillTestConfig(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(data) error = %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(wiki) error = %v", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	config := "data_dir: " + dataDir + "\n" +
		"wiki_dir: " + wikiDir + "\n" +
		"locale: en\n" +
		"permission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(config), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	return cfgPath, dataDir, wikiDir
}

func writeSkillPage(t *testing.T, wikiDir string, page *wiki.Page) {
	t.Helper()
	store, err := wiki.NewStore(wikiDir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Create(page); err != nil {
		t.Fatalf("Create(%q) error = %v", page.Path, err)
	}
}

func withStdin(t *testing.T, input string) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("WriteString(stdin) error = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close(stdin writer) error = %v", err)
	}
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = r.Close()
	})
}

func TestCmdSkillList(t *testing.T) {
	cfgPath, _, wikiDir := writeSkillTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	writeSkillPage(t, wikiDir, &wiki.Page{
		Path:    "skills/pr-review.md",
		Title:   "PR Review",
		Type:    wiki.PageTypeAnalysis,
		Tags:    []string{"skill"},
		Content: "Review PR {pr_number}",
		Extra: map[string]any{
			"name":        "pr-review",
			"description": "Review PRs",
			"status":      "active",
		},
	})
	writeSkillPage(t, wikiDir, &wiki.Page{
		Path:    "skills/deploy-check.md",
		Title:   "Deploy Check",
		Type:    wiki.PageTypeAnalysis,
		Tags:    []string{"skill"},
		Content: "Check deploy",
		Extra: map[string]any{
			"name":        "deploy-check",
			"description": "Check deployments",
			"status":      "draft",
		},
	})

	stdout, _ := captureOutput(t, func() {
		if err := cmdSkill(context.Background(), []string{"list"}); err != nil {
			t.Fatalf("cmdSkill(list) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "/pr-review") {
		t.Fatalf("stdout = %q, want active skill", stdout)
	}
	if strings.Contains(stdout, "deploy-check") {
		t.Fatalf("stdout = %q, should hide draft skills by default", stdout)
	}

	stdout, _ = captureOutput(t, func() {
		if err := cmdSkill(context.Background(), []string{"list", "--all"}); err != nil {
			t.Fatalf("cmdSkill(list --all) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "deploy-check") || !strings.Contains(stdout, "[draft]") {
		t.Fatalf("stdout = %q, want draft skill with marker", stdout)
	}
}

func TestCmdSkillListJSON(t *testing.T) {
	cfgPath, _, wikiDir := writeSkillTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	writeSkillPage(t, wikiDir, &wiki.Page{
		Path:    "skills/pr-review.md",
		Title:   "PR Review",
		Type:    wiki.PageTypeAnalysis,
		Tags:    []string{"skill"},
		Content: "Review PR {pr_number}",
		Extra: map[string]any{
			"name":           "pr-review",
			"description":    "Review PRs",
			"trigger":        "/pr-review <number>",
			"required_tools": []string{"bash", "read_file"},
			"status":         "active",
			"source":         "user",
		},
	})

	stdout, _ := captureOutput(t, func() {
		if err := cmdSkill(context.Background(), []string{"list", "--json"}); err != nil {
			t.Fatalf("cmdSkill(list --json) error = %v", err)
		}
	})
	var out struct {
		Skills []struct {
			Name          string   `json:"name"`
			Description   string   `json:"description"`
			Trigger       string   `json:"trigger"`
			RequiredTools []string `json:"required_tools"`
			Status        string   `json:"status"`
			Source        string   `json:"source"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if len(out.Skills) != 1 {
		t.Fatalf("skills = %+v, want one skill", out.Skills)
	}
	got := out.Skills[0]
	if got.Name != "pr-review" || got.Trigger != "/pr-review <number>" || got.Source != "user" {
		t.Fatalf("skill = %+v, want pr-review metadata", got)
	}
	if len(got.RequiredTools) != 2 || got.RequiredTools[0] != "bash" || got.RequiredTools[1] != "read_file" {
		t.Fatalf("required_tools = %v, want [bash read_file]", got.RequiredTools)
	}
}

func TestCmdSkillListCompatibleIncludesSkillRoots(t *testing.T) {
	cfgPath, _, wikiDir := writeSkillTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	root := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore Chdir: %v", err)
		}
	})

	writeSkillPage(t, wikiDir, &wiki.Page{
		Path:    "skills/wiki-skill.md",
		Title:   "Wiki Skill",
		Type:    wiki.PageTypeAnalysis,
		Tags:    []string{"skill"},
		Content: "Use wiki context.",
		Extra: map[string]any{
			"name":        "wiki-skill",
			"description": "Wiki skill",
			"status":      "active",
		},
	})
	writeRuntimeCompatSkill(t, filepath.Join(root, ".codex", "skills", "project-codex"), "Project Codex")

	stdout, _ := captureOutput(t, func() {
		if err := cmdSkill(context.Background(), []string{"list", "--compatible", "--json"}); err != nil {
			t.Fatalf("cmdSkill(list --compatible --json) error = %v", err)
		}
	})
	var out struct {
		Skills []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	seen := map[string]string{}
	for _, sk := range out.Skills {
		seen[sk.Name] = sk.Source
	}
	if _, ok := seen["wiki-skill"]; !ok {
		t.Fatalf("skills = %+v, want wiki skill included", out.Skills)
	}
	if seen["project-codex"] != "codex-skill" {
		t.Fatalf("project-codex source = %q, want codex-skill; skills=%+v", seen["project-codex"], out.Skills)
	}
}

func TestCmdSkillShow(t *testing.T) {
	cfgPath, _, wikiDir := writeSkillTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	writeSkillPage(t, wikiDir, &wiki.Page{
		Path:    "skills/deploy-check.md",
		Title:   "Deploy Check",
		Type:    wiki.PageTypeAnalysis,
		Tags:    []string{"skill"},
		Content: "Check deployment for {env}.",
		Extra: map[string]any{
			"name":           "deploy-check",
			"description":    "Check deployment status",
			"trigger":        "/deploy-check <env>",
			"required_tools": []string{"bash"},
			"status":         "draft",
			"source":         "analyst",
		},
	})

	stdout, _ := captureOutput(t, func() {
		if err := cmdSkill(context.Background(), []string{"show", "deploy-check"}); err != nil {
			t.Fatalf("cmdSkill(show) error = %v", err)
		}
	})
	checks := []string{"Name:        deploy-check", "Status:      draft", "Source:      analyst", "Check deployment for {env}."}
	for _, want := range checks {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q\n%s", want, stdout)
		}
	}
}

func TestCmdSkillRejectsInvalidNameForShowAndEdit(t *testing.T) {
	cfgPath, _, _ := writeSkillTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	t.Setenv("EDITOR", "true")

	for _, args := range [][]string{{"show", "../escape"}, {"edit", "../escape"}} {
		err := cmdSkill(context.Background(), args)
		if err == nil {
			t.Fatalf("cmdSkill(%v) error = nil, want invalid name error", args)
		}
		if !strings.Contains(err.Error(), "invalid skill name") {
			t.Fatalf("cmdSkill(%v) error = %v, want invalid skill name", args, err)
		}
	}
}

func TestCmdSkillCreateDeleteEditAndStats(t *testing.T) {
	cfgPath, dataDir, wikiDir := writeSkillTestConfig(t)
	withArgs(t, []string{"elnath", "--config", cfgPath})

	withStdin(t, "Check deployment status\n/deploy-check <env>\nCheck deployment for {env}.\n")
	stdout, _ := captureOutput(t, func() {
		if err := cmdSkill(context.Background(), []string{"create", "deploy-check"}); err != nil {
			t.Fatalf("cmdSkill(create) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Created skill: /deploy-check") {
		t.Fatalf("stdout = %q, want created confirmation", stdout)
	}
	store, err := wiki.NewStore(wikiDir)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	page, err := store.Read("skills/deploy-check.md")
	if err != nil {
		t.Fatalf("Read(created skill) error = %v", err)
	}
	if got := page.Extra["source"]; got != "user" {
		t.Fatalf("created source = %v, want user", got)
	}

	t.Setenv("EDITOR", "true")
	if err := cmdSkill(context.Background(), []string{"edit", "deploy-check"}); err != nil {
		t.Fatalf("cmdSkill(edit) error = %v", err)
	}

	tracker := skill.NewTracker(dataDir)
	if err := tracker.RecordUsage(skill.UsageRecord{SkillName: "deploy-check", SessionID: "sess-1"}); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	if err := tracker.RecordUsage(skill.UsageRecord{SkillName: "deploy-check", SessionID: "sess-2"}); err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	stdout, _ = captureOutput(t, func() {
		if err := cmdSkill(context.Background(), []string{"stats"}); err != nil {
			t.Fatalf("cmdSkill(stats) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "deploy-check") || !strings.Contains(stdout, "2 invocations") {
		t.Fatalf("stdout = %q, want usage stats", stdout)
	}

	withStdin(t, "y\n")
	stdout, _ = captureOutput(t, func() {
		if err := cmdSkill(context.Background(), []string{"delete", "deploy-check"}); err != nil {
			t.Fatalf("cmdSkill(delete) error = %v", err)
		}
	})
	if !strings.Contains(stdout, "Deleted skill: deploy-check") {
		t.Fatalf("stdout = %q, want delete confirmation", stdout)
	}
	if _, err := store.Read("skills/deploy-check.md"); err == nil {
		t.Fatal("Read(deleted skill) error = nil, want not found")
	}
}

func TestCommandRegistryIncludesSkill(t *testing.T) {
	reg := commandRegistry()
	if _, ok := reg["skill"]; !ok {
		t.Fatal("commandRegistry() missing skill command")
	}
}
