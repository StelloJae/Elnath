package onboarding

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLanguageModel_InitialSelection(t *testing.T) {
	m := NewLanguageModel(En)
	if m.SelectedLocale() != En {
		t.Errorf("expected En, got %q", m.SelectedLocale())
	}
}

func TestLanguageModel_InitialSelectionKo(t *testing.T) {
	m := NewLanguageModel(Ko)
	if m.SelectedLocale() != Ko {
		t.Errorf("expected Ko, got %q", m.SelectedLocale())
	}
}

func TestLanguageModel_NavigateDown(t *testing.T) {
	m := NewLanguageModel(En)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	lm := updated.(LanguageModel)
	if lm.SelectedLocale() != Ko {
		t.Errorf("after down, expected Ko, got %q", lm.SelectedLocale())
	}
}

func TestLanguageModel_EnterEmitsMsg(t *testing.T) {
	m := NewLanguageModel(En)
	// Navigate to Ko
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(LanguageModel)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter")
	}
	msg := cmd()
	done, ok := msg.(LanguageDoneMsg)
	if !ok {
		t.Fatalf("expected LanguageDoneMsg, got %T", msg)
	}
	if done.Locale != Ko {
		t.Errorf("expected Ko, got %q", done.Locale)
	}
}

func TestLanguageModel_EscEmitsBack(t *testing.T) {
	m := NewLanguageModel(En)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected cmd on esc")
	}
	msg := cmd()
	if _, ok := msg.(stepBackMsg); !ok {
		t.Fatalf("expected stepBackMsg, got %T", msg)
	}
}

func TestLanguageModel_ViewContainsTitle(t *testing.T) {
	m := NewLanguageModel(En)
	view := m.View()
	if !strings.Contains(view, "Language") {
		t.Error("view missing Language title")
	}
}
