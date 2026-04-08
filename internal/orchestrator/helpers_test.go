package orchestrator

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

// testProvider returns canned text responses in FIFO order.
// Thread-safe: team workflow runs subtask agents concurrently.
type testProvider struct {
	mu        sync.Mutex
	responses []string
	idx       int
	calls     atomic.Int32
}

func newTestProvider(responses ...string) *testProvider {
	return &testProvider{responses: responses}
}

func (p *testProvider) nextResponse() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idx >= len(p.responses) {
		return "fallback response"
	}
	r := p.responses[p.idx]
	p.idx++
	return r
}

func (p *testProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls.Add(1)
	return &llm.ChatResponse{Content: p.nextResponse()}, nil
}

func (p *testProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.calls.Add(1)
	text := p.nextResponse()
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: text})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
	return nil
}

func (p *testProvider) Name() string           { return "test" }
func (p *testProvider) Models() []llm.ModelInfo { return nil }

func (p *testProvider) CallCount() int { return int(p.calls.Load()) }

// testWikiSearcher implements research.WikiSearcher with empty results.
type testWikiSearcher struct{}

func (s *testWikiSearcher) Search(_ context.Context, _ wiki.SearchOpts) ([]wiki.SearchResult, error) {
	return nil, nil
}

// testInput builds a minimal WorkflowInput for testing.
func testInput(msg string, provider llm.Provider) WorkflowInput {
	return WorkflowInput{
		Message:  msg,
		Messages: nil,
		Tools:    tools.NewRegistry(),
		Provider: provider,
		Config:   WorkflowConfig{MaxIterations: 10},
	}
}
