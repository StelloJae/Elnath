package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	agenticmemory "github.com/stello/elnath/internal/agentic/memory"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"

	_ "modernc.org/sqlite"
)

func newTestUsageTracker(t *testing.T) *llm.UsageTracker {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tracker, err := llm.NewUsageTracker(db)
	if err != nil {
		t.Fatal(err)
	}
	return tracker
}

func newTestWikiStore(t *testing.T) *wiki.Store {
	t.Helper()
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func newTestAgenticStore(t *testing.T) (*agentic.Store, *agentic.AgenticTask) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := agentic.NewStore(db)
	task, err := store.CreateAgenticTask(context.Background(), agentic.AgenticTask{
		Title:              "Agentic research",
		Prompt:             "Research guarded memory writes.",
		Status:             agentic.TaskStatusRunning,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return store, task
}

type researchWorkflowMutationProvider struct {
	chatCalls   int
	streamCalls int
}

func (p *researchWorkflowMutationProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	p.chatCalls++
	if p.chatCalls == 1 {
		return &llm.ChatResponse{Content: `[{"id":"H1","statement":"write file","rationale":"r","test_plan":"write file","priority":1}]`}, nil
	}
	return &llm.ChatResponse{Content: "Research summary"}, nil
}

func (p *researchWorkflowMutationProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.streamCalls++
	switch p.streamCalls {
	case 1:
		cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: "write-1", Name: "write_file"}})
		cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: "write-1", Name: "write_file", Input: `{"file_path":"foo.go","content":"package main\n"}`}})
	default:
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: `{"findings":"f","evidence":"e","confidence":"high","supported":true}`})
	}
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
	return nil
}

func (p *researchWorkflowMutationProvider) Name() string            { return "test" }
func (p *researchWorkflowMutationProvider) Models() []llm.ModelInfo { return nil }

func TestResearchWorkflow_E2E(t *testing.T) {
	ctx := context.Background()

	hypothesisJSON := `[{"id":"H1","statement":"Go channels are faster than mutexes for producer-consumer","rationale":"Because channels avoid lock contention","test_plan":"Benchmark both approaches","priority":1}]`
	experimentResult := `I ran the benchmarks and found clear results. {"findings":"Channels 2x faster for producer-consumer","evidence":"BenchmarkChannel: 150ns/op vs BenchmarkMutex: 310ns/op","confidence":"high","supported":true}`
	summaryText := "Research complete: Go channels outperform mutexes for producer-consumer patterns by 2x."

	// Call sequence: Chat(hypothesis) → Stream(experiment) → Chat(summarize)
	provider := newTestProvider(
		hypothesisJSON,
		experimentResult,
		summaryText,
	)

	deps := &ResearchDeps{
		WikiIndex:    &testWikiSearcher{},
		WikiStore:    newTestWikiStore(t),
		UsageTracker: newTestUsageTracker(t),
		MaxRounds:    1,
		CostCapUSD:   10.0,
	}

	input := testInput("Go concurrency patterns performance", provider)
	input.Extra = deps
	var streamed strings.Builder
	input.Sink = event.OnTextToSink(func(s string) { streamed.WriteString(s) })

	wf := NewResearchWorkflow()
	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("ResearchWorkflow.Run: %v", err)
	}

	if result.Workflow != "research" {
		t.Errorf("workflow = %q, want %q", result.Workflow, "research")
	}

	// FU-OutcomeDropFix regression guard: research workflow must populate
	// FinishReason so runtime.recordOutcome (learning.ShouldRecord gate) logs
	// the outcome. Empty FinishReason silently drops the task from
	// outcomes.jsonl — V2 smoke repro 2026-04-22 confirmed 100% drop before
	// this assert was added.
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q (empty causes silent outcome drop)", result.FinishReason, "stop")
	}

	// hypothesis Chat + experiment Stream + summarize Chat = 3 calls
	if provider.CallCount() != 3 {
		t.Errorf("provider calls = %d, want 3", provider.CallCount())
	}

	if !strings.Contains(result.Summary, "channels") {
		t.Errorf("summary %q should contain research findings", result.Summary)
	}

	// Verify wiki page was written
	pages, err := deps.WikiStore.List()
	if err != nil {
		t.Fatalf("wiki list: %v", err)
	}
	if len(pages) == 0 {
		t.Error("expected research results written to wiki")
	}
	if !strings.Contains(streamed.String(), "topic:") {
		t.Errorf("expected research progress output, got %q", streamed.String())
	}
	if !strings.Contains(streamed.String(), "I ran the benchmarks") {
		t.Errorf("expected experiment stream output, got %q", streamed.String())
	}
}

