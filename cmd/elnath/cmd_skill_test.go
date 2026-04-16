package main

import (
	"context"
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
