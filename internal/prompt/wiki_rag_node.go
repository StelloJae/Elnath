package prompt

import (
	"context"
	"strings"

	"github.com/stello/elnath/internal/wiki"
)

// WikiRAGNode renders wiki RAG context through the existing wiki package.
type WikiRAGNode struct {
	priority   int
	maxResults int
}

func NewWikiRAGNode(priority, maxResults int) *WikiRAGNode {
	if maxResults <= 0 {
		maxResults = 3
	}
	return &WikiRAGNode{priority: priority, maxResults: maxResults}
}

func (n *WikiRAGNode) Name() string {
	return "wiki_rag"
}

func (n *WikiRAGNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *WikiRAGNode) Render(ctx context.Context, state *RenderState) (string, error) {
	if state != nil && state.BenchmarkMode {
		return "", nil
	}
	if n == nil || state == nil || state.WikiIdx == nil || strings.TrimSpace(state.UserInput) == "" {
		return "", nil
	}
	return wiki.BuildRAGContext(ctx, state.WikiIdx, state.UserInput, n.maxResults), nil
}
