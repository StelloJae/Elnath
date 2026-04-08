package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SmokeTestDoneMsg is emitted when the smoke test completes (pass or fail).
type SmokeTestDoneMsg struct{}

type smokeTestResultMsg struct {
	success  bool
	response string
	err      error
}

type smokeTestState int

const (
	smokeTestRunning smokeTestState = iota
	smokeTestPassed
	smokeTestFailed
	smokeTestSkipped
)

// SmokeTestModel is the Bubbletea model for the post-setup connection test.
type SmokeTestModel struct {
	locale   Locale
	apiKey   string
	spinner  spinner.Model
	state    smokeTestState
	response string
	errMsg   string
}

// NewSmokeTestModel creates a new smoke test model.
func NewSmokeTestModel(locale Locale, apiKey string) SmokeTestModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(accentColor)

	return SmokeTestModel{
		locale:  locale,
		apiKey:  apiKey,
		spinner: s,
		state:   smokeTestRunning,
	}
}

func (m SmokeTestModel) Init() tea.Cmd {
	if m.apiKey == "" {
		return func() tea.Msg {
			return smokeTestResultMsg{success: false, err: fmt.Errorf("no API key")}
		}
	}
	return tea.Batch(m.spinner.Tick, runSmokeTestCmd(m.apiKey))
}

func (m SmokeTestModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.state != smokeTestRunning {
				return m, func() tea.Msg { return SmokeTestDoneMsg{} }
			}
		case "esc":
			if m.state != smokeTestRunning {
				return m, func() tea.Msg { return stepBackMsg{} }
			}
		case "ctrl+c":
			return m, tea.Quit
		}
	case smokeTestResultMsg:
		if msg.success {
			m.state = smokeTestPassed
			m.response = msg.response
		} else if m.apiKey == "" {
			m.state = smokeTestSkipped
		} else {
			m.state = smokeTestFailed
			if msg.err != nil {
				m.errMsg = msg.err.Error()
			}
		}
		return m, nil
	case spinner.TickMsg:
		if m.state == smokeTestRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m SmokeTestModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(T(m.locale, "smoketest.title")))
	b.WriteString("\n\n")

	switch m.state {
	case smokeTestRunning:
		b.WriteString(m.spinner.View() + " " + T(m.locale, "smoketest.testing"))
	case smokeTestPassed:
		b.WriteString(successStyle.Render(T(m.locale, "smoketest.success")))
		if m.response != "" {
			b.WriteString("\n\n")
			truncated := m.response
			if len(truncated) > 120 {
				truncated = truncated[:120] + "..."
			}
			b.WriteString(descStyle.Render(fmt.Sprintf(T(m.locale, "smoketest.response"), truncated)))
		}
	case smokeTestFailed:
		b.WriteString(warningStyle.Render(fmt.Sprintf(T(m.locale, "smoketest.fail"), m.errMsg)))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render(T(m.locale, "smoketest.fail.tip")))
	case smokeTestSkipped:
		b.WriteString(helpStyle.Render(T(m.locale, "smoketest.skip")))
	}

	b.WriteString("\n\n")
	if m.state != smokeTestRunning {
		b.WriteString(helpStyle.Render(T(m.locale, "smoketest.continue")))
	}
	b.WriteString("\n")

	return containerStyle.Render(b.String())
}

func runSmokeTestCmd(apiKey string) tea.Cmd {
	return func() tea.Msg {
		result := ValidateAnthropicKey(context.Background(), apiKey)
		if result.Valid {
			return smokeTestResultMsg{success: true, response: "API key validated successfully"}
		}
		return smokeTestResultMsg{success: false, err: result.Error}
	}
}
