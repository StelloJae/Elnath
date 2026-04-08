package onboarding

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAPIKeyModel_InitialState(t *testing.T) {
	m := NewAPIKeyModel(En)
	if m.state != apiKeyInput {
		t.Errorf("expected apiKeyInput state, got %d", m.state)
	}
}

func TestAPIKeyModel_EmptyEnterSkips(t *testing.T) {
	m := NewAPIKeyModel(En)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter with empty input")
	}
	msg := cmd()
	done, ok := msg.(APIKeyDoneMsg)
	if !ok {
		t.Fatalf("expected APIKeyDoneMsg, got %T", msg)
	}
	if done.Key != "" {
		t.Errorf("expected empty key on skip, got %q", done.Key)
	}
}

func TestAPIKeyModel_ValidationMsg(t *testing.T) {
	m := NewAPIKeyModel(En)
	m.state = apiKeyValidating

	updated, _ := m.Update(apiKeyValidatedMsg{result: ValidationResult{Valid: true}})
	am := updated.(APIKeyModel)

	if am.state != apiKeyResult {
		t.Errorf("expected apiKeyResult state, got %d", am.state)
	}
	if am.validated == nil || !am.validated.Valid {
		t.Error("expected valid result")
	}
}

func TestAPIKeyModel_EnterAfterValidation(t *testing.T) {
	m := NewAPIKeyModel(En)
	m.state = apiKeyResult
	m.textInput.SetValue("sk-test-key")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter after validation")
	}
	msg := cmd()
	done, ok := msg.(APIKeyDoneMsg)
	if !ok {
		t.Fatalf("expected APIKeyDoneMsg, got %T", msg)
	}
	if done.Key != "sk-test-key" {
		t.Errorf("expected sk-test-key, got %q", done.Key)
	}
}

func TestAPIKeyModel_EscGoesBack(t *testing.T) {
	m := NewAPIKeyModel(En)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected cmd on esc")
	}
	msg := cmd()
	if _, ok := msg.(stepBackMsg); !ok {
		t.Fatalf("expected stepBackMsg, got %T", msg)
	}
}

func TestAPIKeyModel_ViewContainsTitle(t *testing.T) {
	m := NewAPIKeyModel(En)
	view := m.View()
	if !strings.Contains(view, "API Key") {
		t.Error("view missing API Key title")
	}
}

func TestAPIKeyModel_ViewKorean(t *testing.T) {
	m := NewAPIKeyModel(Ko)
	view := m.View()
	if !strings.Contains(view, "API 키 설정") {
		t.Error("view missing Korean API key title")
	}
}
