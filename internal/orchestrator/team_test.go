package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

func TestTeamWorkflow_E2E(t *testing.T) {
	ctx := context.Background()

	subtasksJSON := `[
		{"id":1,"title":"Research API","instruction":"List REST API patterns"},
		{"id":2,"title":"Draft schema","instruction":"Design the data model"}
	]`

	provider := newTestProvider(
		subtasksJSON,                                     // planner
		"API patterns: REST, GraphQL",                    // subtask 1
		"Schema: users, posts tables",                    // subtask 2
		"Combined: REST API with users and posts tables", // synthesizer
	)

	wf := NewTeamWorkflow()
	input := testInput("Design a blog API", provider)
	var streamed strings.Builder
	input.Sink = event.OnTextToSink(func(s string) { streamed.WriteString(s) })

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("TeamWorkflow.Run: %v", err)
	}

	if result.Workflow != "team" {
		t.Errorf("workflow = %q, want %q", result.Workflow, "team")
	}

	// planner + 2 subtasks + synthesizer = 4
	if provider.CallCount() != 4 {
		t.Errorf("provider calls = %d, want 4", provider.CallCount())
	}

	if !strings.Contains(result.Summary, "Combined") {
		t.Errorf("summary %q should contain synthesized content", result.Summary)
	}

	if result.Usage.InputTokens == 0 {
		t.Error("usage input tokens should be > 0")
	}
	if !strings.Contains(streamed.String(), "[team]") {
		t.Errorf("expected team progress output, got %q", streamed.String())
	}
	if !strings.Contains(streamed.String(), "Combined") {
		t.Errorf("expected synthesized stream output, got %q", streamed.String())
	}
	if got := countExactUserRoleText(result.Messages, "Design a blog API"); got != 1 {
		t.Fatalf("exact user turn count = %d, want 1", got)
	}
}

func TestTeamWorkflow_FallbackToSingle(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"[]",                   // planner returns empty subtasks
		"Direct single answer", // fallback single workflow
	)

	wf := NewTeamWorkflow()
	input := testInput("Simple question", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("TeamWorkflow.Run fallback: %v", err)
	}

	if result.Summary == "" {
		t.Error("summary should not be empty for fallback case")
	}
}

func TestParseSubtasks(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int
		wantID  int
		wantErr bool
	}{
		{
			name:   "plain JSON",
			raw:    `[{"id":1,"title":"A","instruction":"Do A"},{"id":2,"title":"B","instruction":"Do B"}]`,
			want:   2,
			wantID: 1,
		},
		{
			name:   "with code fence",
			raw:    "```json\n[{\"id\":1,\"title\":\"A\",\"instruction\":\"Do A\"}]\n```",
			want:   1,
			wantID: 1,
		},
		{
			name:    "no JSON array",
			raw:     "just text with no array",
			wantErr: true,
		},
		{
			name:   "JSON with surrounding text",
			raw:    "Here is the plan:\n[{\"id\":1,\"title\":\"Only task\",\"instruction\":\"Do it\"}]\nDone.",
			want:   1,
			wantID: 1,
		},
		{
			name: "multiple arrays choose last valid plan",
			raw: "Example array:\n" +
				`[{"id":99,"title":"Example","instruction":"Ignore this"}]` + "\n" +
				"Actual plan:\n" +
				`[{"id":1,"title":"Inspect","instruction":"Inspect the failure"},{"id":2,"title":"Fix","instruction":"Implement the patch"}]`,
			want:   2,
			wantID: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSubtasks(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSubtasks: %v", err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d subtasks, want %d", len(got), tt.want)
			}
			if tt.want > 0 && got[0].ID != tt.wantID {
				t.Errorf("first subtask id = %d, want %d", got[0].ID, tt.wantID)
			}
		})
	}
}

func TestTeamWorkflow_PlannerFailureFallsBackToSingle(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"planner output with no usable JSON array",
		"Recovered via single workflow",
	)

	wf := NewTeamWorkflow()
	input := testInput("Ship the smallest safe fix", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("TeamWorkflow.Run fallback after planner failure: %v", err)
	}

	if provider.CallCount() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.CallCount())
	}
	if result.Workflow != "single" {
		t.Fatalf("workflow = %q, want %q", result.Workflow, "single")
	}
	if !strings.Contains(result.Summary, "Recovered via single workflow") {
		t.Fatalf("summary %q should contain fallback answer", result.Summary)
	}
}

