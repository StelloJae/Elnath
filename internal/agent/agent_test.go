package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	randv2 "math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agent/errorclass"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/userfacingerr"
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

func (m *mockProvider) Name() string            { return "mock" }
func (m *mockProvider) Models() []llm.ModelInfo { return nil }

type statusCodeErr struct {
	code int
	msg  string
}

func (e statusCodeErr) Error() string   { return e.msg }
func (e statusCodeErr) StatusCode() int { return e.code }

// mockTool implements tools.Tool for testing.
type mockTool struct {
	name        string
	description string
	schema      json.RawMessage
	safe        bool
	reversible  bool
	scope       tools.ToolScope
	cancelOnErr bool
	executeFn   func(ctx context.Context, params json.RawMessage) (*tools.Result, error)
}

func (t *mockTool) Name() string                           { return t.name }
func (t *mockTool) Description() string                    { return t.description }
func (t *mockTool) Schema() json.RawMessage                { return t.schema }
func (t *mockTool) IsConcurrencySafe(json.RawMessage) bool { return t.safe }
func (t *mockTool) Reversible() bool                       { return t.reversible }
func (t *mockTool) Scope(json.RawMessage) tools.ToolScope {
	if len(t.scope.ReadPaths) == 0 && len(t.scope.WritePaths) == 0 && !t.scope.Network && !t.scope.Persistent {
		return tools.ConservativeScope()
	}
	return t.scope
}
func (t *mockTool) ShouldCancelSiblingsOnError() bool { return t.cancelOnErr }
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

func TestStreamWithRetry_ClassifiesBeforeRetry(t *testing.T) {
	reg := tools.NewRegistry()
	streamCalls := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
			streamCalls++
			if streamCalls == 1 {
				return errors.New("RATE LIMIT exceeded")
			}
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "recovered"})
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 2, OutputTokens: 1}})
			return nil
		},
	}

	a := New(provider, reg)
	msg, finalReq, usage, err := a.streamWithRetry(context.Background(), llm.Request{
		Messages:  []llm.Message{llm.NewUserMessage("hello")},
		MaxTokens: defaultMaxTokens,
	}, nil)
	if err != nil {
		t.Fatalf("streamWithRetry: %v", err)
	}
	if streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", streamCalls)
	}
	if msg.Text() != "recovered" {
		t.Fatalf("msg.Text() = %q, want %q", msg.Text(), "recovered")
	}
	if usage.OutputTokens != 1 {
		t.Fatalf("usage.OutputTokens = %d, want 1", usage.OutputTokens)
	}
	if got := finalReq.Messages[len(finalReq.Messages)-1].Text(); got != "hello" {
		t.Fatalf("final request message = %q, want %q", got, "hello")
	}
}

func TestStreamWithRetry_ContextOverflowReturnsClassifiedError(t *testing.T) {
	reg := tools.NewRegistry()
	streamCalls := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
			streamCalls++
			return errors.New("context_length_exceeded")
		},
	}

	a := New(provider, reg)
	_, _, _, err := a.streamWithRetry(context.Background(), llm.Request{
		Messages:  []llm.Message{llm.NewUserMessage("hello")},
		MaxTokens: defaultMaxTokens,
	}, nil)
	if err == nil {
		t.Fatal("expected classified error, got nil")
	}
	if streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", streamCalls)
	}

	var classified *errorclass.ClassifiedError
	if !errors.As(err, &classified) {
		t.Fatalf("error type = %T, want ClassifiedError", err)
	}
	if classified.Category != errorclass.ContextOverflow {
		t.Fatalf("Category = %q, want %q", classified.Category, errorclass.ContextOverflow)
	}
	if !classified.Recovery.ShouldCompress {
		t.Fatal("ShouldCompress must be true")
	}
}

func TestExtractStatusCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"typed error", statusCodeErr{code: 503, msg: "typed"}, 503},
		{"http keyword", errors.New("openai: http 429: too many requests"), 429},
		{"paren status", errors.New("rate limit (429)"), 429},
		{"provider prefix", errors.New("ollama: 503"), 503},
		{"false positive", errors.New("file has 500 lines"), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractStatusCode(tt.err); got != tt.want {
				t.Fatalf("extractStatusCode(%q) = %d, want %d", tt.err, got, tt.want)
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

func streamMessages(messages ...llm.Message) func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
	call := 0
	return func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
		if call >= len(messages) {
			return fmt.Errorf("unexpected stream call %d", call+1)
		}
		msg := messages[call]
		call++

		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.TextBlock:
				if b.Text != "" {
					cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: b.Text})
				}
			case llm.ToolUseBlock:
				cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: b.ID, Name: b.Name}})
				cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: b.ID, Name: b.Name, Input: string(b.Input)}})
			}
		}

		usage := llm.UsageStats{InputTokens: 1, OutputTokens: 1}
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &usage})
		return nil
	}
}

