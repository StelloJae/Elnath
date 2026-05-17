package skill

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

func TestInvocationToolExecutesSkillWithArgsAndToolFilter(t *testing.T) {
	t.Parallel()

	skillReg := NewRegistry()
	skillReg.Add(&Skill{
		Name:          "review-pr",
		Description:   "Review PRs",
		RequiredTools: []string{"read_file"},
		ArgumentNames: []string{"pr_number"},
		Model:         "skill-model",
		Prompt:        "Review $pr_number from $ARGUMENTS in {target}",
		Status:        "active",
		Source:        "codex-plugin-skill",
	})
	toolReg := tools.NewRegistry()
	toolReg.Register(&mockTool{name: "read_file"})
	toolReg.Register(&mockTool{name: "bash"})

	var captured llm.ChatRequest
	provider := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			captured = req
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "skill done"})
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
			return nil
		},
	}

	invoke := NewInvocationTool(InvocationToolConfig{
		Registry:   skillReg,
		Provider:   provider,
		Tools:      toolReg,
		Model:      "default-model",
		Permission: agent.NewPermission(agent.WithMode(agent.ModeBypass)),
	})
	res, err := invoke.Execute(context.Background(), json.RawMessage(`{
		"skill": "/review-pr",
		"args": "PR 123",
		"named_args": {"target": "repo"}
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	var out struct {
		Skill      string `json:"skill"`
		Status     string `json:"status"`
		Source     string `json:"source"`
		TrustLevel string `json:"trust_level"`
		External   bool   `json:"external"`
		Output     string `json:"output"`
		Receipt    struct {
			Skill             string   `json:"skill"`
			Provider          string   `json:"provider"`
			Model             string   `json:"model"`
			PermissionMode    string   `json:"permission_mode"`
			RequiredTools     []string `json:"required_tools"`
			AvailableTools    []string `json:"available_tools"`
			ToolFilterApplied bool     `json:"tool_filter_applied"`
			Source            string   `json:"source"`
			TrustLevel        string   `json:"trust_level"`
			External          bool     `json:"external"`
			UserInvocable     bool     `json:"user_invocable"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if out.Skill != "review-pr" || out.Status != "completed" || out.Output != "skill done" {
		t.Fatalf("output = %+v, want completed review-pr output", out)
	}
	if out.Source != "codex-plugin-skill" || out.TrustLevel != "plugin_cache" || !out.External {
		t.Fatalf("trust metadata = %+v, want plugin_cache external", out)
	}
	if out.Receipt.Skill != "review-pr" || out.Receipt.Provider != "mock" || out.Receipt.Model != "skill-model" {
		t.Fatalf("receipt = %+v, want skill/provider/effective model", out.Receipt)
	}
	if out.Receipt.PermissionMode != "bypass" {
		t.Fatalf("receipt permission mode = %q, want bypass", out.Receipt.PermissionMode)
	}
	if !out.Receipt.ToolFilterApplied || !reflect.DeepEqual(out.Receipt.RequiredTools, []string{"read_file"}) || !reflect.DeepEqual(out.Receipt.AvailableTools, []string{"read_file"}) {
		t.Fatalf("receipt tools = %+v, want filtered read_file only", out.Receipt)
	}
	if out.Receipt.Source != "codex-plugin-skill" || out.Receipt.TrustLevel != "plugin_cache" || !out.Receipt.External || !out.Receipt.UserInvocable {
		t.Fatalf("receipt trust metadata = %+v", out.Receipt)
	}
	if captured.Model != "skill-model" {
		t.Fatalf("captured model = %q, want skill-model", captured.Model)
	}
	if !strings.Contains(captured.System, "Review PR 123 from PR 123 in repo") {
		t.Fatalf("system prompt = %q, want rendered skill prompt", captured.System)
	}
	toolNames := map[string]bool{}
	for _, def := range captured.Tools {
		toolNames[def.Name] = true
	}
	if !toolNames["read_file"] {
		t.Fatalf("captured tools = %+v, want read_file", captured.Tools)
	}
	if toolNames["bash"] {
		t.Fatalf("captured tools = %+v, bash should be filtered out", captured.Tools)
	}
}

func TestInvocationToolRecordsSkillUsageOnSuccess(t *testing.T) {
	t.Parallel()

	skillReg := NewRegistry()
	skillReg.Add(&Skill{
		Name:   "probe",
		Prompt: "Probe.",
		Status: "active",
	})
	tracker := NewTracker(t.TempDir())
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
			cb(llm.StreamEvent{Type: llm.EventDone})
			return nil
		},
	}
	invoke := NewInvocationTool(InvocationToolConfig{
		Registry: skillReg,
		Provider: provider,
		Tools:    tools.NewRegistry(),
		Tracker:  tracker,
	})

	ctx := tools.WithSessionID(context.Background(), "session-usage-success")
	res, err := invoke.Execute(ctx, json.RawMessage(`{"skill":"probe"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	var out struct {
		UsageRecorded bool `json:"usage_recorded"`
	}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, res.Output)
	}
	if !out.UsageRecorded {
		t.Fatal("usage_recorded = false, want true")
	}
	records, err := readJSONL[UsageRecord](tracker.usagePath)
	if err != nil {
		t.Fatalf("read usage records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("usage records = %+v, want one record", records)
	}
	if records[0].SkillName != "probe" || records[0].SessionID != "session-usage-success" || !records[0].Success {
		t.Fatalf("usage record = %+v, want successful probe invocation bound to session", records[0])
	}
}

func TestInvocationToolRecordsSkillUsageOnExecutionFailure(t *testing.T) {
	t.Parallel()

	skillReg := NewRegistry()
	skillReg.Add(&Skill{
		Name:   "fragile",
		Prompt: "Fail.",
		Status: "active",
	})
	tracker := NewTracker(t.TempDir())
	provider := &mockProvider{
		streamFn: func(context.Context, llm.ChatRequest, func(llm.StreamEvent)) error {
			return errors.New("provider boom")
		},
	}
	invoke := NewInvocationTool(InvocationToolConfig{
		Registry: skillReg,
		Provider: provider,
		Tools:    tools.NewRegistry(),
		Tracker:  tracker,
	})

	ctx := tools.WithSessionID(context.Background(), "session-usage-failure")
	res, err := invoke.Execute(ctx, json.RawMessage(`{"skill":"fragile"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "provider boom") {
		t.Fatalf("result = %+v, want provider failure error result", res)
	}
	records, err := readJSONL[UsageRecord](tracker.usagePath)
	if err != nil {
		t.Fatalf("read usage records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("usage records = %+v, want one record", records)
	}
	if records[0].SkillName != "fragile" || records[0].SessionID != "session-usage-failure" || records[0].Success {
		t.Fatalf("usage record = %+v, want failed fragile invocation bound to session", records[0])
	}
}

func TestInvocationToolResolvesProviderAndModelAtExecutionTime(t *testing.T) {
	t.Parallel()

	skillReg := NewRegistry()
	skillReg.Add(&Skill{
		Name:   "probe",
		Prompt: "Probe runtime provider.",
		Status: "active",
	})

	var firstCalls, secondCalls int
	first := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
			firstCalls++
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "first"})
			cb(llm.StreamEvent{Type: llm.EventDone})
			return nil
		},
	}
	var captured llm.ChatRequest
	second := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			secondCalls++
			captured = req
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "second"})
			cb(llm.StreamEvent{Type: llm.EventDone})
			return nil
		},
	}

	currentProvider := llm.Provider(first)
	currentModel := "first-model"
	invoke := NewInvocationTool(InvocationToolConfig{
		Registry: skillReg,
		Tools:    tools.NewRegistry(),
		ProviderResolver: func() llm.Provider {
			return currentProvider
		},
		ModelResolver: func() string {
			return currentModel
		},
		Permission: agent.NewPermission(agent.WithMode(agent.ModeBypass)),
	})

	currentProvider = second
	currentModel = "second-model"
	res, err := invoke.Execute(context.Background(), json.RawMessage(`{"skill":"probe"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	if firstCalls != 0 {
		t.Fatalf("first provider calls = %d, want 0 after resolver switch", firstCalls)
	}
	if secondCalls != 1 {
		t.Fatalf("second provider calls = %d, want 1", secondCalls)
	}
	if captured.Model != "second-model" {
		t.Fatalf("captured model = %q, want second-model", captured.Model)
	}
}

func TestInvocationToolNamedArgsOverridePositionalBackfill(t *testing.T) {
	t.Parallel()

	skillReg := NewRegistry()
	skillReg.Add(&Skill{
		Name:          "review-pr",
		ArgumentNames: []string{"pr_number"},
		Prompt:        "Review $pr_number from $ARGUMENTS.",
		Status:        "active",
	})

	var captured llm.ChatRequest
	provider := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			captured = req
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
			cb(llm.StreamEvent{Type: llm.EventDone})
			return nil
		},
	}

	invoke := NewInvocationTool(InvocationToolConfig{
		Registry:   skillReg,
		Provider:   provider,
		Tools:      tools.NewRegistry(),
		Permission: agent.NewPermission(agent.WithMode(agent.ModeBypass)),
	})
	res, err := invoke.Execute(context.Background(), json.RawMessage(`{
		"skill": "review-pr",
		"args": "positional-42",
		"named_args": {"pr_number": "named-99"}
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	if !strings.Contains(captured.System, "Review named-99 from positional-42.") {
		t.Fatalf("system prompt = %q, want named arg to override positional backfill", captured.System)
	}
}

func TestInvocationToolPassesSessionIDToRuntimePlaceholders(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	skillReg := NewRegistry()
	skillReg.Add(&Skill{
		Name:    "asset-skill",
		BaseDir: baseDir,
		Prompt:  "Run ${CLAUDE_SKILL_DIR}/scripts/check.sh for ${CLAUDE_SESSION_ID}.",
		Status:  "active",
	})

	var captured llm.ChatRequest
	provider := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			captured = req
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
			cb(llm.StreamEvent{Type: llm.EventDone})
			return nil
		},
	}

	invoke := NewInvocationTool(InvocationToolConfig{
		Registry:   skillReg,
		Provider:   provider,
		Tools:      tools.NewRegistry(),
		Permission: agent.NewPermission(agent.WithMode(agent.ModeBypass)),
	})
	ctx := tools.WithSessionID(context.Background(), "session-456")
	res, err := invoke.Execute(ctx, json.RawMessage(`{"skill":"asset-skill"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	want := "Run " + baseDir + "/scripts/check.sh for session-456."
	if !strings.Contains(captured.System, want) {
		t.Fatalf("system prompt = %q, want %q", captured.System, want)
	}
}

func TestInvocationToolHonorsTrustLevelAllowlist(t *testing.T) {
	t.Parallel()

	skillReg := NewRegistry()
	skillReg.Add(&Skill{
		Name:   "local-review",
		Prompt: "Review local code.",
		Status: "active",
		Source: "claude-skill",
	})

	var calls int
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
			calls++
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "allowed"})
			cb(llm.StreamEvent{Type: llm.EventDone})
			return nil
		},
	}

	invoke := NewInvocationTool(InvocationToolConfig{
		Registry: skillReg,
		Provider: provider,
		Tools:    tools.NewRegistry(),
	})
	res, err := invoke.Execute(context.Background(), json.RawMessage(`{
		"skill":"local-review",
		"allow_trust_levels":["local_compatible"]
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned error result: %s", res.Output)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}

func TestInvocationToolBlocksDisallowedTrustLevelBeforeProvider(t *testing.T) {
	t.Parallel()

	skillReg := NewRegistry()
	skillReg.Add(&Skill{
		Name:   "plugin-review",
		Prompt: "Review with plugin skill.",
		Status: "active",
		Source: "codex-plugin-skill",
	})

	invoke := NewInvocationTool(InvocationToolConfig{
		Registry: skillReg,
		Tools:    tools.NewRegistry(),
	})
	res, err := invoke.Execute(context.Background(), json.RawMessage(`{
		"skill":"plugin-review",
		"allow_trust_levels":["local_compatible"]
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "filtered by allow_trust_levels") {
		t.Fatalf("result = %+v, want trust-filter error", res)
	}
}

func TestInvocationToolRejectsUnknownTrustLevelAllowlist(t *testing.T) {
	t.Parallel()

	invoke := NewInvocationTool(InvocationToolConfig{
		Registry: NewRegistry(),
		Tools:    tools.NewRegistry(),
	})
	res, err := invoke.Execute(context.Background(), json.RawMessage(`{
		"skill":"review",
		"allow_trust_levels":["mystery"]
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "unsupported trust level") {
		t.Fatalf("result = %+v, want unsupported trust level error", res)
	}
}

func TestInvocationToolDefaultProviderCheckPrecedesSkillLookup(t *testing.T) {
	t.Parallel()

	invoke := NewInvocationTool(InvocationToolConfig{
		Registry: NewRegistry(),
		Tools:    tools.NewRegistry(),
	})
	res, err := invoke.Execute(context.Background(), json.RawMessage(`{"skill":"missing"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "skill provider is not configured") {
		t.Fatalf("result = %+v, want default provider configuration error", res)
	}
}

func TestInvocationToolUnknownSkillReturnsErrorResult(t *testing.T) {
	t.Parallel()

	invoke := NewInvocationTool(InvocationToolConfig{
		Registry: NewRegistry(),
		Provider: &mockProvider{},
		Tools:    tools.NewRegistry(),
	})
	res, err := invoke.Execute(context.Background(), json.RawMessage(`{"skill":"missing"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !res.IsError || !strings.Contains(res.Output, "skill \"missing\" not found") {
		t.Fatalf("result = %+v, want unknown-skill error", res)
	}
}

func TestInvocationToolMetadataIsConservative(t *testing.T) {
	t.Parallel()

	invoke := NewInvocationTool(InvocationToolConfig{})
	if invoke.Name() != "skill" {
		t.Fatalf("Name() = %q, want skill", invoke.Name())
	}
	if invoke.IsConcurrencySafe(nil) {
		t.Fatal("skill invocation should not be concurrency-safe")
	}
	if invoke.Reversible() {
		t.Fatal("skill invocation should not be reversible")
	}
	if got := invoke.Scope(nil); !reflect.DeepEqual(got, tools.ConservativeScope()) {
		t.Fatalf("Scope(nil) = %+v, want conservative", got)
	}
	if !invoke.ShouldCancelSiblingsOnError() {
		t.Fatal("skill invocation errors should cancel sibling batch")
	}
	if !invoke.DeferInitialToolSchema() {
		t.Fatal("skill invocation should be deferred in search-first mode")
	}
}