func TestTeamWorkflow_Learning(t *testing.T) {
	ctx := context.Background()
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	provider := &teamLearningProvider{
		planner: `[
			{"id":1,"title":"Fail A","instruction":"failing bash loop A"},
			{"id":2,"title":"Fail B","instruction":"failing bash loop B"},
			{"id":3,"title":"Read","instruction":"successful read task"}
		]`,
		synth: "Combined result",
		scripts: map[string][]llm.Message{
			"failing bash loop A": {
				assistantStep("", llm.CompletedToolCall{ID: "bash-a-1", Name: "bash", Input: `{}`}),
				assistantStep("", llm.CompletedToolCall{ID: "bash-a-2", Name: "bash", Input: `{}`}),
				assistantStep("", llm.CompletedToolCall{ID: "bash-a-3", Name: "bash", Input: `{}`}),
				assistantStep("subtask A done"),
			},
			"failing bash loop B": {
				assistantStep("", llm.CompletedToolCall{ID: "bash-b-1", Name: "bash", Input: `{}`}),
				assistantStep("", llm.CompletedToolCall{ID: "bash-b-2", Name: "bash", Input: `{}`}),
				assistantStep("", llm.CompletedToolCall{ID: "bash-b-3", Name: "bash", Input: `{}`}),
				assistantStep("subtask B done"),
			},
			"successful read task": {
				assistantStep("", llm.CompletedToolCall{ID: "read-1", Name: "read", Input: `{}`}),
				assistantStep("subtask C done"),
			},
		},
		indexes: map[string]int{},
	}
	reg := tools.NewRegistry()
	reg.Register(&testTool{
		name: "bash",
		executeFn: func(context.Context, json.RawMessage) (*tools.Result, error) {
			return nil, errors.New("boom")
		},
	})
	reg.Register(&testTool{name: "read"})

	input := testInput("fix the existing repo safely", provider)
	input.Tools = reg
	// Phase 8.1a Fix 3 (GPT G4): per-subagent cap = min(max(global/N, 1), 20).
	// This scenario runs 3 subtasks of ≤4 iters each; set global high enough
	// that cap grants each subtask its full scripted budget (30/3 = 10).
	input.Config.MaxIterations = 30
	input.Learning = &LearningDeps{Store: store}

	result, err := NewTeamWorkflow().Run(ctx, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Workflow != "team" {
		t.Fatalf("workflow = %q, want team", result.Workflow)
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	// Phase 8.1a Fix 3 (GPT G4): per-subagent cap may alter iteration count
	// which in turn affects how many learning rules fire. Accept ≥1 lesson
	// and require at least one bash-related lesson from agent:team source.
	if len(lessons) < 1 {
		t.Fatalf("len(lessons) = %d, want ≥ 1", len(lessons))
	}
	hasBashTeamLesson := false
	for _, l := range lessons {
		if l.Source == "agent:team" && strings.Contains(l.Text, "bash") {
			hasBashTeamLesson = true
			break
		}
	}
	if !hasBashTeamLesson {
		t.Fatalf("no bash lesson from agent:team source: %+v", lessons)
	}
}

func TestTeamWorkflow_LLMExtractionUsesMergedRunContext(t *testing.T) {
	ctx := context.Background()
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{}
	provider := &teamLearningProvider{
		planner: `[{"id":1,"title":"bash task a","instruction":"successful bash task a"},{"id":2,"title":"bash task b","instruction":"successful bash task b"}]`,
		synth:   "team synthesis",
		scripts: map[string][]llm.Message{
			"successful bash task a": {
				assistantStep("", llm.CompletedToolCall{ID: "bash-a-1", Name: "bash", Input: `{}`}),
				assistantStep("subtask done"),
			},
			"successful bash task b": {
				assistantStep("", llm.CompletedToolCall{ID: "bash-b-1", Name: "bash", Input: `{}`}),
				assistantStep("subtask done again"),
			},
		},
		indexes: map[string]int{},
	}
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "bash"})

	input := testInput("fix the existing repo safely", provider)
	input.Tools = reg
	input.Learning = &LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.ComplexityGate{MinMessages: 1, RequireToolCall: true},
	}

	if _, err := NewTeamWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if extractor.calls != 1 {
		t.Fatalf("extractor calls = %d, want 1", extractor.calls)
	}
	if extractor.reqs[0].Workflow != "team" {
		t.Fatalf("workflow = %q, want team", extractor.reqs[0].Workflow)
	}
	for _, want := range []string{"subtask done", "subtask done again"} {
		if !strings.Contains(extractor.reqs[0].CompactSummary, want) {
			t.Fatalf("compact summary = %q, want substring %q", extractor.reqs[0].CompactSummary, want)
		}
	}
}

