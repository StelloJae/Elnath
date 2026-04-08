package onboarding

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// PermissionDoneMsg is emitted when the user selects a permission mode.
type PermissionDoneMsg struct {
	Mode string
}

type permMode struct {
	id          string
	nameKey     string
	descKey     string
	recommended bool
}

var permModes = []permMode{
	{id: "default", nameKey: "perm.default", descKey: "perm.default.desc", recommended: true},
	{id: "accept_edits", nameKey: "perm.accept_edits", descKey: "perm.accept_edits.desc"},
	{id: "plan", nameKey: "perm.plan", descKey: "perm.plan.desc"},
	{id: "bypass", nameKey: "perm.bypass", descKey: "perm.bypass.desc"},
}

// PermissionModel is the Bubbletea model for the permission mode selector.
type PermissionModel struct {
	locale Locale
	cursor int
}

// NewPermissionModel creates a new permission mode selector.
func NewPermissionModel(locale Locale) PermissionModel {
	return PermissionModel{locale: locale, cursor: 0}
}

func (m PermissionModel) Init() tea.Cmd {
	return nil
}

func (m PermissionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(permModes)-1 {
				m.cursor++
			}
		case "enter":
			selected := permModes[m.cursor]
			return m, func() tea.Msg {
				return PermissionDoneMsg{Mode: selected.id}
			}
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m PermissionModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(T(m.locale, "perm.title")))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render(T(m.locale, "perm.subtitle")))
	b.WriteString("\n\n")

	for i, mode := range permModes {
		name := T(m.locale, mode.nameKey)
		if mode.recommended {
			name = name + " " + recommendedBadge.Render(T(m.locale, "perm.recommended"))
		}

		desc := T(m.locale, mode.descKey)

		if i == m.cursor {
			b.WriteString(selectedItemStyle.Render("▸ " + name))
			b.WriteString("\n")
			b.WriteString(activeBoxStyle.Render(desc))
		} else {
			b.WriteString(unselectedItemStyle.Render("  " + name))
			b.WriteString("\n")
			b.WriteString(boxStyle.Render(desc))
		}
		b.WriteString("\n\n")
	}

	b.WriteString(helpStyle.Render(T(m.locale, "perm.navigate")))
	b.WriteString("\n")

	return containerStyle.Render(b.String())
}
