package prompt

import (
	"context"
	"regexp"
	"strings"
	testing "testing"
)

func TestSelfStateNodeRendersOperationalState(t *testing.T) {
	t.Parallel()

	got, err := NewSelfStateNode(85).Render(context.Background(), &RenderState{
		SessionID:    "sess-123",
		MessageCount: 7,
		DaemonMode:   false,
		WorkDir:      "/tmp/project",
	})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	for _, want := range []string{
		"Operational state:",
		"- Session: sess-123",
		"- Messages in conversation: 7",
		"- Mode: interactive",
		"- Working directory: /tmp/project",
		"- Current time: ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
	if !regexp.MustCompile(`(?m)- Current time: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(got) {
		t.Fatalf("Render = %q, want RFC3339 UTC timestamp", got)
	}
}

func TestSelfStateNodeUsesNewSessionPlaceholder(t *testing.T) {
	t.Parallel()

	got, err := NewSelfStateNode(85).Render(context.Background(), &RenderState{MessageCount: 1})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "- Session: (new)") {
		t.Fatalf("Render = %q, want new session placeholder", got)
	}
	if !strings.Contains(got, "- Working directory: (none)") {
		t.Fatalf("Render = %q, want empty workdir placeholder", got)
	}
}

func TestSelfStateNodePrefersSessionWorkDir(t *testing.T) {
	t.Parallel()

	got, err := NewSelfStateNode(85).Render(context.Background(), &RenderState{
		SessionID:      "sess-iso",
		WorkDir:        "/shared/root",
		SessionWorkDir: "/shared/root/sessions/sess-iso",
	})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "- Working directory: /shared/root/sessions/sess-iso") {
		t.Fatalf("Render = %q, want session workdir advertised", got)
	}
	if strings.Contains(got, "- Working directory: /shared/root\n") {
		t.Fatalf("Render = %q, root WorkDir leaked despite SessionWorkDir override", got)
	}
}

func TestSelfStateNodeDaemonMode(t *testing.T) {
	t.Parallel()

	got, err := NewSelfStateNode(85).Render(context.Background(), &RenderState{DaemonMode: true})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(got, "- Mode: daemon") {
		t.Fatalf("Render = %q, want daemon mode", got)
	}
}

func TestSelfStateNodeNilState(t *testing.T) {
	t.Parallel()

	got, err := NewSelfStateNode(85).Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestSelfStateNodeNameAndPriority(t *testing.T) {
	t.Parallel()

	node := NewSelfStateNode(85)
	if got := node.Name(); got != "self_state" {
		t.Fatalf("Name = %q, want %q", got, "self_state")
	}
	if got := node.Priority(); got != 85 {
		t.Fatalf("Priority = %d, want 85", got)
	}
}
