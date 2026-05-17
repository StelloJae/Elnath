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
		ArgumentNames: []string{"pr_number"},
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
			ArgumentNames []string `json:"arguments"`
			Model         string   `json:"model"`
			Effort        string   `json:"effort"`
			BaseDir       string   `json:"base_dir"`
			Status        string   `json:"status"`
			Source        string   `json:"source"`
			TrustLevel    string   `json:"trust_level"`
			External      bool     `json:"external"`
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
	if len(got.ArgumentNames) != 1 || got.ArgumentNames[0] != "pr_number" {
		t.Fatalf("arguments = %v, want [pr_number]", got.ArgumentNames)
	}
	if got.TrustLevel != "local_compatible" || got.External {
		t.Fatalf("trust metadata = level %q external %v, want local_compatible false", got.TrustLevel, got.External)
	}
	if got.Prompt != "" {
		t.Fatalf("prompt = %q, want omitted by default", got.Prompt)
	}
}

func TestCatalogToolExposesUserInvocableMetadata(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{Name: "visible", Status: "active"})
	reg.Add(&Skill{Name: "hidden-helper", Status: "active", Hidden: true})

	tool := NewCatalogTool(reg)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Skills []struct {
			Name          string `json:"name"`
			UserInvocable bool   `json:"user_invocable"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	seen := map[string]bool{}
	for _, got := range out.Skills {
		seen[got.Name] = got.UserInvocable
	}
	if seen["visible"] != true {
		t.Fatalf("visible user_invocable = %v, want true", seen["visible"])
	}
	if seen["hidden-helper"] != false {
		t.Fatalf("hidden-helper user_invocable = %v, want false", seen["hidden-helper"])
	}
}

func TestCatalogToolIncludesDiscoveryReceipt(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{Name: "visible", Description: "Review code", Status: "active", Source: "claude-skill"})
	reg.Add(&Skill{Name: "plugin", Description: "Review code", Status: "active", Source: "codex-plugin-skill"})

	tool := NewCatalogTool(reg)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"recommend","query":"review code","max_results":1,"allow_trust_levels":["local_compatible"]}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Action  string `json:"action"`
		Skills  []any  `json:"skills"`
		Receipt struct {
			Tool               string   `json:"tool"`
			Action             string   `json:"action"`
			ReadOnly           bool     `json:"read_only"`
			RegistryAvailable  bool     `json:"registry_available"`
			TotalSkills        int      `json:"total_skills"`
			ReturnedSkills     int      `json:"returned_skills"`
			ReturnedMatches    int      `json:"returned_matches"`
			TrustFilterApplied bool     `json:"trust_filter_applied"`
			AllowTrustLevels   []string `json:"allow_trust_levels"`
			MaxResults         int      `json:"max_results"`
			Query              string   `json:"query"`
			IncludePrompt      bool     `json:"include_prompt"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Receipt.Tool != CatalogToolName || out.Receipt.Action != "recommend" || !out.Receipt.ReadOnly || !out.Receipt.RegistryAvailable {
		t.Fatalf("receipt identity = %+v", out.Receipt)
	}
	if out.Receipt.TotalSkills != 2 || out.Receipt.ReturnedSkills != 1 || out.Receipt.ReturnedMatches != 0 {
		t.Fatalf("receipt counts = %+v", out.Receipt)
	}
	if !out.Receipt.TrustFilterApplied || len(out.Receipt.AllowTrustLevels) != 1 || out.Receipt.AllowTrustLevels[0] != "local_compatible" {
		t.Fatalf("receipt trust filter = %+v", out.Receipt)
	}
	if out.Receipt.MaxResults != 1 || out.Receipt.Query != "review code" || out.Receipt.IncludePrompt {
		t.Fatalf("receipt request bounds = %+v", out.Receipt)
	}
}

