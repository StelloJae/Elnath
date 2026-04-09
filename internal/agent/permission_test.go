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
		{"bypass allows read_file", ModeBypass, "read_file", true},

		// ModePlan permits only read-only tools.
		{"plan allows read_file", ModePlan, "read_file", true},
		{"plan allows glob", ModePlan, "glob", true},
		{"plan allows grep", ModePlan, "grep", true},
		{"plan denies git", ModePlan, "git", false},
		{"plan denies bash", ModePlan, "bash", false},
		{"plan denies write_file", ModePlan, "write_file", false},
		{"plan denies arbitrary", ModePlan, "custom_tool", false},

		// ModeAcceptEdits permits read-only and edit tools (no prompter → allow for others).
		{"accept_edits allows read_file", ModeAcceptEdits, "read_file", true},
		{"accept_edits allows write_file (no prompter)", ModeAcceptEdits, "write_file", true},
		{"accept_edits allows edit_file (no prompter)", ModeAcceptEdits, "edit_file", true},
		{"accept_edits allows bash (no prompter)", ModeAcceptEdits, "bash", true}, // no prompter → allow

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

func TestPermissionWithActualToolNames(t *testing.T) {
	ctx := context.Background()

	// These test cases use the ACTUAL registered tool names, not the
	// stale names that were hardcoded in permission.go.
	cases := []struct {
		name     string
		mode     PermissionMode
		toolName string
		want     bool
	}{
		// ModePlan: read-only tools must be allowed.
		{"plan allows read_file", ModePlan, "read_file", true},
		{"plan allows glob", ModePlan, "glob", true},
		{"plan allows grep", ModePlan, "grep", true},
		{"plan allows web_fetch", ModePlan, "web_fetch", true},
		{"plan allows web_search", ModePlan, "web_search", true},
		{"plan allows wiki_search", ModePlan, "wiki_search", true},
		{"plan allows wiki_read", ModePlan, "wiki_read", true},
		{"plan allows conversation_search", ModePlan, "conversation_search", true},
		{"plan allows cross_project_search", ModePlan, "cross_project_search", true},
		{"plan allows cross_project_conversation_search", ModePlan, "cross_project_conversation_search", true},

		// ModePlan: write/edit/exec tools must be denied.
		{"plan denies write_file", ModePlan, "write_file", false},
		{"plan denies edit_file", ModePlan, "edit_file", false},
		{"plan denies bash", ModePlan, "bash", false},
		{"plan denies git", ModePlan, "git", false},
		{"plan denies wiki_write", ModePlan, "wiki_write", false},
		{"plan denies mcp tool", ModePlan, "mcp_some_tool", false},
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

func TestAcceptEditsAutoApprovesWithoutPrompter(t *testing.T) {
	ctx := context.Background()
	// Use a prompter that denies — if the tool is auto-approved,
	// the prompter should NOT be called at all.
	pr := &mockPrompter{answer: false}

	p := NewPermission(
		WithMode(ModeAcceptEdits),
		WithPrompter(pr),
	)

	autoApproved := []string{
		"read_file", "glob", "grep", "web_fetch", "web_search",
		"wiki_search", "wiki_read", "conversation_search",
		"cross_project_search", "cross_project_conversation_search",
		"write_file", "edit_file", "wiki_write",
	}

	for _, tool := range autoApproved {
		got, err := p.Check(ctx, tool, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Check(%q) error: %v", tool, err)
		}
		if !got {
			t.Errorf("Check(%q) = false, want true (auto-approved)", tool)
		}
	}

	if len(pr.calls) != 0 {
		t.Errorf("prompter was called %d times for auto-approved tools: %v", len(pr.calls), pr.calls)
	}
}

func TestAcceptEditsPromptsForNonEditTools(t *testing.T) {
	ctx := context.Background()
	pr := &mockPrompter{answer: false}

	p := NewPermission(
		WithMode(ModeAcceptEdits),
		WithPrompter(pr),
	)

	// bash, git, mcp_* are not read-only or edit — prompter must be called.
	for _, tool := range []string{"bash", "git", "mcp_some_tool"} {
		got, err := p.Check(ctx, tool, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("Check(%q) error: %v", tool, err)
		}
		if got {
			t.Errorf("Check(%q) = true, want false (prompter denies)", tool)
		}
	}

	if len(pr.calls) != 3 {
		t.Errorf("prompter called %d times, want 3; calls: %v", len(pr.calls), pr.calls)
	}
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

func TestDangerousBashBypassesAllowListAndPrompts(t *testing.T) {
	ctx := context.Background()
	pr := &mockPrompter{answer: false}

	p := NewPermission(
		WithMode(ModeDefault),
		WithAllowList("bash"),
		WithPrompter(pr),
	)

	got, err := p.Check(ctx, "bash", json.RawMessage(`{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("expected dangerous bash command to require prompt, not allowlist auto-approval")
	}
	if len(pr.calls) != 1 || pr.calls[0] != "bash" {
		t.Fatalf("prompter calls = %v, want [bash]", pr.calls)
	}
}

func TestDangerousBashDeniedWithoutPrompter(t *testing.T) {
	ctx := context.Background()

	p := NewPermission(WithMode(ModeDefault))

	got, err := p.Check(ctx, "bash", json.RawMessage(`{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("expected dangerous bash command to be denied without a prompter")
	}
}

func TestDangerousBashRedirectionBypassesAllowListAndPrompts(t *testing.T) {
	ctx := context.Background()
	pr := &mockPrompter{answer: false}

	p := NewPermission(
		WithMode(ModeDefault),
		WithAllowList("bash"),
		WithPrompter(pr),
	)

	got, err := p.Check(ctx, "bash", json.RawMessage(`{"command":"echo hi > /etc/passwd"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("expected dangerous bash redirection to require prompt, not allowlist auto-approval")
	}
	if len(pr.calls) != 1 || pr.calls[0] != "bash" {
		t.Fatalf("prompter calls = %v, want [bash]", pr.calls)
	}
}