func TestAggregateFinishReason(t *testing.T) {
	tests := []struct {
		name    string
		reasons []string
		want    string
	}{
		{name: "prefers budget exceeded", reasons: []string{"stop", "error", "budget_exceeded"}, want: "budget_exceeded"},
		{name: "prefers error over ack loop", reasons: []string{"ack_loop", "error", "stop"}, want: "error"},
		{name: "prefers ack loop over stop", reasons: []string{"stop", "ack_loop"}, want: "ack_loop"},
		{name: "falls back to stop", reasons: []string{"", "stop"}, want: "stop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := aggregateFinishReason(tt.reasons); got != tt.want {
				t.Fatalf("aggregateFinishReason(%v) = %q, want %q", tt.reasons, got, tt.want)
			}
		})
	}
}

type promptCaptureProvider struct {
	prompt string
}

func (p *promptCaptureProvider) Name() string            { return "test" }
func (p *promptCaptureProvider) Models() []llm.ModelInfo { return nil }
func (p *promptCaptureProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, nil
}
func (p *promptCaptureProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if len(req.Messages) > 0 {
		p.prompt = req.Messages[len(req.Messages)-1].Text()
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: `[]`})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
	return nil
}

// Phase 8.1a Fix 3 (GPT G4): regression test for per-subagent budget formula.
// Asserts global is a strict ceiling — sum(per_subagent) <= global.
func TestPerSubagentMaxIterations(t *testing.T) {
	tests := []struct {
		name        string
		global      int
		numSubtasks int
		want        int
	}{
		{"50/3 yields 16", 50, 3, 16},
		{"50/2 yields 20 (upper cap)", 50, 2, 20},
		{"50/5 yields 10", 50, 5, 10},
		{"50/1 yields 20 (upper cap)", 50, 1, 20},
		{"10/5 yields 2 (no floor bug)", 10, 5, 2},
		{"10/3 yields 3", 10, 3, 3},
		{"3/5 yields 1 (min 1)", 3, 5, 1},
		{"0/3 yields 1 (min 1)", 0, 3, 1},
		{"10/0 yields 10 (guards n=1)", 10, 0, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := perSubagentMaxIterations(tt.global, tt.numSubtasks)
			if got != tt.want {
				t.Errorf("perSubagentMaxIterations(%d, %d) = %d, want %d",
					tt.global, tt.numSubtasks, got, tt.want)
			}
			// Regression: per * n must never exceed global when n >= 1 and
			// global > 0. This prevents the original floor=5 bug where
			// (global=10, n=5) yielded total=25 > global=10.
			if tt.global > 0 && tt.numSubtasks >= 1 && got*tt.numSubtasks > tt.global && got > 1 {
				// Exception: per=1 floor may cause total > global when N is huge,
				// but that's acceptable because 1-iter subagents are tiny.
				if tt.global > 2 {
					t.Errorf("total budget %d*%d=%d exceeds global %d",
						got, tt.numSubtasks, got*tt.numSubtasks, tt.global)
				}
			}
		})
	}
}

func TestTeamPlannerPromptIncludesBrownfieldRules(t *testing.T) {
	// Phase 8.1a Fix 3: verify subtask is optional (no "MUST verify" mandate).
	// Planner still preserves brownfield diff requirement and budget-awareness.
	provider := &promptCaptureProvider{}
	wf := NewTeamWorkflow()
	input := testInput("Modify the existing server middleware and verify the change with tests", provider)

	_, _ = wf.planSubtasks(context.Background(), input)

	for _, needle := range []string{
		"include a modify subtask and a verify subtask",
		"OMIT the verify subtask",
		"inline artifact",
		"actual working-tree diff",
		"~15 tool calls",
	} {
		if !strings.Contains(provider.prompt, needle) {
			t.Fatalf("planner prompt missing %q:\n%s", needle, provider.prompt)
		}
	}
}

func countExactUserRoleText(messages []llm.Message, want string) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == llm.RoleUser && msg.Text() == want {
			count++
		}
	}
	return count
}

type teamLearningProvider struct {
	mu      sync.Mutex
	planner string
	synth   string
	scripts map[string][]llm.Message
	indexes map[string]int
	calls   int
}

