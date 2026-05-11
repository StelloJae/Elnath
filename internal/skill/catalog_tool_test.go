package skill

import (
	"context"
	"encoding/json"
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
		Model:         "gpt-5.5",
		Effort:        "high",
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
			Model         string   `json:"model"`
			Effort        string   `json:"effort"`
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