func assistantMessage(text string, toolCalls ...llm.CompletedToolCall) llm.Message {
	return llm.BuildAssistantMessage([]string{text}, toolCalls)
}

func assertToolStats(t *testing.T, got []ToolStat, want []ToolStat) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(ToolStats) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Calls != want[i].Calls || got[i].Errors != want[i].Errors {
			t.Fatalf("ToolStats[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func assertToolTimeAtLeast(t *testing.T, stat ToolStat, wantMin time.Duration) {
	t.Helper()
	if stat.TotalTime < wantMin {
		t.Fatalf("ToolStat(%s).TotalTime = %s, want >= %s", stat.Name, stat.TotalTime, wantMin)
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
	result, err := a.Run(context.Background(), initial, event.OnTextToSink(func(s string) { received += s }))
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

func TestRunResult(t *testing.T) {
	t.Run("ToolStats", func(t *testing.T) {
		reg := tools.NewRegistry()
		bashCalls := 0
		reg.Register(&mockTool{
			name:        "bash",
			description: "shell",
			schema:      json.RawMessage(`{"type":"object"}`),
			executeFn: func(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
				time.Sleep(5 * time.Millisecond)
				bashCalls++
				if bashCalls == 2 {
					return nil, errors.New("boom")
				}
				return tools.SuccessResult("bash ok"), nil
			},
		})
		reg.Register(&mockTool{
			name:        "file",
			description: "file",
			schema:      json.RawMessage(`{"type":"object"}`),
			executeFn: func(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
				time.Sleep(5 * time.Millisecond)
				return tools.SuccessResult("file ok"), nil
			},
		})

		p := &mockProvider{streamFn: streamMessages(
			assistantMessage("",
				llm.CompletedToolCall{ID: "bash-1", Name: "bash", Input: `{}`},
				llm.CompletedToolCall{ID: "file-1", Name: "file", Input: `{}`},
			),
			assistantMessage("", llm.CompletedToolCall{ID: "bash-2", Name: "bash", Input: `{}`}),
			assistantMessage("done"),
		)}

		result, err := New(p, reg).Run(context.Background(), []llm.Message{llm.NewUserMessage("run")}, nil)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}

		assertToolStats(t, result.ToolStats, []ToolStat{
			{Name: "bash", Calls: 2, Errors: 1},
			{Name: "file", Calls: 1, Errors: 0},
		})
		assertToolTimeAtLeast(t, result.ToolStats[0], 10*time.Millisecond)
		assertToolTimeAtLeast(t, result.ToolStats[1], 5*time.Millisecond)
	})

	t.Run("FinishReason Stop", func(t *testing.T) {
		result, err := New(&mockProvider{streamFn: streamMessages(assistantMessage("all done"))}, tools.NewRegistry()).Run(
			context.Background(),
			[]llm.Message{llm.NewUserMessage("hi")},
			nil,
		)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if result.FinishReason != FinishReasonStop {
			t.Fatalf("FinishReason = %q, want %q", result.FinishReason, FinishReasonStop)
		}
		if result.Iterations != 1 {
			t.Fatalf("Iterations = %d, want 1", result.Iterations)
		}
	})

	t.Run("FinishReason BudgetExceeded", func(t *testing.T) {
		reg := tools.NewRegistry()
		reg.Register(&mockTool{name: "loop_tool", description: "loop", schema: json.RawMessage(`{"type":"object"}`)})
		p := &mockProvider{streamFn: streamMessages(
			assistantMessage("", llm.CompletedToolCall{ID: "tool-1", Name: "loop_tool", Input: `{}`}),
			assistantMessage("", llm.CompletedToolCall{ID: "tool-2", Name: "loop_tool", Input: `{}`}),
		)}

		result, err := New(p, reg, WithMaxIterations(2)).Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if result.FinishReason != FinishReasonBudgetExceeded {
			t.Fatalf("FinishReason = %q, want %q", result.FinishReason, FinishReasonBudgetExceeded)
		}
		if result.Iterations != 2 {
			t.Fatalf("Iterations = %d, want 2", result.Iterations)
		}
	})

	t.Run("FinishReason AckLoop", func(t *testing.T) {
		p := &mockProvider{streamFn: streamMessages(
			assistantMessage("I'll inspect the files."),
			assistantMessage("I'll inspect the files."),
			assistantMessage("I'll inspect the files."),
		)}

		result, err := New(p, tools.NewRegistry()).Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if result.FinishReason != FinishReasonAckLoop {
			t.Fatalf("FinishReason = %q, want %q", result.FinishReason, FinishReasonAckLoop)
		}
		if result.Iterations != 3 {
			t.Fatalf("Iterations = %d, want 3", result.Iterations)
		}
	})

	t.Run("ToolStats Parallel", func(t *testing.T) {
		reg := tools.NewRegistry()
		reg.Register(&mockTool{
			name:        "parallel",
			description: "parallel tool",
			schema:      json.RawMessage(`{"type":"object"}`),
			safe:        true,
			executeFn: func(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
				time.Sleep(10 * time.Millisecond)
				return tools.SuccessResult("ok"), nil
			},
		})

		calls := make([]llm.CompletedToolCall, 0, 8)
		for i := 0; i < 8; i++ {
			calls = append(calls, llm.CompletedToolCall{
				ID:    fmt.Sprintf("parallel-%d", i),
				Name:  "parallel",
				Input: `{}`,
			})
		}

		p := &mockProvider{streamFn: streamMessages(
			assistantMessage("", calls...),
			assistantMessage("done"),
		)}

		result, err := New(p, reg).Run(context.Background(), []llm.Message{llm.NewUserMessage("parallel")}, nil)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}

		assertToolStats(t, result.ToolStats, []ToolStat{{Name: "parallel", Calls: 8, Errors: 0}})
		assertToolTimeAtLeast(t, result.ToolStats[0], 60*time.Millisecond)
	})
}

func TestRunFallsBackToChatOnEmptyStream(t *testing.T) {
	reg := tools.NewRegistry()
	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 0}})
			return nil
		},
		chatFn: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{
				Content: "fallback answer",
				Usage:   llm.Usage{InputTokens: 11, OutputTokens: 5},
			}, nil
		},
	}

	a := New(p, reg)
	var received string
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hello")}, event.OnTextToSink(func(s string) {
		received += s
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(received, "fallback answer") {
		t.Fatalf("received = %q, want fallback answer", received)
	}
	last := result.Messages[len(result.Messages)-1]
	if last.Text() != "fallback answer" {
		t.Fatalf("last.Text() = %q, want fallback answer", last.Text())
	}
}

