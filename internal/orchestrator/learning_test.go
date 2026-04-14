package orchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/tools"
)

type countingExtractor struct {
	mu      sync.Mutex
	calls   int
	reqs    []learning.ExtractRequest
	lessons []learning.Lesson
	err     error
}

func (c *countingExtractor) Extract(_ context.Context, req learning.ExtractRequest) ([]learning.Lesson, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.reqs = append(c.reqs, req)
	if c.err != nil {
		return nil, c.err
	}
	out := make([]learning.Lesson, len(c.lessons))
	copy(out, c.lessons)
	return out, nil
}

func TestApplyAgentLearningLLMPathComplexityGateBlocks(t *testing.T) {
	t.Parallel()

	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{}
	applyAgentLearning(&LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.DefaultComplexityGate,
		MessageCount:   3,
		ToolCallCount:  1,
	}, learning.AgentResultInfo{Workflow: "single"})

	if extractor.calls != 0 {
		t.Fatalf("extractor calls = %d, want 0", extractor.calls)
	}
	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 0 {
		t.Fatalf("len(lessons) = %d, want 0", len(lessons))
	}
}

func TestApplyAgentLearningLLMPathMockLessonsAppended(t *testing.T) {
	t.Parallel()

	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{lessons: []learning.Lesson{{Text: "first llm lesson", Confidence: "high"}, {Text: "second llm lesson", Confidence: "medium"}}}
	applyAgentLearning(&LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.DefaultComplexityGate,
		MessageCount:   5,
		ToolCallCount:  1,
		SessionID:      "session-1",
	}, learning.AgentResultInfo{Workflow: "single"})

	if extractor.calls != 1 {
		t.Fatalf("extractor calls = %d, want 1", extractor.calls)
	}
	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 2 {
		t.Fatalf("len(lessons) = %d, want 2", len(lessons))
	}
	for _, lesson := range lessons {
		if lesson.Source != "agent:llm:single" {
			t.Fatalf("lesson source = %q, want agent:llm:single", lesson.Source)
		}
	}
}

func TestApplyAgentLearningLLMPathFailClosed(t *testing.T) {
	t.Parallel()

	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{err: errors.New("boom")}
	applyAgentLearning(&LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.DefaultComplexityGate,
		MessageCount:   5,
		ToolCallCount:  1,
		SessionID:      "session-1",
	}, learning.AgentResultInfo{
		Topic:    "repo cleanup",
		Workflow: "single",
		ToolStats: []learning.AgentToolStat{{
			Name:   "bash",
			Calls:  1,
			Errors: 3,
		}},
	})

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1", len(lessons))
	}
	if lessons[0].Source != "agent:single" {
		t.Fatalf("rule lesson source = %q, want agent:single", lessons[0].Source)
	}
}

func TestApplyAgentLearningRuleAndLLMParallel(t *testing.T) {
	t.Parallel()

	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{lessons: []learning.Lesson{{Text: "llm lesson one", Confidence: "high"}, {Text: "llm lesson two", Confidence: "medium"}}}
	applyAgentLearning(&LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.DefaultComplexityGate,
		MessageCount:   5,
		ToolCallCount:  1,
		SessionID:      "session-1",
	}, learning.AgentResultInfo{
		Topic:    "repo cleanup",
		Workflow: "single",
		ToolStats: []learning.AgentToolStat{{
			Name:   "bash",
			Calls:  1,
			Errors: 3,
		}},
	})

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 3 {
		t.Fatalf("len(lessons) = %d, want 3", len(lessons))
	}
	var sources []string
	for _, lesson := range lessons {
		sources = append(sources, lesson.Source)
	}
	sort.Strings(sources)
	want := []string{"agent:llm:single", "agent:llm:single", "agent:single"}
	for i := range want {
		if sources[i] != want[i] {
			t.Fatalf("sources = %#v, want %#v", sources, want)
		}
	}
}

