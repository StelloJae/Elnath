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
