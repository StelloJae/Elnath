package skill

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/tools"
)

func TestCatalogToolListsSkillsWithoutPromptsByDefault(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{
		Name:          "review-pr",
		Description:   "Review pull requests",
		Trigger:       "/review-pr",
		RequiredTools: []string{"read_file", "grep"},
		Paths:         []string{"internal/**/*.go"},
		Model:         "gpt-5.5",
		Effort:        "high",
		BaseDir:       "/tmp/elnath-skills/review-pr",
		Prompt:        "Secret detailed prompt",
		Status:        "active",
		Source:        "claude-skill",
	})

	tool := NewCatalogTool(reg)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	if strings.Contains(res.Output, "Secret detailed prompt") {
		t.Fatalf("list output leaked prompt: %s", res.Output)
	}

	var out struct {
		Action string `json:"action"`
		Skills []struct {
			Name          string   `json:"name"`
			Description   string   `json:"description"`
			Trigger       string   `json:"trigger"`
			RequiredTools []string `json:"required_tools"`
			Paths         []string `json:"paths"`
			Model         string   `json:"model"`
			Effort        string   `json:"effort"`
			BaseDir       string   `json:"base_dir"`
			Status        string   `json:"status"`
			Source        string   `json:"source"`
			Prompt        string   `json:"prompt,omitempty"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "list" || len(out.Skills) != 1 {
		t.Fatalf("output = %+v, want one listed skill", out)
	}
	got := out.Skills[0]
	if got.Name != "review-pr" || got.Description == "" || got.Trigger != "/review-pr" {
		t.Fatalf("skill metadata = %+v, want review-pr metadata", got)
	}
	if len(got.Paths) != 1 || got.Paths[0] != "internal/**/*.go" {
		t.Fatalf("paths = %v, want [internal/**/*.go]", got.Paths)
	}
	if got.BaseDir != "/tmp/elnath-skills/review-pr" {
		t.Fatalf("base_dir = %q, want skill base dir", got.BaseDir)
	}
	if got.Prompt != "" {
		t.Fatalf("prompt = %q, want omitted by default", got.Prompt)
	}
}

func TestCatalogToolShowsSkillPromptOnlyWhenRequested(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{Name: "audit", Prompt: "Detailed audit prompt", Status: "active"})

	tool := NewCatalogTool(reg)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"show","skill":"audit","include_prompt":true}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Action string `json:"action"`
		Skill  struct {
			Name   string `json:"name"`
			Prompt string `json:"prompt"`
		} `json:"skill"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "show" || out.Skill.Name != "audit" || out.Skill.Prompt != "Detailed audit prompt" {
		t.Fatalf("output = %+v, want audit prompt only when requested", out)
	}
}

func TestCatalogToolRecommendsSkillsByQuery(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{
		Name:        "review-pr",
		Description: "Review pull requests and CI failures",
		Trigger:     "/review-pr",
		Prompt:      "Detailed review prompt",
		Status:      "active",
	})
	reg.Add(&Skill{
		Name:        "deploy-check",
		Description: "Prepare a deployment checklist",
		Trigger:     "/deploy-check",
		Prompt:      "Detailed deploy prompt",
		Status:      "active",
	})

	tool := NewCatalogTool(reg)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"recommend","query":"pull request review","max_results":1}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	if strings.Contains(res.Output, "Detailed review prompt") || strings.Contains(res.Output, "Detailed deploy prompt") {
		t.Fatalf("recommend output leaked prompt: %s", res.Output)
	}

	var out struct {
		Action string `json:"action"`
		Query  string `json:"query"`
		Skills []struct {
			Name          string   `json:"name"`
			Score         int      `json:"score"`
			MatchedFields []string `json:"matched_fields"`
			Prompt        string   `json:"prompt,omitempty"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "recommend" || out.Query != "pull request review" || len(out.Skills) != 1 {
		t.Fatalf("output = %+v, want one recommendation for query", out)
	}
	if out.Skills[0].Name != "review-pr" || out.Skills[0].Score <= 0 || len(out.Skills[0].MatchedFields) == 0 {
		t.Fatalf("recommendation = %+v, want scored review-pr match", out.Skills[0])
	}
	if out.Skills[0].Prompt != "" {
		t.Fatalf("prompt = %q, want omitted", out.Skills[0].Prompt)
	}
}

func TestCatalogToolMatchesConditionalSkillPaths(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{Name: "go-review", Paths: []string{"internal/**/*.go"}, Status: "active"})
	reg.Add(&Skill{Name: "docs-review", Paths: []string{"docs"}, Status: "active"})
	reg.Add(&Skill{Name: "always-on", Status: "active"})

	root := t.TempDir()
	params := map[string]any{
		"action": "match_paths",
		"cwd":    root,
		"paths": []string{
			filepath.Join(root, "internal", "skill", "catalog_tool.go"),
			filepath.Join(root, "docs", "roadmap.md"),
			filepath.Join(root, "README.md"),
		},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal params error = %v", err)
	}

	tool := NewCatalogTool(reg)
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Action  string `json:"action"`
		Matches []struct {
			SkillName string `json:"skill_name"`
			Pattern   string `json:"pattern"`
			Path      string `json:"path"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "match_paths" || len(out.Matches) != 2 {
		t.Fatalf("output = %+v, want two path matches", out)
	}
	if out.Matches[0].SkillName != "docs-review" || out.Matches[0].Path != "docs/roadmap.md" {
		t.Fatalf("first match = %+v, want docs-review docs/roadmap.md", out.Matches[0])
	}
	if out.Matches[1].SkillName != "go-review" || out.Matches[1].Path != "internal/skill/catalog_tool.go" {
		t.Fatalf("second match = %+v, want go-review internal/skill/catalog_tool.go", out.Matches[1])
	}
}

func TestCatalogToolRejectsUnknownSkill(t *testing.T) {
	tool := NewCatalogTool(NewRegistry())
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"show","skill":"missing"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "skill \"missing\" not found") {
		t.Fatalf("result = %+v, want missing-skill error", res)
	}
}

func TestCatalogToolMetadataIsReadOnly(t *testing.T) {
	tool := NewCatalogTool(NewRegistry())
	if tool.Name() != "skill_catalog" {
		t.Fatalf("Name() = %q, want skill_catalog", tool.Name())
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatal("skill_catalog should be read-only and reversible")
	}
	if got := tool.Scope(nil); len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 || got.Network || got.Persistent {
		t.Fatalf("Scope(nil) = %+v, want empty read-only scope", got)
	}
	if tool.ShouldCancelSiblingsOnError() {
		t.Fatal("skill_catalog should not cancel sibling tools on error")
	}
}

var _ tools.Tool = (*CatalogTool)(nil)
