package agent

import (
	"testing"

	"github.com/stello/elnath/internal/llm"
)

func TestSessionNewAndAppend(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if s.ID == "" {
		t.Error("session ID must not be empty")
	}
	if len(s.Messages) != 0 {
		t.Errorf("new session should have no messages, got %d", len(s.Messages))
	}

	msgs := []llm.Message{
		llm.NewUserMessage("hello"),
		llm.NewAssistantMessage("world"),
	}
	for _, m := range msgs {
		if err := s.AppendMessage(m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	if len(s.Messages) != 2 {
		t.Fatalf("in-memory messages = %d, want 2", len(s.Messages))
	}

	// Reload from disk and verify roundtrip.
	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if loaded.ID != s.ID {
		t.Errorf("loaded ID = %q, want %q", loaded.ID, s.ID)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("loaded messages = %d, want 2", len(loaded.Messages))
	}

	for i, want := range msgs {
		got := loaded.Messages[i]
		if got.Role != want.Role {
			t.Errorf("messages[%d].Role = %q, want %q", i, got.Role, want.Role)
		}
		if got.Text() != want.Text() {
			t.Errorf("messages[%d].Text() = %q, want %q", i, got.Text(), want.Text())
		}
	}
}

func TestSessionAppendMessages(t *testing.T) {
	dir := t.TempDir()

	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	batch := []llm.Message{
		llm.NewUserMessage("first"),
		llm.NewAssistantMessage("second"),
		llm.NewUserMessage("third"),
	}
	if err := s.AppendMessages(batch); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	loaded, err := LoadSession(dir, s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(loaded.Messages) != 3 {
		t.Errorf("loaded messages = %d, want 3", len(loaded.Messages))
	}
}

func TestSessionFork(t *testing.T) {
	dir := t.TempDir()

	parent, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	msgs := []llm.Message{
		llm.NewUserMessage("parent msg 1"),
		llm.NewAssistantMessage("parent reply"),
	}
	if err := parent.AppendMessages(msgs); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	child, err := parent.Fork(dir)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	// Fork must produce a distinct session.
	if child.ID == parent.ID {
		t.Error("forked session must have a different ID")
	}

	// Child must contain all parent messages.
	if len(child.Messages) != len(parent.Messages) {
		t.Errorf("child messages = %d, want %d", len(child.Messages), len(parent.Messages))
	}
	for i := range msgs {
		if child.Messages[i].Text() != parent.Messages[i].Text() {
			t.Errorf("messages[%d] mismatch: child=%q parent=%q",
				i, child.Messages[i].Text(), parent.Messages[i].Text())
		}
	}

	// Verify the child file exists and is loadable independently.
	loaded, err := LoadSession(dir, child.ID)
	if err != nil {
		t.Fatalf("LoadSession(child): %v", err)
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("loaded child messages = %d, want 2", len(loaded.Messages))
	}

	// Adding a message to the child must not affect the parent.
	if err := child.AppendMessage(llm.NewUserMessage("child only")); err != nil {
		t.Fatalf("child AppendMessage: %v", err)
	}

	reloadedParent, err := LoadSession(dir, parent.ID)
	if err != nil {
		t.Fatalf("reload parent: %v", err)
	}
	if len(reloadedParent.Messages) != 2 {
		t.Errorf("parent messages after child append = %d, want 2 (unchanged)", len(reloadedParent.Messages))
	}
}

func TestLoadSessionNotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadSession(dir, "nonexistent-id")
	if err == nil {
		t.Error("LoadSession with nonexistent ID should return an error")
	}
}
