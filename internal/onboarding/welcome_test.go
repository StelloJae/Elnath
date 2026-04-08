package onboarding

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWelcomeModel_InitialState(t *testing.T) {
	m := NewWelcomeModel(En, "0.3.0")
	if m.SelectedPath() != PathQuick {
		t.Errorf("expected initial selection PathQuick, got %q", m.SelectedPath())
	}
}

func TestWelcomeModel_NavigateDown(t *testing.T) {
	m := NewWelcomeModel(En, "0.3.0")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	wm := updated.(WelcomeModel)
	if wm.SelectedPath() != PathFull {
		t.Errorf("after down, expected PathFull, got %q", wm.SelectedPath())
	}
}

func TestWelcomeModel_NavigateUpAtTop(t *testing.T) {
	m := NewWelcomeModel(En, "0.3.0")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	wm := updated.(WelcomeModel)
	if wm.SelectedPath() != PathQuick {
		t.Errorf("up at top should stay at PathQuick, got %q", wm.SelectedPath())
	}
}

func TestWelcomeModel_EnterEmitsMsg(t *testing.T) {
	m := NewWelcomeModel(En, "0.3.0")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter, got nil")
	}
	msg := cmd()
	done, ok := msg.(WelcomeDoneMsg)
	if !ok {
		t.Fatalf("expected WelcomeDoneMsg, got %T", msg)
	}
	if done.Path != PathQuick {
		t.Errorf("expected PathQuick, got %q", done.Path)
	}
}

func TestWelcomeModel_ViewContainsTitle(t *testing.T) {
	m := NewWelcomeModel(En, "0.3.0")
	view := m.View()
	if !strings.Contains(view, "Welcome to Elnath") {
		t.Error("view missing English title")
	}
}

func TestWelcomeModel_ViewKorean(t *testing.T) {
	m := NewWelcomeModel(Ko, "0.3.0")
	view := m.View()
	if !strings.Contains(view, "Elnath에 오신 것을 환영합니다") {
		t.Error("view missing Korean title")
	}
}

func TestWelcomeModel_ViewShowsVersion(t *testing.T) {
	m := NewWelcomeModel(En, "1.2.3")
	view := m.View()
	if !strings.Contains(view, "v1.2.3") {
		t.Error("view missing version string")
	}
}
