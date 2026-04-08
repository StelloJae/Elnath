package onboarding

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SummaryDoneMsg is emitted when the user confirms the summary.
type SummaryDoneMsg struct{}

// SummaryEditMsg is emitted when the user wants to edit a specific step.
type SummaryEditMsg struct {
	Step Step
}

type summaryAction int

const (
	summaryConfirm summaryAction = iota
	summaryEdit
)

// SummaryModel is the Bubbletea model for the configuration summary screen.
type SummaryModel struct {
	locale Locale
	result Result
	cursor int
	quick  bool
}

// NewSummaryModel creates a new summary review model.
func NewSummaryModel(locale Locale, result Result, quick bool) SummaryModel {
	return SummaryModel{
		locale: locale,
		result: result,
		cursor: 0,
		quick:  quick,
	}
}

func (m SummaryModel) Init() tea.Cmd {
	return nil
}

func (m SummaryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < 1 {
				m.cursor++
			}
		case "enter":
			if m.cursor == 0 {
				return m, func() tea.Msg { return SummaryDoneMsg{} }
			}
			return m, func() tea.Msg { return SummaryEditMsg{Step: StepAPIKey} }
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m SummaryModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(T(m.locale, "summary.title")))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render(T(m.locale, "summary.subtitle")))
	b.WriteString("\n\n")

	// API Key (masked)
	b.WriteString(m.renderRow(T(m.locale, "summary.apikey"), m.maskedKey()))
	b.WriteString("\n")

	if !m.quick {
		// Permission Mode
		b.WriteString(m.renderRow(T(m.locale, "summary.permission"), m.result.PermissionMode))
		b.WriteString("\n")

		// MCP Servers
		mcpVal := T(m.locale, "summary.mcp.none")
		if len(m.result.MCPServers) > 0 {
			names := make([]string, len(m.result.MCPServers))
			for i, s := range m.result.MCPServers {
				names[i] = s.Name
			}
			mcpVal = strings.Join(names, ", ")
		}
		b.WriteString(m.renderRow(T(m.locale, "summary.mcp"), mcpVal))
		b.WriteString("\n")

		// Directories
		b.WriteString(m.renderRow(T(m.locale, "summary.datadir"), m.result.DataDir))
		b.WriteString("\n")
		b.WriteString(m.renderRow(T(m.locale, "summary.wikidir"), m.result.WikiDir))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// Action buttons
	actions := []string{
		T(m.locale, "summary.confirm"),
		T(m.locale, "summary.edit"),
	}
	for i, action := range actions {
		if i == m.cursor {
			b.WriteString(selectedItemStyle.Render("  ▸ " + action))
		} else {
			b.WriteString(unselectedItemStyle.Render("    " + action))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(T(m.locale, "summary.navigate")))
	b.WriteString("\n")

	return containerStyle.Render(b.String())
}

func (m SummaryModel) renderRow(label, value string) string {
	return boxStyle.Render(
		selectedItemStyle.Render(label) + "\n" +
			unselectedItemStyle.Render("  "+value),
	)
}

func (m SummaryModel) maskedKey() string {
	key := m.result.APIKey
	if key == "" {
		return "(not set)"
	}
	if len(key) <= 8 {
		return "••••••••"
	}
	suffix := key[len(key)-4:]
	return fmt.Sprintf(T(m.locale, "summary.masked"), suffix)
}
