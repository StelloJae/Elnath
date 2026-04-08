package onboarding

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPermissionModel_InitialState(t *testing.T) {
	m := NewPermissionModel(En)
	if m.cursor != 0 {
		t.Errorf("expected cursor 0, got %d", m.cursor)
	}
}

func TestPermissionModel_CursorNavigation(t *testing.T) {
	m := NewPermissionModel(En)

	// Down
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(PermissionModel)
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 after down, got %d", m.cursor)
	}

	// Down again
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(PermissionModel)
	if m.cursor != 2 {
		t.Errorf("expected cursor 2, got %d", m.cursor)
	}

	// Up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(PermissionModel)
	if m.cursor != 1 {
		t.Errorf("expected cursor 1 after up, got %d", m.cursor)
	}
}

func TestPermissionModel_CursorBounds(t *testing.T) {
	m := NewPermissionModel(En)

	// Up at top — should stay at 0
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(PermissionModel)
	if m.cursor != 0 {
		t.Errorf("expected cursor 0 at top, got %d", m.cursor)
	}

	// Navigate to bottom
	for i := 0; i < 10; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = updated.(PermissionModel)
	}
	if m.cursor != len(permModes)-1 {
		t.Errorf("expected cursor %d at bottom, got %d", len(permModes)-1, m.cursor)
	}
}

func TestPermissionModel_EnterSelectsMode(t *testing.T) {
	tests := []struct {
		name     string
		cursor   int
		wantMode string
	}{
		{"default", 0, "default"},
		{"accept_edits", 1, "accept_edits"},
		{"plan", 2, "plan"},
		{"bypass", 3, "bypass"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewPermissionModel(En)
			m.cursor = tt.cursor

			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("expected cmd, got nil")
			}

			msg := cmd()
			done, ok := msg.(PermissionDoneMsg)
			if !ok {
				t.Fatalf("expected PermissionDoneMsg, got %T", msg)
			}
			if done.Mode != tt.wantMode {
				t.Errorf("expected mode %q, got %q", tt.wantMode, done.Mode)
			}
		})
	}
}

func TestPermissionModel_EscGoesBack(t *testing.T) {
	m := NewPermissionModel(En)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd on esc")
	}
	msg := cmd()
	if _, ok := msg.(stepBackMsg); !ok {
		t.Fatalf("expected stepBackMsg, got %T", msg)
	}
}

func TestPermissionModel_CtrlCQuits(t *testing.T) {
	m := NewPermissionModel(En)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("expected quit cmd on ctrl+c")
	}
}

func TestPermissionModel_ViewContainsModes(t *testing.T) {
	for _, locale := range []Locale{En, Ko} {
		t.Run(string(locale), func(t *testing.T) {
			m := NewPermissionModel(locale)
			view := m.View()
			if view == "" {
				t.Error("expected non-empty view")
			}
		})
	}
}

func TestPermissionModel_DefaultIsRecommended(t *testing.T) {
	if !permModes[0].recommended {
		t.Error("expected first mode (default) to be recommended")
	}
	for i := 1; i < len(permModes); i++ {
		if permModes[i].recommended {
			t.Errorf("expected mode %d (%s) to not be recommended", i, permModes[i].id)
		}
	}
}
