package research

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"

	_ "modernc.org/sqlite"
)

// mockProvider implements llm.Provider with canned responses.
// Chat and Stream share the same response queue so that both the
// hypothesis generator (Chat) and the experiment agent (Stream) consume
// responses in order.
type mockProvider struct {
	chatResponses []llm.ChatResponse
	callIndex     int
}

func (m *mockProvider) next() llm.ChatResponse {
	if m.callIndex >= len(m.chatResponses) {
		return llm.ChatResponse{Content: "fallback response"}
	}
	resp := m.chatResponses[m.callIndex]
	m.callIndex++
	return resp
}

func (m *mockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	resp := m.next()
	return &resp, nil
}

func (m *mockProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	resp := m.next()
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: resp.Content})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{}})
	return nil
}

func (m *mockProvider) Name() string           { return "mock" }
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
			{Content: hypothesisResp},  // round 0: hypothesis
			{Content: experimentResp},  // round 0: experiment agent
			{Content: hypothesisResp},  // round 1: hypothesis
			{Content: experimentResp},  // round 1: experiment agent
			{Content: summaryResp},     // summarize
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
