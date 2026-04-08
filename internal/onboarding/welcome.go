package onboarding

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PathChoice represents the user's selected onboarding path.
type PathChoice string

const (
	PathQuick PathChoice = "quick"
	PathFull  PathChoice = "full"
)

// WelcomeDoneMsg is emitted when the user confirms their path choice.
type WelcomeDoneMsg struct {
	Path PathChoice
}

// WelcomeModel is the Bubbletea model for the welcome screen.
type WelcomeModel struct {
	locale  Locale
	version string
	cursor  int
	choices []PathChoice
}

// NewWelcomeModel creates a new welcome screen model.
func NewWelcomeModel(locale Locale, version string) WelcomeModel {
	return WelcomeModel{
		locale:  locale,
		version: version,
		cursor:  0,
		choices: []PathChoice{PathQuick, PathFull},
	}
}

func (m WelcomeModel) Init() tea.Cmd {
	return nil
}

func (m WelcomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			return m, func() tea.Msg {
				return WelcomeDoneMsg{Path: m.choices[m.cursor]}
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m WelcomeModel) View() string {
	var b strings.Builder

	b.WriteString(renderLogo())
	b.WriteString("\n\n")

	b.WriteString(titleStyle.Render(T(m.locale, "welcome.title")))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render(
		T(m.locale, "welcome.subtitle") + "  " +
			fmt.Sprintf(T(m.locale, "welcome.version"), m.version),
	))
	b.WriteString("\n\n")

	for i, choice := range m.choices {
		var label, desc string
		switch choice {
		case PathQuick:
			label = T(m.locale, "welcome.quick")
			desc = T(m.locale, "welcome.quick.desc")
		case PathFull:
			label = T(m.locale, "welcome.full")
			desc = T(m.locale, "welcome.full.desc")
		}

		if i == m.cursor {
			b.WriteString(selectedItemStyle.Render("  ▸ " + label))
		} else {
			b.WriteString(unselectedItemStyle.Render("    " + label))
		}
		b.WriteString("\n")
		b.WriteString(descStyle.Render(desc))
		b.WriteString("\n")
	}

	b.WriteString(helpStyle.Render(T(m.locale, "welcome.navigate")))
	b.WriteString("\n")

	return containerStyle.Render(b.String())
}

// SelectedPath returns the currently highlighted path choice.
func (m WelcomeModel) SelectedPath() PathChoice {
	return m.choices[m.cursor]
}