func TestRunRetriesOnEmptyStreamAndEmptyChatFallback(t *testing.T) {
	reg := tools.NewRegistry()
	streamCalls := 0
	chatCalls := 0
	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			streamCalls++
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10}})
			return nil
		},
		chatFn: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			chatCalls++
			if chatCalls < 3 {
				return &llm.ChatResponse{}, nil
			}
			return &llm.ChatResponse{
				Content: "recovered after empty responses",
				Usage:   llm.Usage{InputTokens: 10, OutputTokens: 4},
			}, nil
		},
	}

	a := New(p, reg)
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hello")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if streamCalls < 3 || chatCalls < 3 {
		t.Fatalf("expected retries, got streamCalls=%d chatCalls=%d", streamCalls, chatCalls)
	}
	last := result.Messages[len(result.Messages)-1]
	if last.Text() != "recovered after empty responses" {
		t.Fatalf("last.Text() = %q", last.Text())
	}
}

func TestRunAddsNudgeAfterEmptyResponse(t *testing.T) {
	reg := tools.NewRegistry()
	streamCalls := 0
	var seenNudge bool
	p := &mockProvider{
		streamFn: func(ctx context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			streamCalls++
			if len(req.Messages) > 1 && strings.Contains(req.Messages[len(req.Messages)-1].Text(), "empty response") {
				seenNudge = true
				cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "recovered with nudge"})
			}
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 4}})
			return nil
		},
		chatFn: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{}, nil
		},
	}

	a := New(p, reg)
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hello")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !seenNudge || streamCalls < 2 {
		t.Fatalf("expected nudged retry, seenNudge=%v streamCalls=%d", seenNudge, streamCalls)
	}
	last := result.Messages[len(result.Messages)-1]
	if last.Text() != "recovered with nudge" {
		t.Fatalf("last.Text() = %q", last.Text())
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

func TestRunEmptyResponseExhaustionEmitsELN120(t *testing.T) {
	reg := tools.NewRegistry()
	p := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1}})
			return nil
		},
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{}, nil
		},
	}

	a := New(p, reg)
	_, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("hello")}, nil)
	if err == nil {
		t.Fatal("expected error after retry exhaustion, got nil")
	}
	if !errors.Is(err, core.ErrProviderError) {
		t.Errorf("error chain must include core.ErrProviderError, got: %v", err)
	}
	var ufe *userfacingerr.UserFacingError
	if !errors.As(err, &ufe) {
		t.Fatalf("expected UserFacingError in chain, got %T: %v", err, err)
	}
	if ufe.Code() != userfacingerr.ELN120 {
		t.Fatalf("expected code %q, got %q (err: %v)", userfacingerr.ELN120, ufe.Code(), err)
	}
}

