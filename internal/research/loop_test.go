package research

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"

	_ "modernc.org/sqlite"
)

// mockProvider implements llm.Provider with canned responses.
// Chat and Stream share the same response queue so that both the
// hypothesis generator (Chat) and the experiment agent (Stream) consume
// responses in order. Each incoming ChatRequest is recorded so tests can
// assert on the resolved System prompt (for pipeline prefix coverage).
type mockProvider struct {
	mu            sync.Mutex
	chatResponses []llm.ChatResponse
	callIndex     int
	requests      []llm.ChatRequest
}

func (m *mockProvider) next() llm.ChatResponse {
	if m.callIndex >= len(m.chatResponses) {
		return llm.ChatResponse{Content: "fallback response"}
	}
	resp := m.chatResponses[m.callIndex]
	m.callIndex++
	return resp
}

func (m *mockProvider) record(req llm.ChatRequest) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
}

func (m *mockProvider) capturedSystems() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.requests))
	for i, r := range m.requests {
		out[i] = r.System
	}
	return out
}

func (m *mockProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	m.record(req)
	resp := m.next()
	return &resp, nil
}

func (m *mockProvider) Stream(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	m.record(req)
	resp := m.next()
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: resp.Content})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{}})
	return nil
}

func (m *mockProvider) Name() string            { return "mock" }
func (m *mockProvider) Models() []llm.ModelInfo { return nil }

// mockSearcher implements WikiSearcher with canned results.
type mockSearcher struct {
	results []wiki.SearchResult
}

func (m *mockSearcher) Search(_ context.Context, _ wiki.SearchOpts) ([]wiki.SearchResult, error) {
	return m.results, nil
}

// mockExperimenter wraps ExperimentRunner behavior for testing without a real agent.
// We bypass ExperimentRunner entirely by embedding results directly in the loop test.
// Instead, we use a thin shim: the hypothesis generator and experimenter are constructed
// with the mock provider, so their LLM calls return canned data.

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
	dir := t.TempDir()
	store, err := wiki.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRun_SingleRound(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	hypothesisResp := `[{"id":"H1","statement":"Test hypothesis","rationale":"Because","test_plan":"Do X","priority":1}]`
	experimentResp := `I investigated and found evidence. {"findings":"Found something","evidence":"Data","confidence":"high","supported":true}`
	summaryResp := "Research summary: test hypothesis was supported."

	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: hypothesisResp},
			{Content: experimentResp},
			{Content: summaryResp},
		},
	}

	searcher := &mockSearcher{}
	tracker := newTestUsageTracker(t)
	store := newTestWikiStore(t)

	gen := NewHypothesisGenerator(provider, "test-model", logger)

	loop := &Loop{
		hypothesizer: gen,
		experimenter: &ExperimentRunner{provider: provider, tools: tools.NewRegistry(), model: "test-model", logger: logger},
		wikiIndex:    searcher,
		wikiStore:    store,
		usageTracker: tracker,
		provider:     provider,
		model:        "test-model",
		sessionID:    "test-session",
		maxRounds:    1,
		costCapUSD:   10.0,
		logger:       logger,
	}

	result, err := loop.Run(ctx, "test topic")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if result.Topic != "test topic" {
		t.Errorf("topic = %q, want %q", result.Topic, "test topic")
	}
	if len(result.Rounds) == 0 {
		t.Fatal("expected at least 1 round result")
	}
	if result.Rounds[0].Hypothesis.ID != "H1" {
		t.Errorf("hypothesis ID = %q, want %q", result.Rounds[0].Hypothesis.ID, "H1")
	}

	pages, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	var found bool
	for _, p := range pages {
		if p.PageSource() == wiki.SourceResearch {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one research-sourced wiki page, got %d pages with no research source", len(pages))
	}
}