func TestCatalogToolReportsUsageStats(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Add(&Skill{Name: "review-pr", Status: "active", Source: "claude-skill"})
	reg.Add(&Skill{Name: "deploy-check", Status: "active"})
	tracker := NewTracker(t.TempDir())
	if err := tracker.RecordUsage(UsageRecord{SkillName: "review-pr", SessionID: "sess-1", Success: true}); err != nil {
		t.Fatalf("RecordUsage success error = %v", err)
	}
	if err := tracker.RecordUsage(UsageRecord{SkillName: "review-pr", SessionID: "sess-2", Success: false}); err != nil {
		t.Fatalf("RecordUsage failure error = %v", err)
	}

	tool := NewCatalogTool(reg, tracker)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"usage"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Action string `json:"action"`
		Usage  []struct {
			SkillName   string `json:"skill_name"`
			Invocations int    `json:"invocations"`
			Successes   int    `json:"successes"`
			Failures    int    `json:"failures"`
		} `json:"usage"`
		Receipt struct {
			Action           string `json:"action"`
			ReadOnly         bool   `json:"read_only"`
			TrackerAvailable bool   `json:"tracker_available"`
			ReturnedUsage    int    `json:"returned_usage"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "usage" || len(out.Usage) != 1 {
		t.Fatalf("usage output = %+v, want one skill with usage", out)
	}
	got := out.Usage[0]
	if got.SkillName != "review-pr" || got.Invocations != 2 || got.Successes != 1 || got.Failures != 1 {
		t.Fatalf("usage = %+v, want review-pr 2 invocations 1 success 1 failure", got)
	}
	if out.Receipt.Action != "usage" || !out.Receipt.ReadOnly || !out.Receipt.TrackerAvailable || out.Receipt.ReturnedUsage != 1 {
		t.Fatalf("receipt = %+v, want read-only usage receipt", out.Receipt)
	}
}

func TestCatalogToolUsageRequiresTracker(t *testing.T) {
	t.Parallel()

	tool := NewCatalogTool(NewRegistry())
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"usage"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "skill usage tracker is not configured") {
		t.Fatalf("result = %+v, want missing tracker error", res)
	}
}

func TestCatalogToolMarksPluginCacheSkillsAsExternal(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{
		Name:   "browser",
		Status: "active",
		Source: "codex-plugin-skill",
	})

	tool := NewCatalogTool(reg)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"show","skill":"browser"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Skill struct {
			Name       string `json:"name"`
			Source     string `json:"source"`
			TrustLevel string `json:"trust_level"`
			External   bool   `json:"external"`
		} `json:"skill"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Skill.Name != "browser" || out.Skill.Source != "codex-plugin-skill" {
		t.Fatalf("skill = %+v, want browser plugin source", out.Skill)
	}
	if out.Skill.TrustLevel != "plugin_cache" || !out.Skill.External {
		t.Fatalf("trust metadata = level %q external %v, want plugin_cache true", out.Skill.TrustLevel, out.Skill.External)
	}
}

func TestCatalogToolShowsSkillPromptOnlyWhenRequested(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{Name: "audit", ArgumentNames: []string{"target"}, Prompt: "Detailed audit prompt", Status: "active"})

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
			Name          string   `json:"name"`
			ArgumentNames []string `json:"arguments"`
			Prompt        string   `json:"prompt"`
		} `json:"skill"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Action != "show" || out.Skill.Name != "audit" || out.Skill.Prompt != "Detailed audit prompt" {
		t.Fatalf("output = %+v, want audit prompt only when requested", out)
	}
	if len(out.Skill.ArgumentNames) != 1 || out.Skill.ArgumentNames[0] != "target" {
		t.Fatalf("arguments = %v, want [target]", out.Skill.ArgumentNames)
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

func TestCatalogToolFiltersByAllowedTrustLevels(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{Name: "wiki-review", Description: "Review wiki-native notes", Status: "active"})
	reg.Add(&Skill{Name: "local-review", Description: "Review local code", Status: "active", Source: "claude-skill"})
	reg.Add(&Skill{Name: "plugin-review", Description: "Review with plugin skill", Status: "active", Source: "codex-plugin-skill"})

	tool := NewCatalogTool(reg)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list","allow_trust_levels":["wiki","local_compatible"]}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}

	var out struct {
		Skills []struct {
			Name       string `json:"name"`
			TrustLevel string `json:"trust_level"`
			External   bool   `json:"external"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if len(out.Skills) != 2 {
		t.Fatalf("skills = %+v, want wiki/local only", out.Skills)
	}
	seen := map[string]struct{}{}
	for _, got := range out.Skills {
		seen[got.Name] = struct{}{}
		if got.TrustLevel == "plugin_cache" || got.External {
			t.Fatalf("filtered skill leaked plugin-cache metadata: %+v", got)
		}
	}
	if _, ok := seen["wiki-review"]; !ok {
		t.Fatalf("wiki-review missing: %+v", out.Skills)
	}
	if _, ok := seen["local-review"]; !ok {
		t.Fatalf("local-review missing: %+v", out.Skills)
	}
	if _, ok := seen["plugin-review"]; ok {
		t.Fatalf("plugin-review leaked through trust filter: %+v", out.Skills)
	}
}

