package agent

import (
	"context"
	"encoding/json"
	"testing"

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
