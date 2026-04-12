package prompt

import (
	"context"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/wiki"
)

// RenderState is a read-only snapshot passed to every Node.Render call.
// Any field may be nil or empty; nodes must handle that gracefully.
type RenderState struct {
	SessionID     string
	UserInput     string
	Self          *self.SelfState
	Messages      []llm.Message
	WikiIdx       *wiki.Index
	TokenBudget   int
	PersonaExtra  string
	Model         string
	Provider      string
	ToolNames     []string
	WorkDir       string
	ExistingCode  bool
	VerifyHint    bool
	BenchmarkMode bool
	TaskLanguage  string
}

// Node is a single prompt contribution rendered into the builder output.
type Node interface {
	Name() string
	Priority() int
	Render(ctx context.Context, state *RenderState) (string, error)
}
