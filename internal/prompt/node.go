package prompt

import (
	"context"

	"github.com/stello/elnath/internal/identity"
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
	Principal     identity.Principal
	Messages      []llm.Message
	WikiIdx       *wiki.Index
	TokenBudget   int
	Locale        string
	PersonaExtra  string
	Model         string
	Provider      string
	ToolNames     []string
	WorkDir       string
	// SessionWorkDir, when non-empty, is the per-session subdir advertised
	// to the LLM by SelfStateNode in place of the shared root WorkDir.
	// Search-oriented nodes (ContextFilesNode, ProjectContextNode) keep
	// using WorkDir so project-root files (CLAUDE.md, ELNATH.md, etc.)
	// remain reachable. Empty falls back to WorkDir.
	SessionWorkDir string
	ExistingCode   bool
	VerifyHint    bool
	BenchmarkMode bool
	TaskLanguage  string
	DaemonMode    bool
	ProjectID     string
	MessageCount  int
	// IsChat is true when rendering the Telegram chat-path prompt so
	// chat-only nodes (ChatSystemPromptNode / ChatToolGuideNode) gate on
	// a single explicit boolean instead of re-inferring surface. The task
	// path leaves this false, which keeps chat content out of task prompts.
	IsChat bool
	// AvailableTools enumerates tool names the chat tool loop exposes for
	// this turn. Populated only when ChatResponder.useToolLoop() is true;
	// chat legacy stream and task path leave it nil so the chat tool guide
	// node doesn't render a guide the model cannot act on.
	AvailableTools []string
}

// Node is a single prompt contribution rendered into the builder output.
type Node interface {
	Name() string
	Priority() int
	Render(ctx context.Context, state *RenderState) (string, error)
}
