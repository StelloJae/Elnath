package research

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/self"
)

func TestNewTaskRunnerDefaults(t *testing.T) {
	r := NewTaskRunner(nil, "test-model", nil, nil, nil, nil)
	if r.maxRounds != 5 {
		t.Fatalf("maxRounds = %d, want 5", r.maxRounds)
	}
	if r.costCapUSD != 5.0 {
		t.Fatalf("costCapUSD = %v, want 5.0", r.costCapUSD)
	}
	if r.logger == nil {
		t.Fatal("logger = nil, want default logger")
	}
}

func TestNewTaskRunnerWithRunnerMaxRounds(t *testing.T) {
	r := NewTaskRunner(nil, "test-model", nil, nil, nil, slog.Default(), WithRunnerMaxRounds(3))
	if r.maxRounds != 3 {
		t.Fatalf("maxRounds = %d, want 3", r.maxRounds)
	}
}

func TestNewTaskRunnerRejectsNegativeMaxRounds(t *testing.T) {
	r := NewTaskRunner(nil, "test-model", nil, nil, nil, slog.Default(), WithRunnerMaxRounds(-1))
	if r.maxRounds != 5 {
		t.Fatalf("maxRounds = %d, want default 5", r.maxRounds)
	}
}

func TestTaskRunnerRunRejectsEmptyPrompt(t *testing.T) {
	r := NewTaskRunner(nil, "test-model", &mockSearcher{}, newTestWikiStore(t), nil, slog.Default())
	_, err := r.Run(context.Background(), daemon.TaskPayload{Prompt: "   "}, nil)
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestTaskRunnerRunReturnsResearchResult(t *testing.T) {
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `[{"id":"H1","statement":"Test hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`},
			{Content: `I investigated. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`},
			{Content: `Research summary`},
		},
	}
	r := NewTaskRunner(
		provider,
		"test-model",
		&mockSearcher{},
		newTestWikiStore(t),
		newTestUsageTracker(t),
		slog.Default(),
		WithRunnerMaxRounds(1),
	)

	var streamed string
	result, err := r.Run(context.Background(), daemon.TaskPayload{Prompt: "test topic", SessionID: "sess-123"}, func(text string) {
		streamed += text
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Summary == "" {
		t.Fatal("Summary = empty, want non-empty")
	}
	if result.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q, want sess-123", result.SessionID)
	}
	if streamed == "" {
		t.Fatal("expected streamed output")
	}

	var decoded ResearchResult
	if err := json.Unmarshal([]byte(result.Result), &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if decoded.Topic != "test topic" {
		t.Fatalf("decoded topic = %q, want test topic", decoded.Topic)
	}
	if decoded.Summary == "" {
		t.Fatal("decoded summary = empty, want non-empty")
	}
	if len(decoded.Rounds) == 0 {
		t.Fatal("decoded rounds = 0, want at least 1")
	}
}

func TestTaskRunnerRunGeneratesSessionIDWhenMissing(t *testing.T) {
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `[{"id":"H1","statement":"Test hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`},
			{Content: `I investigated. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`},
			{Content: `Research summary`},
		},
	}
	r := NewTaskRunner(
		provider,
		"test-model",
		&mockSearcher{},
		newTestWikiStore(t),
		newTestUsageTracker(t),
		slog.Default(),
		WithRunnerMaxRounds(1),
	)

	result, err := r.Run(context.Background(), daemon.TaskPayload{Prompt: "test topic"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("SessionID = empty, want generated session ID")
	}
}

func TestTaskRunnerRunAppendsLessonsAndSavesSelfState(t *testing.T) {
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `[{"id":"H1","statement":"Test hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`},
			{Content: `I investigated. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`},
			{Content: `Research summary`},
		},
	}
	dataDir := t.TempDir()
	store := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	selfState := self.New(dataDir)

	r := NewTaskRunner(
		provider,
		"test-model",
		&mockSearcher{},
		newTestWikiStore(t),
		newTestUsageTracker(t),
		slog.Default(),
		WithRunnerMaxRounds(1),
		WithRunnerLearning(store),
		WithRunnerSelfState(selfState),
	)

	before := selfState.GetPersona()
	_, err := r.Run(context.Background(), daemon.TaskPayload{Prompt: "test topic", SessionID: "sess-123"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) == 0 {
		t.Fatal("lessons = 0, want persisted lessons")
	}

	after := selfState.GetPersona()
	if after.Persistence <= before.Persistence {
		t.Fatalf("Persistence = %v, want > %v", after.Persistence, before.Persistence)
	}

	reloaded, err := self.Load(dataDir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.Persona.Persistence != after.Persistence {
		t.Fatalf("reloaded persistence = %v, want %v", reloaded.Persona.Persistence, after.Persistence)
	}
}

func TestTaskRunnerApplyLearningStoresLessonsWithoutSelfState(t *testing.T) {
	dataDir := t.TempDir()
	store := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	r := NewTaskRunner(nil, "test-model", nil, nil, nil, slog.Default(), WithRunnerLearning(store))

	r.applyLearning(&ResearchResult{
		Topic:     "topic-a",
		TotalCost: 3.0,
		Rounds: []RoundResult{{
			Hypothesis: Hypothesis{ID: "H1", Statement: "stmt"},
			Result:     ExperimentResult{Findings: "finding", Confidence: "high", Supported: true},
		}},
	})

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 2 {
		t.Fatalf("len(lessons) = %d, want 2", len(lessons))
	}
}

func TestTaskRunnerApplyLearningNoopsWhenStoreMissing(t *testing.T) {
	r := NewTaskRunner(nil, "test-model", nil, nil, nil, slog.Default())
	r.applyLearning(&ResearchResult{Topic: "topic-a"})
}
