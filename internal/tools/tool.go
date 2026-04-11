package tools

import (
	"context"
	"encoding/json"
)

// Tool is the interface all executable tools must implement.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, params json.RawMessage) (*Result, error)

	// IsConcurrencySafe reports whether this invocation can run in parallel
	// with OTHER concurrency-safe invocations without any ordering guarantees.
	// The predicate is self-referential: a true return means "I have no
	// observable side-effects that need ordering vs other safe calls".
	// Cross-call collision (same write path, same DB row) is the partitioner's
	// job — it uses Scope() for that, not this method.
	// Params may be nil or malformed; implementations MUST tolerate that and
	// fall back to a conservative (false) default.
	IsConcurrencySafe(params json.RawMessage) bool

	// Reversible reports whether the tool's effect can be undone by a
	// subsequent call (a read has nothing to undo → true; a file overwrite
	// loses prior content → false). This is a static property of the tool,
	// not of the invocation.
	Reversible() bool

	// Scope returns the read/write/network/persistent footprint of this
	// specific invocation. File paths MUST be absolute (resolved via
	// PathGuard when applicable) so partitioners can compare them byte-wise.
	// Params may be nil or malformed; implementations MUST return
	// ConservativeScope() in that case so callers fail closed.
	Scope(params json.RawMessage) ToolScope
}

// Result holds the output of a tool execution.
type Result struct {
	Output  string
	IsError bool
}

// ErrorResult returns a Result that signals a tool execution failure.
func ErrorResult(msg string) *Result {
	return &Result{Output: msg, IsError: true}
}

// SuccessResult returns a Result that signals successful tool execution.
func SuccessResult(output string) *Result {
	return &Result{Output: output, IsError: false}
}
