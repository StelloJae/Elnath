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

	// CacheBoundary classifies the node for prompt-cache placement. Nodes
	// returning CacheBoundaryStable produce content that is identical
	// across calls within a session and therefore belongs in the
	// cacheable cross-session prefix. CacheBoundaryVolatile content
	// varies per call (memory, RAG, per-tick state) and belongs after
	// SystemPromptDynamicBoundary.
	//
	// This is metadata only; the builder does not memoize on the basis
	// of this signal (Anthropic's prompt cache memoizes server-side).
	// Downstream consumers (cmd elnath debug prompt-cache, future
	// cache_control block splitting) read the classification.
	CacheBoundary() CacheBoundary
}

// CacheBoundary tags a node for cache placement.
type CacheBoundary int

const (
	// CacheBoundaryStable marks a node whose Render output is byte-stable
	// for the lifetime of a session under unchanged inputs (identity,
	// persona, locale, model guidance, boundary marker, brownfield/
	// greenfield context anchors). Mirrors Claude Code's
	// systemPromptSection() memoization layer (audit.txt §02 System Prompt
	// Architecture).
	CacheBoundaryStable CacheBoundary = iota

	// CacheBoundaryVolatile marks a node whose Render output is
	// expected to change on every call: memory snapshots, wiki RAG,
	// per-tick self-state, session summaries, skill catalogues that
	// depend on session context. Mirrors CC's
	// DANGEROUS_uncachedSystemPromptSection().
	CacheBoundaryVolatile
)

// String returns the lowercase identifier used in debug surfaces.
func (b CacheBoundary) String() string {
	switch b {
	case CacheBoundaryStable:
		return "stable"
	case CacheBoundaryVolatile:
		return "volatile"
	default:
		return "unknown"
	}
}

// SystemPromptDynamicBoundary is the canonical marker that separates the
// cacheable cross-session prefix from the per-session volatile suffix in
// the rendered prompt. Mirrors CC's SYSTEM_PROMPT_DYNAMIC_BOUNDARY token
// (audit.txt §02). Builders should register a DynamicBoundaryNode at the
// transition point to emit this string.
const SystemPromptDynamicBoundary = dynamicBoundary
