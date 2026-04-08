package onboarding

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSummaryModel_InitialState(t *testing.T) {
	result := Result{
		APIKey:         "sk-ant-test1234567890",
		PermissionMode: "default",
		DataDir:        "/data",
		WikiDir:        "/wiki",
	}
	m := NewSummaryModel(En, result, false)

	if m.cursor != 0 {
		t.Errorf("expected cursor 0, got %d", m.cursor)
	}
	if m.quick {
		t.Error("expected quick=false")
	}
}

func TestSummaryModel_ConfirmEmitsDoneMsg(t *testing.T) {
	m := NewSummaryModel(En, Result{APIKey: "sk-test"}, false)
	m.cursor = 0 // confirm

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on confirm")
	}
	msg := cmd()
	if _, ok := msg.(SummaryDoneMsg); !ok {
		t.Errorf("expected SummaryDoneMsg, got %T", msg)
	}
}

func TestSummaryModel_EditEmitsEditMsg(t *testing.T) {
	m := NewSummaryModel(En, Result{APIKey: "sk-test"}, false)
	m.cursor = 1 // edit

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on edit")
	}
	msg := cmd()
	editMsg, ok := msg.(SummaryEditMsg)
	if !ok {
		t.Errorf("expected SummaryEditMsg, got %T", msg)
	}
	if editMsg.Step != StepAPIKey {
		t.Errorf("expected StepAPIKey, got %d", editMsg.Step)
	}
}

func TestSummaryModel_EscGoesBack(t *testing.T) {
	m := NewSummaryModel(En, Result{}, false)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd on esc")
	}
	msg := cmd()
	if _, ok := msg.(stepBackMsg); !ok {
		t.Errorf("expected stepBackMsg, got %T", msg)
	}
}

func TestSummaryModel_MaskedKey(t *testing.T) {
	tests := []struct {
		key      string
		contains string
	}{
		{"", "(not set)"},
		{"short", "••••••••"},
		{"sk-ant-api03-longkey1234", "1234"},
	}
	for _, tt := range tests {
		m := NewSummaryModel(En, Result{APIKey: tt.key}, false)
		view := m.View()
		if !strings.Contains(view, tt.contains) {
			t.Errorf("key=%q: expected view to contain %q", tt.key, tt.contains)
		}
	}
}

func TestSummaryModel_QuickPathHidesFullFields(t *testing.T) {
	result := Result{
		APIKey:         "sk-ant-test1234567890",
		PermissionMode: "default",
		DataDir:        "/data",
		WikiDir:        "/wiki",
		MCPServers:     []MCPSelection{{Name: "GitHub"}},
	}
	m := NewSummaryModel(En, result, true)
	view := m.View()

	if strings.Contains(view, "Permission") {
		t.Error("quick path should not show Permission")
	}
	if strings.Contains(view, "MCP") {
		t.Error("quick path should not show MCP")
	}
}

func TestSummaryModel_FullPathShowsAllFields(t *testing.T) {
	result := Result{
		APIKey:         "sk-ant-test1234567890",
		PermissionMode: "accept_edits",
		DataDir:        "/custom/data",
		WikiDir:        "/custom/wiki",
		MCPServers:     []MCPSelection{{Name: "GitHub"}, {Name: "Filesystem"}},
	}
	m := NewSummaryModel(En, result, false)
	view := m.View()

	if !strings.Contains(view, "Permission") {
		t.Error("full path should show Permission")
	}
	if !strings.Contains(view, "GitHub") {
		t.Error("full path should show MCP server names")
	}
	if !strings.Contains(view, "Filesystem") {
		t.Error("full path should show all MCP servers")
	}
}

func TestSummaryModel_CursorNavigation(t *testing.T) {
	m := NewSummaryModel(En, Result{}, false)

	// Move down
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(SummaryModel)
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 after down, got %d", m.cursor)
	}

	// Can't go past 1
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(SummaryModel)
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 (clamped), got %d", m.cursor)
	}

	// Move up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(SummaryModel)
	if m.cursor != 0 {
		t.Errorf("expected cursor 0 after up, got %d", m.cursor)
	}
}
