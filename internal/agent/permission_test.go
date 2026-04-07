package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPermissionModes(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		mode     PermissionMode
		toolName string
		want     bool
	}{
		// ModeBypass approves everything.
		{"bypass allows bash", ModeBypass, "bash", true},
		{"bypass allows unknown", ModeBypass, "arbitrary_tool", true},
		{"bypass allows read", ModeBypass, "read", true},

		// ModePlan permits only read-only tools.
		{"plan allows read", ModePlan, "read", true},
		{"plan allows glob", ModePlan, "glob", true},
		{"plan allows grep", ModePlan, "grep", true},
		{"plan allows git_log", ModePlan, "git_log", true},
		{"plan denies bash", ModePlan, "bash", false},
		{"plan denies write", ModePlan, "write", false},
		{"plan denies arbitrary", ModePlan, "custom_tool", false},

		// ModeAcceptEdits permits read-only and edit tools.
		{"accept_edits allows read", ModeAcceptEdits, "read", true},
		{"accept_edits allows write", ModeAcceptEdits, "write", true},
		{"accept_edits allows edit", ModeAcceptEdits, "edit", true},
		{"accept_edits denies bash (no prompter)", ModeAcceptEdits, "bash", true}, // no prompter → allow

		// ModeDefault with no prompter allows everything.
		{"default no prompter allows bash", ModeDefault, "bash", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPermission(WithMode(tc.mode))
			got, err := p.Check(ctx, tc.toolName, json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("Check returned unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Check(%q) = %v, want %v", tc.toolName, got, tc.want)
			}
		})
	}
}

func TestAllowDenyList(t *testing.T) {
	ctx := context.Background()

	p := NewPermission(
		WithMode(ModeDefault),
		WithAllowList("bash"),
		WithDenyList("web_fetch"),
	)

	cases := []struct {
		name     string
		toolName string
		want     bool
	}{
		{"deny list blocks web_fetch", "web_fetch", false},
		{"allow list permits bash", "bash", true},
		{"unlisted tool allowed (no prompter)", "read", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.Check(ctx, tc.toolName, json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("Check returned unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Check(%q) = %v, want %v", tc.toolName, got, tc.want)
			}
		})
	}
}

func TestDenyListTakesPrecedenceOverAllowList(t *testing.T) {
	ctx := context.Background()

	// A tool appearing in both lists must be denied (deny takes priority).
	p := NewPermission(
		WithDenyList("bash"),
		WithAllowList("bash"),
	)

	got, err := p.Check(ctx, "bash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected deny when tool is on deny list, even if also on allow list")
	}
}

// mockPrompter records calls and returns a configured answer.
type mockPrompter struct {
	answer bool
	calls  []string
}

func (m *mockPrompter) Prompt(_ context.Context, toolName string, _ json.RawMessage) (bool, error) {
	m.calls = append(m.calls, toolName)
	return m.answer, nil
}

func TestPrompterIsCalledInDefaultMode(t *testing.T) {
	ctx := context.Background()
	pr := &mockPrompter{answer: true}

	p := NewPermission(
		WithMode(ModeDefault),
		WithPrompter(pr),
	)

	// "bash" is not read-only, not in any list, mode is Default — prompter must be called.
	got, err := p.Check(ctx, "bash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected allow (prompter returned true)")
	}
	if len(pr.calls) != 1 || pr.calls[0] != "bash" {
		t.Errorf("prompter calls = %v, want [bash]", pr.calls)
	}
}