func TestResearchWorkflow_PropagatesMutationReceipts(t *testing.T) {
	ctx := context.Background()
	provider := &researchWorkflowMutationProvider{}
	deps := &ResearchDeps{
		WikiIndex:    &testWikiSearcher{},
		WikiStore:    newTestWikiStore(t),
		UsageTracker: newTestUsageTracker(t),
		MaxRounds:    1,
		CostCapUSD:   10.0,
	}
	input := testInput("Research file write", provider)
	input.Extra = deps
	input.Tools.Register(&testTool{
		name: "write_file",
		executeFn: func(context.Context, json.RawMessage) (*tools.Result, error) {
			return &tools.Result{
				Output: "wrote foo.go",
				Mutation: &tools.FileMutation{
					Operation:          "write_file",
					Path:               "foo.go",
					Changed:            true,
					DiagnosticLanguage: "go",
					DiagnosticStatus:   "diagnostic_delta_clean",
				},
			}, nil
		},
	})

	result, err := NewResearchWorkflow().Run(ctx, input)
	if err != nil {
		t.Fatalf("ResearchWorkflow.Run: %v", err)
	}
	if len(result.Mutations) != 1 {
		t.Fatalf("Mutations = %+v, want one mutation receipt", result.Mutations)
	}
	if result.Mutations[0].Path != "foo.go" || result.Mutations[0].DiagnosticStatus != "diagnostic_delta_clean" {
		t.Fatalf("mutation receipt = %+v", result.Mutations[0])
	}
}

func TestResearchWorkflow_FallbackWithoutDeps(t *testing.T) {
	ctx := context.Background()

	provider := newTestProvider(
		"Fallback single-agent answer about research topic",
	)

	// No Extra → deps will be nil → fallback to single workflow
	input := testInput("Research something", provider)

	wf := NewResearchWorkflow()
	result, err := wf.Run(ctx, input)
	if err != nil {
		t.Fatalf("ResearchWorkflow fallback: %v", err)
	}

	// Falls back to single workflow
	if result.Summary == "" {
		t.Error("summary should not be empty for fallback")
	}

	if provider.CallCount() != 1 {
		t.Errorf("provider calls = %d, want 1 (single fallback)", provider.CallCount())
	}
}

func TestResearchWorkflowRejectsWhitespaceTopic(t *testing.T) {
	ctx := context.Background()
	deps := &ResearchDeps{
		WikiIndex:    &testWikiSearcher{},
		WikiStore:    newTestWikiStore(t),
		UsageTracker: newTestUsageTracker(t),
	}
	input := testInput("   ", newTestProvider())
	input.Extra = deps

	_, err := NewResearchWorkflow().Run(ctx, input)
	if err == nil {
		t.Fatal("expected error for whitespace topic")
	}
	if !strings.Contains(err.Error(), "topic is required") {
		t.Fatalf("error = %v, want topic validation error", err)
	}
}

func TestResearchWorkflowAppliesLearning(t *testing.T) {
	ctx := context.Background()
	provider := newTestProvider(
		`[{"id":"H1","statement":"Useful hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`,
		`I investigated. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`,
		`Research summary`,
	)
	dataDir := t.TempDir()
	store := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	selfState := self.New(dataDir)

	deps := &ResearchDeps{
		WikiIndex:     &testWikiSearcher{},
		WikiStore:     newTestWikiStore(t),
		UsageTracker:  newTestUsageTracker(t),
		LearningStore: store,
		SelfState:     selfState,
		MaxRounds:     1,
		CostCapUSD:    10.0,
	}

	before := selfState.GetPersona()
	input := testInput("Go concurrency patterns performance", provider)
	input.Extra = deps

	if _, err := NewResearchWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("ResearchWorkflow.Run: %v", err)
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) == 0 {
		t.Fatal("lessons = 0, want persisted lessons")
	}
	if selfState.GetPersona().Persistence <= before.Persistence {
		t.Fatalf("Persistence = %v, want > %v", selfState.GetPersona().Persistence, before.Persistence)
	}
}

