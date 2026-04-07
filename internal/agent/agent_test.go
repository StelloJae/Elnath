package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

// mockProvider implements llm.Provider for testing.
type mockProvider struct {
	chatFn   func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
	streamFn func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error
}

func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, req)
	}
	return &llm.ChatResponse{}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	if m.streamFn != nil {
		return m.streamFn(ctx, req, cb)
	}
	return nil
}

func (m *mockProvider) Name() string      { return "mock" }
func (m *mockProvider) Models() []llm.ModelInfo { return nil }

// mockTool implements tools.Tool for testing.
type mockTool struct {
	name        string
	description string
	schema      json.RawMessage
	executeFn   func(ctx context.Context, params json.RawMessage) (*tools.Result, error)
}

func (t *mockTool) Name() string                   { return t.name }
func (t *mockTool) Description() string            { return t.description }
func (t *mockTool) Schema() json.RawMessage        { return t.schema }
func (t *mockTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t.executeFn != nil {
		return t.executeFn(ctx, params)
	}
	return tools.SuccessResult("ok"), nil
}

func TestNew(t *testing.T) {
	reg := tools.NewRegistry()
	p := &mockProvider{}

	a := New(p, reg)

	if a.maxIterations != defaultMaxIterations {
		t.Errorf("maxIterations = %d, want %d", a.maxIterations, defaultMaxIterations)
	}
	if a.permission == nil {
		t.Error("permission must not be nil after New()")
	}
	if a.provider != p {
		t.Error("provider not set correctly")
	}
	if a.tools != reg {
		t.Error("tools registry not set correctly")
	}
}

