package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agent/errorclass"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

// stubHook is a test Hook that records calls and returns configured results.
type stubHook struct {
	preCalls  []string
	postCalls []string
	preResult HookResult
	preErr    error
	postErr   error
}

func (h *stubHook) PreToolUse(_ context.Context, toolName string, _ json.RawMessage) (HookResult, error) {
	h.preCalls = append(h.preCalls, toolName)
	return h.preResult, h.preErr
}

func (h *stubHook) PostToolUse(_ context.Context, toolName string, _ json.RawMessage, _ *tools.Result) error {
	h.postCalls = append(h.postCalls, toolName)
	return h.postErr
}

type lifecycleHook struct {
	stubHook
	preRequests      []llm.Request
	postRequests     []llm.Request
	postResponses    []llm.ChatResponse
	postUsages       []llm.UsageStats
	compressionCalls [][2]int
	iterationCalls   [][2]int
	mutatePreRequest func(*llm.Request)
	preLLMErr        error
	postLLMErr       error
	compressionErr   error
	iterationErr     error
}

func (h *lifecycleHook) PreLLMCall(_ context.Context, req *llm.Request) error {
	h.preRequests = append(h.preRequests, *req)
	if h.mutatePreRequest != nil {
		h.mutatePreRequest(req)
	}
	return h.preLLMErr
}

func (h *lifecycleHook) PostLLMCall(_ context.Context, req llm.Request, resp llm.ChatResponse, usage llm.UsageStats) error {
	h.postRequests = append(h.postRequests, req)
	h.postResponses = append(h.postResponses, resp)
	h.postUsages = append(h.postUsages, usage)
	return h.postLLMErr
}

func (h *lifecycleHook) OnCompression(_ context.Context, beforeCount, afterCount int) error {
	h.compressionCalls = append(h.compressionCalls, [2]int{beforeCount, afterCount})
	return h.compressionErr
}

func (h *lifecycleHook) OnIterationStart(_ context.Context, iteration, maxIterations int) error {
	h.iterationCalls = append(h.iterationCalls, [2]int{iteration, maxIterations})
	return h.iterationErr
}

type compressionOnlyHook struct {
	calls [][2]int
}

func (h *compressionOnlyHook) OnCompression(_ context.Context, beforeCount, afterCount int) error {
	h.calls = append(h.calls, [2]int{beforeCount, afterCount})
	return nil
}

type errorObserverHook struct {
	classified []errorclass.ClassifiedError
	err        error
}

func (h *errorObserverHook) OnClassifiedError(_ context.Context, classified errorclass.ClassifiedError) error {
	h.classified = append(h.classified, classified)
	return h.err
}

func TestHookRegistryPreToolUse_Allow(t *testing.T) {
	reg := NewHookRegistry()
	h := &stubHook{preResult: HookResult{Action: HookAllow}}
	reg.Add(h)

	result, err := reg.RunPreToolUse(context.Background(), "bash", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != HookAllow {
		t.Fatalf("expected HookAllow, got %v", result.Action)
	}
	if len(h.preCalls) != 1 || h.preCalls[0] != "bash" {
		t.Fatalf("expected 1 call with 'bash', got %v", h.preCalls)
	}
}

func TestHookRegistryPreToolUse_Deny(t *testing.T) {
	reg := NewHookRegistry()
	deny := &stubHook{preResult: HookResult{Action: HookDeny, Message: "blocked"}}
	allow := &stubHook{preResult: HookResult{Action: HookAllow}}
	reg.Add(deny)
	reg.Add(allow)

	result, err := reg.RunPreToolUse(context.Background(), "bash", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != HookDeny {
		t.Fatal("expected HookDeny")
	}
	if result.Message != "blocked" {
		t.Fatalf("expected 'blocked', got %q", result.Message)
	}
	// Second hook should NOT have been called.
	if len(allow.preCalls) != 0 {
		t.Fatal("second hook should not be called after deny")
	}
}

func TestHookRegistryPostToolUse(t *testing.T) {
	reg := NewHookRegistry()
	h1 := &stubHook{}
	h2 := &stubHook{}
	reg.Add(h1)
	reg.Add(h2)

	result := &tools.Result{Output: "ok", IsError: false}
	err := reg.RunPostToolUse(context.Background(), "read", nil, result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h1.postCalls) != 1 || len(h2.postCalls) != 1 {
		t.Fatal("both hooks should be called")
	}
}

func TestHookRegistryOnStop(t *testing.T) {
	reg := NewHookRegistry()
	called := false
	reg.AddOnStop(func(_ context.Context) error {
		called = true
		return nil
	})

	if err := reg.RunOnStop(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("onStop should have been called")
	}
}

func TestHookRegistryEmpty(t *testing.T) {
	reg := NewHookRegistry()

	result, err := reg.RunPreToolUse(context.Background(), "bash", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != HookAllow {
		t.Fatal("empty registry should allow")
	}

	err = reg.RunPostToolUse(context.Background(), "bash", nil, &tools.Result{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLLMHookPreCall(t *testing.T) {
	reg := tools.NewRegistry()
	hooks := NewHookRegistry()
	hook := &lifecycleHook{
		mutatePreRequest: func(req *llm.Request) {
			req.System = "hooked system"
		},
	}
	hooks.Add(hook)

	provider := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			if req.System != "hooked system" {
				t.Fatalf("req.System = %q, want %q", req.System, "hooked system")
			}
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "ok"})
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
			return nil
		},
	}

	_, err := New(provider, reg, WithHooks(hooks)).Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hook.preRequests) != 1 {
		t.Fatalf("PreLLMCall count = %d, want 1", len(hook.preRequests))
	}
	last := hook.preRequests[0].Messages[len(hook.preRequests[0].Messages)-1]
	if last.Text() != "hi" {
		t.Fatalf("last request message = %q, want %q", last.Text(), "hi")
	}
}

func TestLLMHookPostCall(t *testing.T) {
	reg := tools.NewRegistry()
	hooks := NewHookRegistry()
	hook := &lifecycleHook{}
	hooks.Add(hook)

	provider := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 3, OutputTokens: 5}})
			return nil
		},
	}

	_, err := New(provider, reg, WithHooks(hooks)).Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hook.postResponses) != 1 {
		t.Fatalf("PostLLMCall count = %d, want 1", len(hook.postResponses))
	}
	if hook.postResponses[0].Content != "done" {
		t.Fatalf("response content = %q, want %q", hook.postResponses[0].Content, "done")
	}
	if hook.postUsages[0].OutputTokens != 5 {
		t.Fatalf("usage.OutputTokens = %d, want 5", hook.postUsages[0].OutputTokens)
	}
}