func TestApplyAgentLearningRuleAndLLMDedupesByText(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	state := self.New(dataDir)
	before := state.GetPersona()
	info := learning.AgentResultInfo{
		Topic:    "repo cleanup",
		Workflow: "single",
		ToolStats: []learning.AgentToolStat{{
			Name:   "bash",
			Calls:  1,
			Errors: 3,
		}},
	}
	ruleText := learning.ExtractAgent(info)[0].Text
	extractor := &countingExtractor{lessons: []learning.Lesson{{Text: ruleText, Confidence: "high", PersonaDelta: []self.Lesson{{Param: "caution", Delta: 0.02}}}}}
	applyAgentLearning(&LearningDeps{
		Store:          store,
		SelfState:      state,
		LLMExtractor:   extractor,
		ComplexityGate: learning.DefaultComplexityGate,
		MessageCount:   5,
		ToolCallCount:  1,
		SessionID:      "session-1",
	}, info)

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1 after duplicate text", len(lessons))
	}
	diff := state.GetPersona().Caution - before.Caution
	if diff < 0.019 || diff > 0.021 {
		t.Fatalf("caution delta = %v, want one application near 0.02", diff)
	}
}

func TestApplyAgentLearningLLMPathPersonaHintApplied(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	store := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	state := self.New(dataDir)
	before := state.GetPersona()
	extractor := &countingExtractor{lessons: []learning.Lesson{{
		Text:             "prefer safer retries",
		Confidence:       "high",
		PersonaDirection: "increase",
		PersonaMagnitude: "medium",
		PersonaDelta:     []self.Lesson{{Param: "caution", Delta: 0}},
	}}}
	applyAgentLearning(&LearningDeps{
		Store:          store,
		SelfState:      state,
		LLMExtractor:   extractor,
		ComplexityGate: learning.DefaultComplexityGate,
		MessageCount:   5,
		ToolCallCount:  1,
		SessionID:      "session-1",
	}, learning.AgentResultInfo{Workflow: "single"})

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("len(lessons) = %d, want 1", len(lessons))
	}
	if len(lessons[0].PersonaDelta) != 1 || lessons[0].PersonaDelta[0].Delta != 0.03 {
		t.Fatalf("PersonaDelta = %#v, want caution +0.03", lessons[0].PersonaDelta)
	}
	if state.GetPersona().Caution <= before.Caution {
		t.Fatalf("Caution = %v, want > %v", state.GetPersona().Caution, before.Caution)
	}
	loaded, err := self.Load(dataDir)
	if err != nil {
		t.Fatalf("self.Load() error = %v", err)
	}
	if loaded.GetPersona().Caution <= before.Caution {
		t.Fatalf("saved caution = %v, want > %v", loaded.GetPersona().Caution, before.Caution)
	}
}

func TestApplyAgentLearningLLMPathBreakerOpenSkips(t *testing.T) {
	t.Parallel()

	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{}
	breaker := learning.NewBreaker(nil, learning.BreakerConfig{})
	for i := 0; i < 5; i++ {
		breaker.Record(errors.New("boom"))
	}
	applyAgentLearning(&LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.DefaultComplexityGate,
		MessageCount:   5,
		ToolCallCount:  1,
		SessionID:      "session-1",
		Breaker:        breaker,
	}, learning.AgentResultInfo{Workflow: "single"})

	if extractor.calls != 0 {
		t.Fatalf("extractor calls = %d, want 0 when breaker is open", extractor.calls)
	}
}

func TestApplyAgentLearningMockFallbackDoesNotUpdateBreaker(t *testing.T) {
	t.Parallel()

	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	breaker := learning.NewBreaker(nil, learning.BreakerConfig{})
	applyAgentLearning(&LearningDeps{
		Store:          store,
		LLMExtractor:   &learning.MockLLMExtractor{},
		ComplexityGate: learning.DefaultComplexityGate,
		MessageCount:   5,
		ToolCallCount:  1,
		SessionID:      "session-1",
		Breaker:        breaker,
	}, learning.AgentResultInfo{Workflow: "single"})

	if !breaker.LastRun().IsZero() {
		t.Fatalf("breaker.LastRun() = %v, want zero for mock fallback", breaker.LastRun())
	}
}

func TestApplyPersonaHintSynthesizesPersonaDelta(t *testing.T) {
	t.Parallel()

	lesson := learning.Lesson{PersonaParam: "caution", PersonaDirection: "increase", PersonaMagnitude: "medium"}
	applyPersonaHint(&lesson)
	if len(lesson.PersonaDelta) != 1 {
		t.Fatalf("len(PersonaDelta) = %d, want 1", len(lesson.PersonaDelta))
	}
	if lesson.PersonaDelta[0].Param != "caution" || lesson.PersonaDelta[0].Delta != 0.03 {
		t.Fatalf("PersonaDelta = %#v, want caution +0.03", lesson.PersonaDelta)
	}
}

