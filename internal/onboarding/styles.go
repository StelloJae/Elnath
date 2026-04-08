package onboarding

import "github.com/charmbracelet/lipgloss"

var (
	accentColor   = lipgloss.Color("#7C3AED")
	subtleColor   = lipgloss.Color("#6B7280")
	selectedColor = lipgloss.Color("#A78BFA")
	textColor     = lipgloss.Color("#E5E7EB")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor).
			MarginBottom(1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			MarginBottom(1)

	logoStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(selectedColor).
				Bold(true)

	unselectedItemStyle = lipgloss.NewStyle().
				Foreground(textColor)

	descStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			PaddingLeft(4)

	helpStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			MarginTop(1)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981")).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")).
			Bold(true)

	containerStyle = lipgloss.NewStyle().
			Padding(1, 2)

	recommendedBadge = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F59E0B")).
				Bold(true)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtleColor).
			Padding(0, 1).
			Width(62)

	activeBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accentColor).
				Padding(0, 1).
				Width(62)

	checkboxOn = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981")).
			Bold(true)

	checkboxOff = lipgloss.NewStyle().
			Foreground(subtleColor)

	categoryStyle = lipgloss.NewStyle().
				Foreground(accentColor).
				Bold(true).
				MarginTop(1)

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))

	progressFilledStyle = lipgloss.NewStyle().
				Foreground(accentColor).
				Bold(true)

	progressEmptyStyle = lipgloss.NewStyle().
				Foreground(subtleColor)

	accentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B")).
			Bold(true)
)