func TestRun_CostCap(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	hypothesisResp := `[{"id":"H1","statement":"Hypothesis","rationale":"R","test_plan":"P","priority":1}]`
	experimentResp := `{"findings":"F","evidence":"E","confidence":"medium","supported":false}`

	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: hypothesisResp},
			{Content: experimentResp},
			// Round 1 would need these but cost cap should stop it
			{Content: hypothesisResp},
			{Content: experimentResp},
			{Content: "summary"},
		},
	}

	searcher := &mockSearcher{}
	tracker := newTestUsageTracker(t)
	store := newTestWikiStore(t)

	// Pre-seed usage so total cost exceeds cap after round 0 completes.
	// Record a large usage that will cost well above the $0.01 cap.
	err := tracker.Record(ctx, "mock", "test-model", "cost-session", llm.UsageStats{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}

	gen := NewHypothesisGenerator(provider, "test-model", logger)

	loop := &Loop{
		hypothesizer: gen,
		experimenter: &ExperimentRunner{provider: provider, tools: tools.NewRegistry(), model: "test-model", logger: logger},
		wikiIndex:    searcher,
		wikiStore:    store,
		usageTracker: tracker,
		provider:     provider,
		model:        "test-model",
		sessionID:    "cost-session",
		maxRounds:    5,
		costCapUSD:   0.01,
		logger:       logger,
	}

	result, err := loop.Run(ctx, "cost topic")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Cost cap is checked at the start of each round. The pre-seeded cost
	// exceeds the $0.01 cap, so the loop should exit immediately with 0 rounds.
	if len(result.Rounds) != 0 {
		t.Errorf("expected 0 rounds due to cost cap, got %d", len(result.Rounds))
	}
}

func TestRun_Convergence(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	hypothesisResp := `[{"id":"H1","statement":"Converging hypothesis","rationale":"R","test_plan":"P","priority":1}]`
	experimentResp := `{"findings":"Confirmed","evidence":"Strong data","confidence":"high","supported":true}`
	summaryResp := "Converged summary."

	// Call sequence per round: hypothesis Chat, then experiment agent Chat.
	// The agent loop calls provider.Chat once (the mock response has no tool
	// calls, so the agent exits after one iteration). After both rounds the
	// summarize method makes one more Chat call.
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: hypothesisResp}, // round 0: hypothesis
			{Content: experimentResp}, // round 0: experiment agent
			{Content: hypothesisResp}, // round 1: hypothesis
			{Content: experimentResp}, // round 1: experiment agent
			{Content: summaryResp},    // summarize
		},
	}

	searcher := &mockSearcher{}
	tracker := newTestUsageTracker(t)
	store := newTestWikiStore(t)

	gen := NewHypothesisGenerator(provider, "test-model", logger)

	loop := &Loop{
		hypothesizer: gen,
		experimenter: &ExperimentRunner{provider: provider, tools: tools.NewRegistry(), model: "test-model", logger: logger},
		wikiIndex:    searcher,
		wikiStore:    store,
		usageTracker: tracker,
		provider:     provider,
		model:        "test-model",
		sessionID:    "converge-session",
		maxRounds:    10,
		costCapUSD:   100.0,
		logger:       logger,
	}

	result, err := loop.Run(ctx, "convergence topic")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// 2 rounds with high confidence + supported → convergence should stop at round 1.
	if len(result.Rounds) != 2 {
		t.Errorf("expected 2 rounds (convergence), got %d", len(result.Rounds))
	}

	for i, rr := range result.Rounds {
		if !rr.Result.Supported {
			t.Errorf("round %d: expected supported=true", i)
		}
		if rr.Result.Confidence != "high" {
			t.Errorf("round %d: expected confidence=high, got %q", i, rr.Result.Confidence)
		}
	}

	if result.Summary != summaryResp {
		t.Errorf("summary = %q, want %q", result.Summary, summaryResp)
	}
}

