package onboarding

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDirectoryModel_InitialFocus(t *testing.T) {
	m := NewDirectoryModel(En)
	if m.focused != dirFieldData {
		t.Errorf("expected data field focused initially, got %d", m.focused)
	}
}

func TestDirectoryModel_TabSwitchesFields(t *testing.T) {
	m := NewDirectoryModel(En)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	dm := updated.(DirectoryModel)
	if dm.focused != dirFieldWiki {
		t.Errorf("expected wiki field after tab, got %d", dm.focused)
	}
}

func TestDirectoryModel_ShiftTabGoesBack(t *testing.T) {
	m := NewDirectoryModel(En)
	// Go to wiki field first.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	dm := updated.(DirectoryModel)

	updated, _ = dm.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	dm = updated.(DirectoryModel)
	if dm.focused != dirFieldData {
		t.Errorf("expected data field after shift+tab, got %d", dm.focused)
	}
}

func TestDirectoryModel_EnterOnDataFieldMovesToWiki(t *testing.T) {
	m := NewDirectoryModel(En)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	dm := updated.(DirectoryModel)
	if dm.focused != dirFieldWiki {
		t.Errorf("expected wiki field after enter on data, got %d", dm.focused)
	}
}

func TestDirectoryModel_EnterOnWikiEmitsDone(t *testing.T) {
	m := NewDirectoryModel(En)
	// Move to wiki field.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	dm := updated.(DirectoryModel)

	_, cmd := dm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter from wiki field")
	}
	msg := cmd()
	done, ok := msg.(DirectoryDoneMsg)
	if !ok {
		t.Fatalf("expected DirectoryDoneMsg, got %T", msg)
	}
	// Empty input uses defaults.
	if done.DataDir == "" {
		t.Error("expected non-empty DataDir default")
	}
	if done.WikiDir == "" {
		t.Error("expected non-empty WikiDir default")
	}
}

func TestDirectoryModel_EscGoesBack(t *testing.T) {
	m := NewDirectoryModel(En)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected cmd on esc")
	}
	msg := cmd()
	if _, ok := msg.(stepBackMsg); !ok {
		t.Fatalf("expected stepBackMsg, got %T", msg)
	}
}

func TestDirectoryModel_ViewContainsTitle(t *testing.T) {
	m := NewDirectoryModel(En)
	view := m.View()
	if !strings.Contains(view, "Directory") {
		t.Error("view missing Directory title")
	}
}