func TestWithOptions(t *testing.T) {
	reg := tools.NewRegistry()
	p := &mockProvider{}

	customPerm := NewPermission(WithMode(ModeBypass))

	a := New(p, reg,
		WithModel("claude-opus"),
		WithSystemPrompt("you are helpful"),
		WithMaxIterations(10),
		WithPermission(customPerm),
	)

	if a.model != "claude-opus" {
		t.Errorf("model = %q, want %q", a.model, "claude-opus")
	}
	if a.systemPrompt != "you are helpful" {
		t.Errorf("systemPrompt = %q, want %q", a.systemPrompt, "you are helpful")
	}
	if a.maxIterations != 10 {
		t.Errorf("maxIterations = %d, want %d", a.maxIterations, 10)
	}
	if a.permission != customPerm {
		t.Error("custom permission not set")
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"429 status", errors.New("request failed: 429 Too Many Requests"), true},
		{"500 status", errors.New("server error 500"), true},
		{"502 status", errors.New("502 bad gateway"), true},
		{"503 status", errors.New("503 service unavailable"), true},
		{"504 status", errors.New("upstream timeout 504"), true},
		{"rate limit lower", errors.New("rate limit exceeded"), true},
		{"rate_limit underscore", errors.New("error: rate_limit"), true},
		{"403 not retryable", errors.New("403 forbidden"), false},
		{"404 not retryable", errors.New("404 not found"), false},
		{"network error", errors.New("connection refused"), false},
		{"empty message", errors.New(""), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRetryable(tc.err)
			if got != tc.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestBuildToolDefs(t *testing.T) {
	reg := tools.NewRegistry()

	tool1 := &mockTool{
		name:        "read",
		description: "Read a file",
		schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}
	tool2 := &mockTool{
		name:        "write",
		description: "Write a file",
		schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`),
	}

	reg.Register(tool1)
	reg.Register(tool2)

	defs := buildToolDefs(reg)

	if len(defs) != 2 {
		t.Fatalf("len(defs) = %d, want 2", len(defs))
	}

	// Build a map for order-independent lookup.
	byName := make(map[string]llm.ToolDef, len(defs))
	for _, d := range defs {
		byName[d.Name] = d
	}

	for _, tool := range []tools.Tool{tool1, tool2} {
		def, ok := byName[tool.Name()]
		if !ok {
			t.Errorf("ToolDef for %q not found", tool.Name())
			continue
		}
		if def.Description != tool.Description() {
			t.Errorf("def.Description = %q, want %q", def.Description, tool.Description())
		}
		if string(def.InputSchema) != string(tool.Schema()) {
			t.Errorf("def.InputSchema = %s, want %s", def.InputSchema, tool.Schema())
		}
	}
}

// textOnlyStreamFn is a stream function that emits a single text event then done.
func textOnlyStreamFn(text string) func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	return func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
		cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: text})
		usage := llm.UsageStats{InputTokens: 10, OutputTokens: 5}
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &usage})
		return nil
	}
}

func TestRunNoToolCalls(t *testing.T) {
	reg := tools.NewRegistry()
	p := &mockProvider{
		streamFn: textOnlyStreamFn("Hello, world!"),
	}

	a := New(p, reg)
	initial := []llm.Message{llm.NewUserMessage("hi")}

	var received string
	result, err := a.Run(context.Background(), initial, func(s string) { received += s })
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if received != "Hello, world!" {
		t.Errorf("onText received %q, want %q", received, "Hello, world!")
	}

	// initial user message + assistant response = 2 messages
	if len(result.Messages) != 2 {
		t.Errorf("len(messages) = %d, want 2", len(result.Messages))
	}

	last := result.Messages[len(result.Messages)-1]
	if last.Role != llm.RoleAssistant {
		t.Errorf("last message role = %q, want %q", last.Role, llm.RoleAssistant)
	}
	if last.Text() != "Hello, world!" {
		t.Errorf("last message text = %q, want %q", last.Text(), "Hello, world!")
	}
}

func TestRunMaxIterations(t *testing.T) {
	reg := tools.NewRegistry()

	// Register a no-op tool so execution succeeds.
	reg.Register(&mockTool{
		name:        "loop_tool",
		description: "infinite loop tool",
		schema:      json.RawMessage(`{}`),
	})

	maxIter := 3
	callCount := 0
	// Stream always returns a tool call — the agent can never settle.
	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			callCount++
			toolID := fmt.Sprintf("tool_%d", callCount)
			cb(llm.StreamEvent{
				Type:     llm.EventToolUseStart,
				ToolCall: &llm.ToolUseEvent{ID: toolID, Name: "loop_tool"},
			})
			cb(llm.StreamEvent{
				Type:     llm.EventToolUseDone,
				ToolCall: &llm.ToolUseEvent{ID: toolID, Name: "loop_tool", Input: `{}`},
			})
			cb(llm.StreamEvent{Type: llm.EventDone})
			return nil
		},
	}

	a := New(p, reg, WithMaxIterations(maxIter))
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)

	// The implementation exhausts the iteration cap and returns the accumulated
	// messages without an error (the post-loop ErrMaxIterations check only fires
	// when the LAST message is an assistant message with pending tool calls, but
	// executeTools always appends a tool-result user message, so the last message
	// is always a user role after a complete iteration).
	if err != nil {
		t.Fatalf("unexpected error: %v (want nil — loop exhaustion returns success)", err)
	}
	if result == nil {
		t.Fatal("expected non-nil RunResult")
	}

	// The provider was called exactly maxIter times.
	if callCount != maxIter {
		t.Errorf("provider called %d times, want %d", callCount, maxIter)
	}

	// Message structure: 1 initial user + maxIter*(1 assistant + 1 tool_result).
	wantMessages := 1 + maxIter*2
	if len(result.Messages) != wantMessages {
		t.Errorf("len(messages) = %d, want %d", len(result.Messages), wantMessages)
	}
}

// TestRunErrMaxIterationsPath verifies that ErrMaxIterations IS returned when
// the last message after the loop is an assistant message with outstanding tool
// calls (i.e., executeTools was somehow not called on the final iteration).
// We simulate this by having the provider succeed on the last call with a tool
// call but making executeTools fail — which causes an early return before the
// tool_result is appended, so the test instead confirms the error propagation
// path and does NOT trigger ErrMaxIterations via that route.
//
// The canonical way to reach ErrMaxIterations: use maxIterations=0 so the loop
// body never executes and we pre-populate messages ending with an assistant
// tool-call message.
func TestRunErrMaxIterationsDirectCheck(t *testing.T) {
	reg := tools.NewRegistry()
	p := &mockProvider{}

	a := New(p, reg, WithMaxIterations(0))

	// Seed messages with a final assistant message that has a tool call.
	// With maxIterations=0 the loop never runs; the post-loop check fires.
	assistantWithTool := llm.Message{
		Role: llm.RoleAssistant,
		Content: []llm.ContentBlock{
			llm.ToolUseBlock{ID: "t1", Name: "some_tool", Input: []byte(`{}`)},
		},
	}
	initial := []llm.Message{
		llm.NewUserMessage("go"),
		assistantWithTool,
	}

	_, err := a.Run(context.Background(), initial, nil)
	if err == nil {
		t.Fatal("expected ErrMaxIterations, got nil")
	}
	if !errors.Is(err, core.ErrMaxIterations) {
		t.Errorf("error = %v, want wrapping core.ErrMaxIterations", err)
	}
}