func toolResultTurnMessages(contents ...string) []llm.Message {
	messages := []llm.Message{llm.NewUserMessage("start")}
	for i, content := range contents {
		messages = append(messages, llm.BuildAssistantMessage(nil, []llm.CompletedToolCall{{
			ID:    fmt.Sprintf("tool-%d", i),
			Name:  "bash",
			Input: `{}`,
		}}))
		messages = llm.AppendToolResult(messages, fmt.Sprintf("tool-%d", i), content, false)
	}
	return messages
}

func toolResultContentForTurn(t *testing.T, messages []llm.Message, turn int) string {
	t.Helper()
	contents := toolResultContentsForTurn(t, messages, turn)
	return contents[0]
}

func toolResultContentsForTurn(t *testing.T, messages []llm.Message, turn int) []string {
	t.Helper()
	toolTurn := 0
	for _, msg := range messages {
		if msg.Role != llm.RoleUser {
			continue
		}
		var contents []string
		for _, block := range msg.Content {
			tr, ok := block.(llm.ToolResultBlock)
			if !ok {
				continue
			}
			contents = append(contents, tr.Content)
		}
		if len(contents) == 0 {
			continue
		}
		if toolTurn == turn {
			return contents
		}
		toolTurn++
	}
	t.Fatalf("tool-result turn %d not found", turn)
	return nil
}

func TestAttenuateHistoricalToolResults_NewTurnUnaffected(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("n", 30_000)
	messages := toolResultTurnMessages(content)

	attenuateHistoricalToolResults(messages, 0)

	if got := toolResultContentForTurn(t, messages, 0); got != content {
		t.Fatalf("current turn changed: got len=%d want len=%d", len(got), len(content))
	}
}

func TestAttenuateHistoricalToolResults_TwoTurnsAgoLimit10K(t *testing.T) {
	t.Parallel()

	messages := toolResultTurnMessages(
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 128),
		strings.Repeat("c", 128),
	)

	attenuateHistoricalToolResults(messages, 2)

	got := toolResultContentForTurn(t, messages, 0)
	if !strings.HasPrefix(got, attenuationMarker) {
		t.Fatalf("two-turn-old result missing marker prefix: %q", got[:min(len(got), 64)])
	}
	if len(got) > toolResultHistoryStage1Limit {
		t.Fatalf("two-turn-old result len=%d, want <= %d", len(got), toolResultHistoryStage1Limit)
	}
	if !strings.Contains(got, "original=30000") {
		t.Fatalf("two-turn-old result missing original size metadata: %q", got)
	}
}

func TestAttenuateHistoricalToolResults_ThreeTurnsAgoLimit2K(t *testing.T) {
	t.Parallel()

	messages := toolResultTurnMessages(
		strings.Repeat("a", 10_000),
		strings.Repeat("b", 128),
		strings.Repeat("c", 128),
		strings.Repeat("d", 128),
	)

	attenuateHistoricalToolResults(messages, 3)

	got := toolResultContentForTurn(t, messages, 0)
	if !strings.HasPrefix(got, attenuationMarker) {
		t.Fatalf("three-turn-old result missing marker prefix: %q", got[:min(len(got), 64)])
	}
	if len(got) > toolResultHistoryStage2Limit {
		t.Fatalf("three-turn-old result len=%d, want <= %d", len(got), toolResultHistoryStage2Limit)
	}
	if !strings.Contains(got, "original=10000") {
		t.Fatalf("three-turn-old result missing original size metadata: %q", got)
	}
}

