package llm

import (
	"context"
	"encoding/json"
)

// StreamEventType identifies the kind of streaming event.
type StreamEventType string

const (
	EventTextDelta    StreamEventType = "content_delta"
	EventToolUseStart StreamEventType = "tool_use"
	EventToolUseDelta StreamEventType = "tool_use_delta"
	EventToolUseDone  StreamEventType = "tool_use_done"
	EventDone         StreamEventType = "message_stop"
	EventError        StreamEventType = "error"
)

// ToolUseEvent carries tool call info from the model during streaming.
type ToolUseEvent struct {
	ID    string
	Name  string
	Input string // accumulated JSON input
}

// UsageStats holds token counts from a completed request.
type UsageStats struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
}

// StreamEvent is emitted by a provider during streaming.
type StreamEvent struct {
	Type     StreamEventType
	Content  string       // text delta for content_delta events
	ToolCall *ToolUseEvent // populated for tool_use events
	Usage    *UsageStats  // populated for message_stop event
	Error    error        // populated for error events

	// InputTokens is populated for message_start events (convenience field).
	InputTokens int
}

// ToolCall represents a completed tool invocation in a ChatResponse.
type ToolCall struct {
	ID    string
	Name  string
	Input string // JSON string
}

// Usage holds token accounting for a completed ChatRequest.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
}

// ChatRequest is the provider-agnostic input to an LLM call.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolDef
	MaxTokens   int
	Temperature float64
	System      string
}

// ChatResponse is the complete (non-streaming) response from a provider.
type ChatResponse struct {
	Content    string
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason string
}

// Request is an alias kept for internal streaming helpers that were written
// before ChatRequest was introduced.
type Request = ChatRequest

// ToolDef describes a tool the model may call.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ModelInfo describes a model offered by a provider.
type ModelInfo struct {
	ID              string
	Name            string
	MaxTokens       int
	InputPricePerM  float64
	OutputPricePerM float64
}

// Provider is the interface every LLM backend must implement.
// Stream uses a callback pattern so callers handle backpressure themselves (AD-2).
type Provider interface {
	// Chat sends a non-streaming request and returns the complete response.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// Stream sends a request and calls cb for each event until done or error.
	Stream(ctx context.Context, req ChatRequest, cb func(StreamEvent)) error

	// Name returns the provider identifier (e.g. "anthropic", "openai").
	Name() string

	// Models returns the list of models available from this provider.
	Models() []ModelInfo
}
