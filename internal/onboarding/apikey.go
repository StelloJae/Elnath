package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// APIKeyDoneMsg is emitted when the user confirms their API key.
type APIKeyDoneMsg struct {
	Key string
}

type apiKeyValidatedMsg struct {
	result ValidationResult
}

type apiKeyState int

const (
	apiKeyInput apiKeyState = iota
	apiKeyValidating
	apiKeyResult
)

// APIKeyModel is the Bubbletea model for the API key input screen.
type APIKeyModel struct {
	locale      Locale
	textInput   textinput.Model
	spinner     spinner.Model
	state       apiKeyState
	validated   *ValidationResult
	existingKey string
}

// NewAPIKeyModel creates a new API key input model.
// An optional existingKey shows a masked hint that a key is already configured.
func NewAPIKeyModel(locale Locale, existingKey ...string) APIKeyModel {
	ti := textinput.New()
	ti.Placeholder = T(locale, "apikey.placeholder")
	if len(existingKey) > 0 && existingKey[0] != "" {
		// Show masked hint of existing key as placeholder.
		k := existingKey[0]
		if len(k) > 8 {
			ti.Placeholder = "••••" + k[len(k)-4:] + " (enter to keep)"
		}
	}
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 200
	ti.Width = 60
	ti.Focus()

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(accentColor)

	existing := ""
	if len(existingKey) > 0 {
		existing = existingKey[0]
	}
	return APIKeyModel{
		locale:      locale,
		textInput:   ti,
		spinner:     s,
		state:       apiKeyInput,
		existingKey: existing,
	}
}

func (m APIKeyModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m APIKeyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if m.state == apiKeyInput {
				return m, func() tea.Msg { return stepBackMsg{} }
			}
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			switch m.state {
			case apiKeyInput:
				key := strings.TrimSpace(m.textInput.Value())
				if key == "" {
					// In rerun mode, keep the existing key; otherwise skip.
					kept := m.existingKey
					return m, func() tea.Msg { return APIKeyDoneMsg{Key: kept} }
				}
				m.state = apiKeyValidating
				return m, tea.Batch(
					m.spinner.Tick,
					validateKeyCmd(key),
				)
			case apiKeyResult:
				key := strings.TrimSpace(m.textInput.Value())
				return m, func() tea.Msg { return APIKeyDoneMsg{Key: key} }
			}
		}
	case apiKeyValidatedMsg:
		m.state = apiKeyResult
		m.validated = &msg.result
		return m, nil
	case spinner.TickMsg:
		if m.state == apiKeyValidating {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	if m.state == apiKeyInput {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m APIKeyModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(T(m.locale, "apikey.title")))
	b.WriteString("\n\n")
	b.WriteString(T(m.locale, "apikey.prompt"))
	b.WriteString("\n\n")
	b.WriteString(m.textInput.View())
	b.WriteString("\n\n")

	switch m.state {
	case apiKeyInput:
		b.WriteString(helpStyle.Render(T(m.locale, "apikey.skip")))
	case apiKeyValidating:
		b.WriteString(m.spinner.View() + " " + T(m.locale, "apikey.validating"))
	case apiKeyResult:
		if m.validated != nil {
			if m.validated.Valid {
				b.WriteString(successStyle.Render("✓ " + T(m.locale, "apikey.valid")))
			} else if m.validated.Error != nil {
				b.WriteString(errorStyle.Render("✗ " + T(m.locale, "apikey.invalid")))
			}
			if m.validated.Error != nil && m.validated.Valid {
				b.WriteString("\n")
				b.WriteString(helpStyle.Render(fmt.Sprintf(T(m.locale, "apikey.error"), m.validated.Error)))
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(T(m.locale, "apikey.navigate")))
	b.WriteString("\n")

	return containerStyle.Render(b.String())
}

func validateKeyCmd(key string) tea.Cmd {
	return func() tea.Msg {
		result := ValidateAnthropicKey(context.Background(), key)
		return apiKeyValidatedMsg{result: result}
	}
}