func TestCompressionHookFires(t *testing.T) {
	reg := NewHookRegistry()
	hook := &lifecycleHook{}
	reg.Add(hook)

	if err := reg.RunOnCompression(context.Background(), 12, 7); err != nil {
		t.Fatalf("RunOnCompression: %v", err)
	}
	if len(hook.compressionCalls) != 1 {
		t.Fatalf("OnCompression count = %d, want 1", len(hook.compressionCalls))
	}
	if hook.compressionCalls[0] != [2]int{12, 7} {
		t.Fatalf("OnCompression args = %v, want [12 7]", hook.compressionCalls[0])
	}
}

func TestIterationHookFires(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name:        "loop_tool",
		description: "loop",
		schema:      json.RawMessage(`{"type":"object"}`),
	})

	hooks := NewHookRegistry()
	hook := &lifecycleHook{}
	hooks.Add(hook)

	provider := &mockProvider{streamFn: streamMessages(
		assistantMessage("", llm.CompletedToolCall{ID: "tool-1", Name: "loop_tool", Input: `{}`}),
		assistantMessage("", llm.CompletedToolCall{ID: "tool-2", Name: "loop_tool", Input: `{}`}),
		assistantMessage("done"),
	)}

	_, err := New(provider, reg, WithHooks(hooks), WithMaxIterations(5)).Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := [][2]int{{1, 5}, {2, 5}, {3, 5}}
	if len(hook.iterationCalls) != len(want) {
		t.Fatalf("iteration call count = %d, want %d", len(hook.iterationCalls), len(want))
	}
	for i := range want {
		if hook.iterationCalls[i] != want[i] {
			t.Fatalf("iterationCalls[%d] = %v, want %v", i, hook.iterationCalls[i], want[i])
		}
	}
}

func TestErrorObserverHookFires(t *testing.T) {
	reg := tools.NewRegistry()
	hooks := NewHookRegistry()
	hook := &errorObserverHook{}
	hooks.Add(hook)

	provider := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
			return errors.New("context_length_exceeded")
		},
	}

	a := New(provider, reg, WithHooks(hooks))
	_, _, _, err := a.streamWithRetry(context.Background(), llm.Request{
		Messages:  []llm.Message{llm.NewUserMessage("hello")},
		MaxTokens: defaultMaxTokens,
	}, nil)
	if err == nil {
		t.Fatal("expected classified error, got nil")
	}
	if len(hook.classified) != 1 {
		t.Fatalf("classified count = %d, want 1", len(hook.classified))
	}
	if hook.classified[0].Category != errorclass.ContextOverflow {
		t.Fatalf("Category = %q, want %q", hook.classified[0].Category, errorclass.ContextOverflow)
	}
}

func TestPartialHookInterface(t *testing.T) {
	reg := NewHookRegistry()
	reg.Add(&stubHook{preResult: HookResult{Action: HookAllow}})

	if err := reg.RunPreLLMCall(context.Background(), &llm.Request{System: "test"}); err != nil {
		t.Fatalf("RunPreLLMCall: %v", err)
	}
	if err := reg.RunPostLLMCall(context.Background(), llm.Request{}, llm.ChatResponse{Content: "ok"}, llm.UsageStats{}); err != nil {
		t.Fatalf("RunPostLLMCall: %v", err)
	}
}

