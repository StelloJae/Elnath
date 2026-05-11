package skill

import (
	"context"
	"encoding/json"
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
		Model:         "skill-model",
		Prompt:        "Review $ARGUMENTS in {target}",
		Status:        "active",
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
	if !strings.Contains(res.Output, `"skill":"review-pr"`) || !strings.Contains(res.Output, `"status":"completed"`) {
		t.Fatalf("output = %s", res.Output)
	}
	if captured.Model != "skill-model" {
		t.Fatalf("captured model = %q, want skill-model", captured.Model)
	}
	if !strings.Contains(captured.System, "Review PR 123 in repo") {
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