func TestResearchWorkflowAgenticMemoryGateBlocksUnverifiedWrites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict string
	}{
		{name: "missing verification"},
		{name: "failed verification", verdict: agentic.VerificationVerdictFailed},
		{name: "inconclusive verification", verdict: agentic.VerificationVerdictInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			provider := newTestProvider(
				`[{"id":"H1","statement":"Useful hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`,
				`I investigated. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`,
				`Research summary`,
			)
			dataDir := t.TempDir()
			lessonStore := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
			wikiStore := newTestWikiStore(t)
			agenticStore, task := newTestAgenticStore(t)
			if tc.verdict != "" {
				if _, err := agenticStore.CreateVerificationRun(ctx, agentic.VerificationRun{
					TaskID:           task.ID,
					CriteriaJSON:     `{"kind":"research-memory"}`,
					EvidenceRefsJSON: `[]`,
					Verdict:          tc.verdict,
					Reason:           "test verifier result",
				}); err != nil {
					t.Fatalf("CreateVerificationRun: %v", err)
				}
			}

			input := testInput("Go concurrency patterns performance", provider)
			input.Extra = &ResearchDeps{
				WikiIndex:     &testWikiSearcher{},
				WikiStore:     wikiStore,
				UsageTracker:  newTestUsageTracker(t),
				LearningStore: lessonStore,
				MemoryGate:    agenticmemory.NewGate(agenticStore),
				AgenticTaskID: task.ID,
				MaxRounds:     1,
				CostCapUSD:    10.0,
			}

			if _, err := NewResearchWorkflow().Run(ctx, input); err != nil {
				t.Fatalf("ResearchWorkflow.Run: %v", err)
			}
			lessons, err := lessonStore.List()
			if err != nil {
				t.Fatalf("lessonStore.List: %v", err)
			}
			if len(lessons) != 0 {
				t.Fatalf("lessons = %d, want 0 while unverified", len(lessons))
			}
			pages, err := wikiStore.List()
			if err != nil {
				t.Fatalf("wikiStore.List: %v", err)
			}
			if len(pages) != 0 {
				t.Fatalf("wiki pages = %d, want 0 while unverified", len(pages))
			}
			updates, err := agenticStore.ListMemoryUpdatesByTask(ctx, task.ID)
			if err != nil {
				t.Fatalf("ListMemoryUpdatesByTask: %v", err)
			}
			if len(updates) < 2 {
				t.Fatalf("memory updates = %d, want blocked wiki and learning updates", len(updates))
			}
			for _, update := range updates {
				if update.Status != agentic.MemoryUpdateStatusBlocked {
					t.Fatalf("memory update = %+v, want blocked", update)
				}
			}
		})
	}
}

func TestResearchWorkflowAgenticMemoryGateAllowsPassedWrites(t *testing.T) {
	ctx := context.Background()
	provider := newTestProvider(
		`[{"id":"H1","statement":"Useful hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`,
		`I investigated. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`,
		`Research summary`,
	)
	dataDir := t.TempDir()
	lessonStore := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	wikiStore := newTestWikiStore(t)
	agenticStore, task := newTestAgenticStore(t)
	if _, err := agenticStore.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           task.ID,
		CriteriaJSON:     `{"kind":"research-memory"}`,
		EvidenceRefsJSON: `[]`,
		Verdict:          agentic.VerificationVerdictPassed,
		Reason:           "test verifier passed",
	}); err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}

	input := testInput("Go concurrency patterns performance", provider)
	input.Extra = &ResearchDeps{
		WikiIndex:     &testWikiSearcher{},
		WikiStore:     wikiStore,
		UsageTracker:  newTestUsageTracker(t),
		LearningStore: lessonStore,
		MemoryGate:    agenticmemory.NewGate(agenticStore),
		AgenticTaskID: task.ID,
		MaxRounds:     1,
		CostCapUSD:    10.0,
	}

	if _, err := NewResearchWorkflow().Run(ctx, input); err != nil {
		t.Fatalf("ResearchWorkflow.Run: %v", err)
	}
	lessons, err := lessonStore.List()
	if err != nil {
		t.Fatalf("lessonStore.List: %v", err)
	}
	if len(lessons) == 0 {
		t.Fatal("lessons = 0, want verified research lessons")
	}
	pages, err := wikiStore.List()
	if err != nil {
		t.Fatalf("wikiStore.List: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("wiki pages = 0, want verified research wiki writes")
	}
	updates, err := agenticStore.ListMemoryUpdatesByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListMemoryUpdatesByTask: %v", err)
	}
	if len(updates) < 2 {
		t.Fatalf("memory updates = %d, want applied wiki and learning updates", len(updates))
	}
	for _, update := range updates {
		if update.Status != agentic.MemoryUpdateStatusApplied {
			t.Fatalf("memory update = %+v, want applied", update)
		}
	}
}