func TestPreLLMCallErrorAborts(t *testing.T) {
	reg := tools.NewRegistry()
	hooks := NewHookRegistry()
	hooks.Add(&lifecycleHook{preLLMErr: errors.New("stop")})

	streamCalls := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
			streamCalls++
			return nil
		},
	}

	_, err := New(provider, reg, WithHooks(hooks)).Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	if err == nil {
		t.Fatal("expected Run error, got nil")
	}
	if streamCalls != 0 {
		t.Fatalf("provider stream calls = %d, want 0", streamCalls)
	}
}

func TestLLMHookPostCallUsesSuccessfulRetryRequest(t *testing.T) {
	reg := tools.NewRegistry()
	hooks := NewHookRegistry()
	hook := &lifecycleHook{}
	hooks.Add(hook)

	streamCalls := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			streamCalls++
			if streamCalls == 2 && !strings.Contains(req.Messages[len(req.Messages)-1].Text(), "empty response") {
				t.Fatalf("retry request missing empty-response nudge: %q", req.Messages[len(req.Messages)-1].Text())
			}
			if streamCalls == 1 {
				cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1}})
				return nil
			}
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "recovered"})
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 2, OutputTokens: 1}})
			return nil
		},
		chatFn: func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{}, nil
		},
	}

	_, err := New(provider, reg, WithHooks(hooks)).Run(context.Background(), []llm.Message{llm.NewUserMessage("hi")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hook.postRequests) != 1 {
		t.Fatalf("PostLLMCall count = %d, want 1", len(hook.postRequests))
	}
	last := hook.postRequests[0].Messages[len(hook.postRequests[0].Messages)-1].Text()
	if !strings.Contains(last, "empty response") {
		t.Fatalf("post-hook request missing retry nudge: %q", last)
	}
}

func TestLifecycleOnlyHookCanRegister(t *testing.T) {
	reg := NewHookRegistry()
	hook := &compressionOnlyHook{}
	reg.Add(hook)

	result, err := reg.RunPreToolUse(context.Background(), "bash", nil)
	if err != nil {
		t.Fatalf("RunPreToolUse: %v", err)
	}
	if result.Action != HookAllow {
		t.Fatalf("RunPreToolUse action = %v, want HookAllow", result.Action)
	}
	if err := reg.RunOnCompression(context.Background(), 3, 1); err != nil {
		t.Fatalf("RunOnCompression: %v", err)
	}
	if len(hook.calls) != 1 || hook.calls[0] != [2]int{3, 1} {
		t.Fatalf("compression calls = %v, want [[3 1]]", hook.calls)
	}
}

func TestAddPanicsOnInvalidHook(t *testing.T) {
	reg := NewHookRegistry()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when adding a value that implements no hook interface")
		}
	}()
	reg.Add(struct{}{})
}

func TestCommandHookMatching(t *testing.T) {
	tests := []struct {
		matcher  string
		toolName string
		want     bool
	}{
		{"*", "bash", true},
		{"", "bash", true},
		{"bash", "bash", true},
		{"bash", "read", false},
		{"write_*", "write_file", true},
		{"write_*", "read_file", false},
	}

	for _, tt := range tests {
		h := &CommandHook{Matcher: tt.matcher}
		if got := h.matches(tt.toolName); got != tt.want {
			t.Errorf("matcher=%q tool=%q: got %v, want %v", tt.matcher, tt.toolName, got, tt.want)
		}
	}
}

func TestCommandHookPreToolUse_Allow(t *testing.T) {
	h := &CommandHook{
		Matcher: "*",
		PreCmd:  "true", // exit 0
	}

	result, err := h.PreToolUse(context.Background(), "bash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != HookAllow {
		t.Fatal("expected allow")
	}
}

func TestCommandHookPreToolUse_Deny(t *testing.T) {
	h := &CommandHook{
		Matcher: "*",
		PreCmd:  "echo 'forbidden' && exit 1",
	}

	result, err := h.PreToolUse(context.Background(), "bash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != HookDeny {
		t.Fatal("expected deny")
	}
}

func TestCommandHookPreToolUse_SkipNoMatch(t *testing.T) {
	h := &CommandHook{
		Matcher: "bash",
		PreCmd:  "exit 1",
	}

	result, err := h.PreToolUse(context.Background(), "read", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != HookAllow {
		t.Fatal("non-matching tool should be allowed")
	}
}

func TestCommandHookPostToolUse(t *testing.T) {
	h := &CommandHook{
		Matcher: "*",
		PostCmd: "true",
	}

	err := h.PostToolUse(context.Background(), "bash", json.RawMessage(`{}`), &tools.Result{Output: "ok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
