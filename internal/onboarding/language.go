package onboarding

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// LanguageDoneMsg is emitted when the user confirms their language choice.
type LanguageDoneMsg struct {
	Locale Locale
}

// LanguageModel is the Bubbletea model for the language selection screen.
type LanguageModel struct {
	locale  Locale
	cursor  int
	choices []Locale
}

// NewLanguageModel creates a new language selection model.
func NewLanguageModel(current Locale) LanguageModel {
	choices := Locales()
	cursor := 0
	for i, l := range choices {
		if l == current {
			cursor = i
			break
		}
	}
	return LanguageModel{
		locale:  current,
		cursor:  cursor,
		choices: choices,
	}
}

func (m LanguageModel) Init() tea.Cmd {
	return nil
}

func (m LanguageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			selected := m.choices[m.cursor]
			return m, func() tea.Msg {
				return LanguageDoneMsg{Locale: selected}
			}
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m LanguageModel) View() string {
	locale := m.choices[m.cursor]

	var b strings.Builder
	b.WriteString(titleStyle.Render(T(locale, "lang.title")))
	b.WriteString("\n\n")

	for i, choice := range m.choices {
		label := T(locale, "lang."+string(choice))
		if i == m.cursor {
			b.WriteString(selectedItemStyle.Render("  ▸ " + label))
		} else {
			b.WriteString(unselectedItemStyle.Render("    " + label))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(T(locale, "lang.navigate")))
	b.WriteString("\n")

	return containerStyle.Render(b.String())
}

// SelectedLocale returns the currently highlighted locale.
func (m LanguageModel) SelectedLocale() Locale {
	return m.choices[m.cursor]
}