func TestCatalogToolTrustFilterAppliesToRecommendShowAndMatchPaths(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{Name: "local-review", Description: "Review code", Paths: []string{"internal"}, Status: "active", Source: "claude-skill"})
	reg.Add(&Skill{Name: "plugin-review", Description: "Review code", Paths: []string{"internal"}, Status: "active", Source: "codex-plugin-skill"})

	tool := NewCatalogTool(reg)
	recommend, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"recommend","query":"review code","allow_trust_levels":["plugin_cache"]}`))
	if err != nil {
		t.Fatalf("recommend Execute error = %v", err)
	}
	if recommend.IsError {
		t.Fatalf("recommend returned error result: %s", recommend.Output)
	}
	var recOut struct {
		Skills []struct {
			Name       string `json:"name"`
			TrustLevel string `json:"trust_level"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(recommend.Output), &recOut); err != nil {
		t.Fatalf("recommend output is not JSON: %v\n%s", err, recommend.Output)
	}
	if len(recOut.Skills) != 1 || recOut.Skills[0].Name != "plugin-review" || recOut.Skills[0].TrustLevel != "plugin_cache" {
		t.Fatalf("recommend skills = %+v, want plugin-review only", recOut.Skills)
	}

	show, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"show","skill":"plugin-review","allow_trust_levels":["local_compatible"]}`))
	if err != nil {
		t.Fatalf("show Execute error = %v", err)
	}
	if !show.IsError || !strings.Contains(show.Output, "filtered by allow_trust_levels") {
		t.Fatalf("show result = %+v, want trust-filter error", show)
	}

	root := t.TempDir()
	params := map[string]any{
		"action":             "match_paths",
		"cwd":                root,
		"allow_trust_levels": []string{"local_compatible"},
		"paths":              []string{filepath.Join(root, "internal", "skill", "catalog_tool.go")},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal params error = %v", err)
	}
	matches, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("match_paths Execute error = %v", err)
	}
	if matches.IsError {
		t.Fatalf("match_paths returned error result: %s", matches.Output)
	}
	var matchOut struct {
		Matches []struct {
			SkillName  string `json:"skill_name"`
			TrustLevel string `json:"trust_level"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(matches.Output), &matchOut); err != nil {
		t.Fatalf("match_paths output is not JSON: %v\n%s", err, matches.Output)
	}
	if len(matchOut.Matches) != 1 || matchOut.Matches[0].SkillName != "local-review" || matchOut.Matches[0].TrustLevel != "local_compatible" {
		t.Fatalf("matches = %+v, want local-review only", matchOut.Matches)
	}
}

func TestCatalogToolRejectsUnknownTrustLevelFilter(t *testing.T) {
	tool := NewCatalogTool(NewRegistry())
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list","allow_trust_levels":["mystery"]}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "unsupported trust level") {
		t.Fatalf("result = %+v, want unsupported trust level error", res)
	}
}

func TestCatalogToolMatchesConditionalSkillPaths(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Skill{Name: "go-review", Paths: []string{"internal/**/*.go"}, Status: "active", Source: "claude-skill"})
	reg.Add(&Skill{Name: "docs-review", Paths: []string{"docs"}, Status: "active", Source: "codex-plugin-skill"})
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
			SkillName  string `json:"skill_name"`
			Pattern    string `json:"pattern"`
			Path       string `json:"path"`
			Source     string `json:"source"`
			TrustLevel string `json:"trust_level"`
			External   bool   `json:"external"`
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
	if out.Matches[0].Source != "codex-plugin-skill" || out.Matches[0].TrustLevel != "plugin_cache" || !out.Matches[0].External {
		t.Fatalf("first trust metadata = %+v, want plugin_cache external match", out.Matches[0])
	}
	if out.Matches[1].SkillName != "go-review" || out.Matches[1].Path != "internal/skill/catalog_tool.go" {
		t.Fatalf("second match = %+v, want go-review internal/skill/catalog_tool.go", out.Matches[1])
	}
	if out.Matches[1].Source != "claude-skill" || out.Matches[1].TrustLevel != "local_compatible" || out.Matches[1].External {
		t.Fatalf("second trust metadata = %+v, want local_compatible non-external match", out.Matches[1])
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