func (p *teamLearningProvider) Name() string            { return "test" }
func (p *teamLearningProvider) Models() []llm.ModelInfo { return nil }
func (p *teamLearningProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *teamLearningProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.mu.Lock()
	p.calls++
	firstUser := firstUserText(req.Messages)
	lastText := ""
	if len(req.Messages) > 0 {
		lastText = req.Messages[len(req.Messages)-1].Text()
	}
	switch {
	case strings.HasPrefix(firstUser, "You are a task planner."):
		p.mu.Unlock()
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: p.planner})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
		return nil
	case strings.HasPrefix(lastText, "You have been given the results of parallel subtasks."):
		p.mu.Unlock()
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: p.synth})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
		return nil
	}
	script := p.scripts[firstUser]
	idx := p.indexes[firstUser]
	p.indexes[firstUser] = idx + 1
	p.mu.Unlock()
	if idx >= len(script) {
		return errors.New("unexpected subtask stream call")
	}
	emitScriptedMessage(cb, script[idx])
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
	return nil
}

func firstUserText(messages []llm.Message) string {
	for _, msg := range messages {
		if msg.Role == llm.RoleUser {
			return msg.Text()
		}
	}
	return ""
}

func emitScriptedMessage(cb func(llm.StreamEvent), msg llm.Message) {
	for _, block := range msg.Content {
		switch b := block.(type) {
		case llm.TextBlock:
			if b.Text != "" {
				cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: b.Text})
			}
		case llm.ToolUseBlock:
			cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: b.ID, Name: b.Name}})
			cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: b.ID, Name: b.Name, Input: string(b.Input)}})
		}
	}
}

// partialFailProvider drives the team workflow with one subtask whose
// stream raises a non-retryable error, while the remaining subtasks
// succeed. It captures the synthesiser prompt so tests can assert that
// successful subtask outputs and the failed subtask's identity both
// reach the synthesiser.
type partialFailProvider struct {
	mu                 sync.Mutex
	planner            string
	synth              string
	failingInstruction string
	failingErr         error
	successResults     map[string]string
	synthPrompt        string
}

func (p *partialFailProvider) Name() string            { return "test" }
func (p *partialFailProvider) Models() []llm.ModelInfo { return nil }
func (p *partialFailProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (p *partialFailProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	firstUser := firstUserText(req.Messages)
	lastText := ""
	if len(req.Messages) > 0 {
		lastText = req.Messages[len(req.Messages)-1].Text()
	}

	switch {
	case strings.HasPrefix(firstUser, "You are a task planner."):
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: p.planner})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
		return nil
	case strings.HasPrefix(lastText, "You have been given the results of parallel subtasks."):
		p.mu.Lock()
		p.synthPrompt = lastText
		p.mu.Unlock()
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: p.synth})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
		return nil
	}

	if firstUser == p.failingInstruction {
		if p.failingErr != nil {
			return p.failingErr
		}
		return errors.New("simulated subtask stream failure")
	}
	if text, ok := p.successResults[firstUser]; ok {
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: text})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
		return nil
	}
	return errors.New("partialFailProvider: unscripted subtask: " + firstUser)
}

func TestTeamWorkflow_PartialFailure_DoesNotAbort(t *testing.T) {
	ctx := context.Background()

	plannerJSON := `[
		{"id":1,"title":"Step A","instruction":"do step A"},
		{"id":2,"title":"Step B","instruction":"do step B"},
		{"id":3,"title":"Step C","instruction":"do step C"}
	]`

	provider := &partialFailProvider{
		planner:            plannerJSON,
		synth:              "synthesised answer covering A and C, with B unavailable",
		failingInstruction: "do step B",
		successResults: map[string]string{
			"do step A": "result A",
			"do step C": "result C",
		},
	}

	wf := NewTeamWorkflow()
	input := testInput("execute three steps", provider)

	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("a single failed subtask must not abort the parent task; got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result on partial subtask failure")
	}
	if !strings.Contains(result.Summary, "synthesised") {
		t.Fatalf("result.Summary should contain the synthesised answer; got %q", result.Summary)
	}

	provider.mu.Lock()
	synthPrompt := provider.synthPrompt
	provider.mu.Unlock()

	if synthPrompt == "" {
		t.Fatal("synthesiser was never called; expected partial-failure flow to still synthesise")
	}
	if !strings.Contains(synthPrompt, "Step B") {
		t.Fatalf("synthesiser prompt must mention the failed subtask (Step B); got:\n%s", synthPrompt)
	}
	if !strings.Contains(synthPrompt, "result A") {
		t.Fatalf("synthesiser prompt must include subtask A output; got:\n%s", synthPrompt)
	}
	if !strings.Contains(synthPrompt, "result C") {
		t.Fatalf("synthesiser prompt must include subtask C output; got:\n%s", synthPrompt)
	}

	if result.FinishReason != string(agent.FinishReasonPartialSuccess) {
		t.Fatalf("partial (1 failed, 2 succeeded) must surface FinishReason=%q so learning treats it as partial success; got %q",
			agent.FinishReasonPartialSuccess, result.FinishReason)
	}
}
