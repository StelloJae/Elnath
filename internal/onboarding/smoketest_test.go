package onboarding

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSmokeTestModel_NoKeySkips(t *testing.T) {
	m := NewSmokeTestModel(En, "")
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected init cmd")
	}
	msg := cmd()
	resultMsg, ok := msg.(smokeTestResultMsg)
	if !ok {
		t.Fatalf("expected smokeTestResultMsg, got %T", msg)
	}
	if resultMsg.success {
		t.Error("expected failure for empty key")
	}
}

func TestSmokeTestModel_SkippedState(t *testing.T) {
	m := NewSmokeTestModel(En, "")
	// Simulate the skip result
	updated, _ := m.Update(smokeTestResultMsg{success: false, err: nil})
	m = updated.(SmokeTestModel)

	if m.state != smokeTestSkipped {
		t.Errorf("expected smokeTestSkipped, got %d", m.state)
	}

	view := m.View()
	if !strings.Contains(view, "Skipping") {
		t.Error("expected skip message in view")
	}
}

func TestSmokeTestModel_PassedState(t *testing.T) {
	m := NewSmokeTestModel(En, "sk-test")
	updated, _ := m.Update(smokeTestResultMsg{success: true, response: "Hello!"})
	m = updated.(SmokeTestModel)

	if m.state != smokeTestPassed {
		t.Errorf("expected smokeTestPassed, got %d", m.state)
	}

	view := m.View()
	if !strings.Contains(view, "working") {
		t.Error("expected success message in view")
	}
}

func TestSmokeTestModel_FailedState(t *testing.T) {
	m := NewSmokeTestModel(En, "sk-bad-key")
	updated, _ := m.Update(smokeTestResultMsg{success: false, err: errTest("auth failed")})
	m = updated.(SmokeTestModel)

	if m.state != smokeTestFailed {
		t.Errorf("expected smokeTestFailed, got %d", m.state)
	}

	view := m.View()
	if !strings.Contains(view, "auth failed") {
		t.Error("expected error message in view")
	}
	if !strings.Contains(view, "worry") {
		t.Error("expected reassuring tip in view")
	}
}

func TestSmokeTestModel_EnterAfterComplete(t *testing.T) {
	m := NewSmokeTestModel(En, "sk-test")
	m.state = smokeTestPassed

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter after completion")
	}
	msg := cmd()
	if _, ok := msg.(SmokeTestDoneMsg); !ok {
		t.Errorf("expected SmokeTestDoneMsg, got %T", msg)
	}
}

func TestSmokeTestModel_EnterDuringRunningNoOp(t *testing.T) {
	m := NewSmokeTestModel(En, "sk-test")
	m.state = smokeTestRunning

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected no cmd during running state")
	}
}

func TestSmokeTestModel_EscAfterComplete(t *testing.T) {
	m := NewSmokeTestModel(En, "sk-test")
	m.state = smokeTestFailed

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd on esc after completion")
	}
	msg := cmd()
	if _, ok := msg.(stepBackMsg); !ok {
		t.Errorf("expected stepBackMsg, got %T", msg)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