func TestShouldStop(t *testing.T) {
	l := &Loop{}

	tests := []struct {
		name   string
		rounds []RoundResult
		want   bool
	}{
		{
			name:   "empty rounds",
			rounds: nil,
			want:   false,
		},
		{
			name: "single round",
			rounds: []RoundResult{
				{Result: ExperimentResult{Supported: true, Confidence: "high"}},
			},
			want: false,
		},
		{
			name: "converged — 2 high confidence supported",
			rounds: []RoundResult{
				{Result: ExperimentResult{Supported: true, Confidence: "high"}},
				{Result: ExperimentResult{Supported: true, Confidence: "high"}},
			},
			want: true,
		},
		{
			name: "stagnated — 2 low confidence",
			rounds: []RoundResult{
				{Result: ExperimentResult{Supported: false, Confidence: "low"}},
				{Result: ExperimentResult{Supported: false, Confidence: "low"}},
			},
			want: true,
		},
		{
			name: "mixed — no stop",
			rounds: []RoundResult{
				{Result: ExperimentResult{Supported: true, Confidence: "high"}},
				{Result: ExperimentResult{Supported: false, Confidence: "medium"}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := l.shouldStop(tt.rounds)
			if got != tt.want {
				t.Errorf("shouldStop() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSanitizeTopic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Simple Topic", "simple-topic"},
		{"Go Memory Management!", "go-memory-management"},
		{"test_underscore", "test-underscore"},
		{"a/b/c", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeTopic(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeTopic(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Prompt pipeline integration (FU-ResearchPipelineIntegration) ---

type stubPrefixRenderer struct {
	prefix string
	err    error

	mu          sync.Mutex
	invocations []Invocation
}

func (s *stubPrefixRenderer) RenderPromptPrefix(_ context.Context, inv Invocation) (string, error) {
	s.mu.Lock()
	s.invocations = append(s.invocations, inv)
	s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	return s.prefix, nil
}

func (s *stubPrefixRenderer) stages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.invocations))
	for i, inv := range s.invocations {
		out[i] = inv.Stage
	}
	return out
}

func TestHypothesisGenerator_PipelinePrefixPrepended(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `[{"id":"H1","statement":"s","rationale":"r","test_plan":"p","priority":1}]`},
		},
	}
	stub := &stubPrefixRenderer{prefix: "You are Elnath.\nMission: Research."}
	gen := NewHypothesisGenerator(provider, "test-model", logger).WithPipeline(stub, "sess-123")

	_, err := gen.Generate(context.Background(), "topic", nil, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	systems := provider.capturedSystems()
	if len(systems) != 1 {
		t.Fatalf("captured %d systems, want 1", len(systems))
	}
	want := "You are Elnath.\nMission: Research.\n\n" + hypothesisSystemPrompt
	if systems[0] != want {
		t.Errorf("system prompt mismatch\n got: %q\nwant: %q", systems[0], want)
	}
	if !strings.HasSuffix(systems[0], hypothesisSystemPrompt) {
		t.Errorf("system should end with legacy hypothesisSystemPrompt, got: %q", systems[0])
	}
	if got := stub.stages(); len(got) != 1 || got[0] != StageHypothesis {
		t.Errorf("stages = %v, want [%s]", got, StageHypothesis)
	}
}

func TestHypothesisGenerator_NilPipelineUsesLegacy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `[{"id":"H1","statement":"s","rationale":"r","test_plan":"p","priority":1}]`},
		},
	}
	gen := NewHypothesisGenerator(provider, "test-model", logger)

	_, err := gen.Generate(context.Background(), "topic", nil, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	systems := provider.capturedSystems()
	if len(systems) != 1 || systems[0] != hypothesisSystemPrompt {
		t.Errorf("expected legacy prompt, got %q", systems[0])
	}
}

func TestHypothesisGenerator_PipelineErrorFallsBackToLegacy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `[{"id":"H1","statement":"s","rationale":"r","test_plan":"p","priority":1}]`},
		},
	}
	stub := &stubPrefixRenderer{err: errors.New("render broken")}
	gen := NewHypothesisGenerator(provider, "test-model", logger).WithPipeline(stub, "sess")

	_, err := gen.Generate(context.Background(), "topic", nil, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	systems := provider.capturedSystems()
	if len(systems) != 1 || systems[0] != hypothesisSystemPrompt {
		t.Errorf("expected legacy fallback on pipeline err, got %q", systems[0])
	}
}

