package orchestrator

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/self"
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