func TestAttenuateHistoricalToolResults_FourPlusTurnsAgoPlaceholder(t *testing.T) {
	t.Parallel()

	stale := strings.Repeat("z", 512)
	messages := toolResultTurnMessages(
		stale,
		strings.Repeat("b", 128),
		strings.Repeat("c", 128),
		strings.Repeat("d", 128),
		strings.Repeat("e", 128),
	)

	firstTurn := &messages[2]
	firstTurn.Content = append(firstTurn.Content, llm.ToolResultBlock{ToolUseID: "tool-0b", Content: strings.Repeat("y", 256)})

	attenuateHistoricalToolResults(messages, 4)

	got := toolResultContentsForTurn(t, messages, 0)
	if len(got) != 2 {
		t.Fatalf("stale turn result count = %d, want 2", len(got))
	}
	if got[0] != "[stale tool result, turns=4, original=512]" {
		t.Fatalf("first stale result placeholder mismatch: %q", got[0])
	}
	if got[1] != "[stale tool result, turns=4, original=256]" {
		t.Fatalf("second stale result placeholder mismatch: %q", got[1])
	}
	if strings.Contains(got[0], stale[:64]) {
		t.Fatalf("stale result should not keep original payload: %q", got[0])
	}
}

func TestAttenuateHistoricalToolResults_Idempotent(t *testing.T) {
	t.Parallel()

	messages := toolResultTurnMessages(
		strings.Repeat("a", 30_000),
		strings.Repeat("b", 128),
		strings.Repeat("c", 128),
	)

	attenuateHistoricalToolResults(messages, 2)
	first := toolResultContentForTurn(t, messages, 0)
	attenuateHistoricalToolResults(messages, 2)
	second := toolResultContentForTurn(t, messages, 0)

	if second != first {
		t.Fatalf("attenuation must be idempotent\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestNextJitterDelay_MinimumRespected(t *testing.T) {
	t.Parallel()

	rng := randv2.New(randv2.NewPCG(1, 2))
	for _, current := range []time.Duration{0, retryBaseDelay / 2, retryBaseDelay, 5 * retryBaseDelay, retryMaxDelay} {
		for i := 0; i < 1000; i++ {
			got := nextJitterDelayWithRand(current, rng)
			if got < retryBaseDelay {
				t.Fatalf("nextJitterDelayWithRand(%s) = %s, want >= %s", current, got, retryBaseDelay)
			}
		}
	}
}

func TestNextJitterDelay_MaximumCapped(t *testing.T) {
	t.Parallel()

	rng := randv2.New(randv2.NewPCG(3, 4))
	for _, current := range []time.Duration{10 * retryBaseDelay, retryMaxDelay, 10 * retryMaxDelay} {
		for i := 0; i < 1000; i++ {
			got := nextJitterDelayWithRand(current, rng)
			if got > retryMaxDelay {
				t.Fatalf("nextJitterDelayWithRand(%s) = %s, want <= %s", current, got, retryMaxDelay)
			}
		}
	}
}

func TestNextJitterDelay_Distribution(t *testing.T) {
	t.Parallel()

	rng := randv2.New(randv2.NewPCG(5, 6))
	current := 5 * retryBaseDelay
	unique := map[time.Duration]struct{}{}
	var total time.Duration

	for i := 0; i < 1000; i++ {
		got := nextJitterDelayWithRand(current, rng)
		total += got
		unique[got] = struct{}{}
	}

	mean := total / 1000
	if mean <= 4*retryBaseDelay {
		t.Fatalf("mean jitter delay = %s, want well above deterministic 2x baseline %s", mean, 2*retryBaseDelay)
	}
	if len(unique) < 100 {
		t.Fatalf("distribution too narrow: got %d unique samples", len(unique))
	}
}

func TestNextJitterDelay_Reproducible(t *testing.T) {
	t.Parallel()

	left := randv2.New(randv2.NewPCG(7, 8))
	right := randv2.New(randv2.NewPCG(7, 8))
	leftDelay := retryBaseDelay
	rightDelay := retryBaseDelay

	for i := 0; i < 8; i++ {
		leftDelay = nextJitterDelayWithRand(leftDelay, left)
		rightDelay = nextJitterDelayWithRand(rightDelay, right)
		if leftDelay != rightDelay {
			t.Fatalf("step %d mismatch: left=%s right=%s", i, leftDelay, rightDelay)
		}
	}
}