func TestApplyPersonaHintMissingParamAndEmptyDeltaNoOp(t *testing.T) {
	t.Parallel()

	lesson := learning.Lesson{PersonaDirection: "increase", PersonaMagnitude: "medium"}
	applyPersonaHint(&lesson)
	if len(lesson.PersonaDelta) != 0 {
		t.Fatalf("PersonaDelta = %#v, want empty", lesson.PersonaDelta)
	}
}

func TestApplyPersonaHintUpdatesAllExistingDeltas(t *testing.T) {
	t.Parallel()

	lesson := learning.Lesson{
		PersonaParam:     "caution",
		PersonaDirection: "increase",
		PersonaMagnitude: "medium",
		PersonaDelta: []self.Lesson{
			{Param: "caution", Delta: 0},
			{Param: "verbosity", Delta: -0.02},
		},
	}
	applyPersonaHint(&lesson)
	if lesson.PersonaDelta[0].Delta != 0.03 {
		t.Fatalf("caution delta = %v, want 0.03", lesson.PersonaDelta[0].Delta)
	}
	if lesson.PersonaDelta[1].Delta != 0.03 {
		t.Fatalf("verbosity delta = %v, want 0.03", lesson.PersonaDelta[1].Delta)
	}
}

func TestApplyAgentLearningConcurrentSingleRunsNoCrossTalk(t *testing.T) {
	t.Parallel()

	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{}
	shared := &LearningDeps{Store: store, LLMExtractor: extractor, ComplexityGate: learning.DefaultComplexityGate}
	info := learning.AgentResultInfo{Workflow: "single"}

	var wg sync.WaitGroup
	for _, sessionID := range []string{"session-a", "session-b"} {
		sessionID := sessionID
		wg.Add(1)
		go func() {
			defer wg.Done()
			deps := *shared
			deps.SessionID = sessionID
			deps.MessageCount = 5
			deps.ToolCallCount = 1
			applyAgentLearning(&deps, info)
		}()
	}
	wg.Wait()

	if extractor.calls != 2 {
		t.Fatalf("extractor calls = %d, want 2", extractor.calls)
	}
	got := []string{extractor.reqs[0].SessionID, extractor.reqs[1].SessionID}
	sort.Strings(got)
	if got[0] != "session-a" || got[1] != "session-b" {
		t.Fatalf("session IDs = %#v, want [session-a session-b]", got)
	}
}

func TestSingleWorkflowLearningUsesCopiedDeps(t *testing.T) {
	t.Parallel()

	session, err := agent.NewSession(t.TempDir())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	store := learning.NewStore(filepath.Join(t.TempDir(), "lessons.jsonl"))
	extractor := &countingExtractor{}
	shared := &LearningDeps{
		Store:          store,
		LLMExtractor:   extractor,
		ComplexityGate: learning.DefaultComplexityGate,
		SessionID:      "shared-session",
		MessageCount:   99,
		ToolCallCount:  99,
		CompactSummary: func() (string, int) { return "shared", 77 },
	}
	reg := tools.NewRegistry()
	reg.Register(&testTool{name: "bash"})
	provider := &scriptedSingleProvider{messages: []llm.Message{
		assistantStep("", llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`}),
		assistantStep("", llm.CompletedToolCall{ID: "bash-2", Name: "bash", Input: `{}`}),
		assistantStep("done"),
	}}

	_, err = NewSingleWorkflow().Run(context.Background(), WorkflowInput{
		Message:  "run bash",
		Messages: []llm.Message{llm.NewUserMessage("prev"), llm.NewAssistantMessage("prev")},
		Session:  session,
		Tools:    reg,
		Provider: provider,
		Config: WorkflowConfig{
			MaxIterations: 5,
			Permission:    agent.NewPermission(agent.WithMode(agent.ModeBypass)),
		},
		Learning: shared,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if extractor.calls != 1 {
		t.Fatalf("extractor calls = %d, want 1", extractor.calls)
	}
	if extractor.reqs[0].SessionID != session.ID {
		t.Fatalf("SessionID = %q, want %q", extractor.reqs[0].SessionID, session.ID)
	}
	if shared.SessionID != "shared-session" || shared.MessageCount != 99 || shared.ToolCallCount != 99 {
		t.Fatalf("shared deps mutated = %#v", shared)
	}
}