func TestExperimentRunner_PipelinePrefixPrepended(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `investigated. {"findings":"f","evidence":"e","confidence":"high","supported":true}`},
		},
	}
	stub := &stubPrefixRenderer{prefix: "You are Elnath.\nMission: Research."}
	er := NewExperimentRunner(provider, tools.NewRegistry(), "test-model", logger).WithPipeline(stub, "sess-123")

	_, err := er.Run(context.Background(), Hypothesis{ID: "H1", Statement: "s", TestPlan: "p"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	systems := provider.capturedSystems()
	if len(systems) == 0 {
		t.Fatal("no systems captured")
	}
	got := systems[0]
	want := "You are Elnath.\nMission: Research.\n\n" + experimentSystemPrompt
	if got != want {
		t.Errorf("system prompt mismatch\n got: %q\nwant: %q", got, want)
	}
	if !strings.HasSuffix(got, experimentSystemPrompt) {
		t.Errorf("system should end with legacy experimentSystemPrompt, got: %q", got)
	}
	if stages := stub.stages(); len(stages) != 1 || stages[0] != StageExperiment {
		t.Errorf("stages = %v, want [%s]", stages, StageExperiment)
	}
}

func TestExperimentRunner_NilPipelineUsesLegacy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `{"findings":"f","evidence":"e","confidence":"medium","supported":false}`},
		},
	}
	er := NewExperimentRunner(provider, tools.NewRegistry(), "test-model", logger)

	_, err := er.Run(context.Background(), Hypothesis{ID: "H1", Statement: "s", TestPlan: "p"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	systems := provider.capturedSystems()
	if len(systems) == 0 || systems[0] != experimentSystemPrompt {
		t.Errorf("expected legacy prompt, got %q", systems)
	}
}

func TestExperimentRunner_PipelineErrorFallsBackToLegacy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `{"findings":"f","evidence":"e","confidence":"low","supported":false}`},
		},
	}
	stub := &stubPrefixRenderer{err: errors.New("render broken")}
	er := NewExperimentRunner(provider, tools.NewRegistry(), "test-model", logger).WithPipeline(stub, "sess")

	_, err := er.Run(context.Background(), Hypothesis{ID: "H1", Statement: "s", TestPlan: "p"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	systems := provider.capturedSystems()
	if len(systems) == 0 || systems[0] != experimentSystemPrompt {
		t.Errorf("expected legacy fallback on pipeline err, got %q", systems)
	}
}

func TestLoopSummarize_PipelinePrefixPrepended(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	stub := &stubPrefixRenderer{prefix: "You are Elnath.\nMission: Research."}

	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `[{"id":"H1","statement":"s","rationale":"r","test_plan":"p","priority":1}]`},
			{Content: `{"findings":"f","evidence":"e","confidence":"high","supported":true}`},
			{Content: "summary text"},
		},
	}
	loop := &Loop{
		hypothesizer: NewHypothesisGenerator(provider, "test-model", logger).WithPipeline(stub, "s"),
		experimenter: NewExperimentRunner(provider, tools.NewRegistry(), "test-model", logger).WithPipeline(stub, "s"),
		wikiIndex:    &mockSearcher{},
		wikiStore:    newTestWikiStore(t),
		usageTracker: newTestUsageTracker(t),
		provider:     provider,
		model:        "test-model",
		sessionID:    "s",
		maxRounds:    1,
		costCapUSD:   100.0,
		logger:       logger,
		sink:         event.NopSink{},
		pipeline:     stub,
	}

	_, err := loop.Run(ctx, "topic")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	systems := provider.capturedSystems()
	// Last request is the summarize Chat call.
	got := systems[len(systems)-1]
	want := "You are Elnath.\nMission: Research.\n\n" + summarizeSystemPrompt
	if got != want {
		t.Errorf("summarize system mismatch\n got: %q\nwant: %q", got, want)
	}
	if !strings.HasSuffix(got, summarizeSystemPrompt) {
		t.Errorf("summarize system should end with legacy prompt, got: %q", got)
	}

	stages := stub.stages()
	var summarizeSeen bool
	for _, st := range stages {
		if st == StageSummarize {
			summarizeSeen = true
			break
		}
	}
	if !summarizeSeen {
		t.Errorf("expected StageSummarize invocation, stages=%v", stages)
	}
}

func TestLoopSummarize_NilPipelineUsesLegacy(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	provider := &mockProvider{
		chatResponses: []llm.ChatResponse{
			{Content: `[{"id":"H1","statement":"s","rationale":"r","test_plan":"p","priority":1}]`},
			{Content: `{"findings":"f","evidence":"e","confidence":"high","supported":true}`},
			{Content: "summary text"},
		},
	}
	loop := &Loop{
		hypothesizer: NewHypothesisGenerator(provider, "test-model", logger),
		experimenter: NewExperimentRunner(provider, tools.NewRegistry(), "test-model", logger),
		wikiIndex:    &mockSearcher{},
		wikiStore:    newTestWikiStore(t),
		usageTracker: newTestUsageTracker(t),
		provider:     provider,
		model:        "test-model",
		sessionID:    "s",
		maxRounds:    1,
		costCapUSD:   100.0,
		logger:       logger,
		sink:         event.NopSink{},
	}

	_, err := loop.Run(ctx, "topic")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	systems := provider.capturedSystems()
	got := systems[len(systems)-1]
	if got != summarizeSystemPrompt {
		t.Errorf("expected legacy summarize prompt, got %q", got)
	}
}

func (s *stubPrefixRenderer) sessionIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.invocations))
	for i, inv := range s.invocations {
		out[i] = inv.SessionID
	}
	return out
}
