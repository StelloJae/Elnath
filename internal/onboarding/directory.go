package onboarding

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// DirectoryDoneMsg is emitted when the user confirms directory paths.
type DirectoryDoneMsg struct {
	DataDir string
	WikiDir string
}

type dirField int

const (
	dirFieldData dirField = iota
	dirFieldWiki
)

// DirectoryModel is the Bubbletea model for directory setup (Full path only).
type DirectoryModel struct {
	locale     Locale
	dataInput  textinput.Model
	wikiInput  textinput.Model
	focused    dirField
	defaultData string
	defaultWiki string
}

// NewDirectoryModel creates a new directory setup model.
// Optional existingDirs [dataDir, wikiDir] pre-fills the text inputs.
func NewDirectoryModel(locale Locale, existingDirs ...string) DirectoryModel {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".elnath")
	defaultData := filepath.Join(base, "data")
	defaultWiki := filepath.Join(base, "wiki")

	// Use existing values as defaults if provided.
	if len(existingDirs) >= 1 && existingDirs[0] != "" {
		defaultData = existingDirs[0]
	}
	if len(existingDirs) >= 2 && existingDirs[1] != "" {
		defaultWiki = existingDirs[1]
	}

	di := textinput.New()
	di.Placeholder = defaultData
	di.Width = 60
	di.Focus()

	wi := textinput.New()
	wi.Placeholder = defaultWiki
	wi.Width = 60

	return DirectoryModel{
		locale:      locale,
		dataInput:   di,
		wikiInput:   wi,
		focused:     dirFieldData,
		defaultData: defaultData,
		defaultWiki: defaultWiki,
	}
}

func (m DirectoryModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m DirectoryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "down":
			if m.focused == dirFieldData {
				m.focused = dirFieldWiki
				m.dataInput.Blur()
				m.wikiInput.Focus()
				return m, textinput.Blink
			}
		case "shift+tab", "up":
			if m.focused == dirFieldWiki {
				m.focused = dirFieldData
				m.wikiInput.Blur()
				m.dataInput.Focus()
				return m, textinput.Blink
			}
		case "enter":
			if m.focused == dirFieldData {
				m.focused = dirFieldWiki
				m.dataInput.Blur()
				m.wikiInput.Focus()
				return m, textinput.Blink
			}
			dataDir := strings.TrimSpace(m.dataInput.Value())
			if dataDir == "" {
				dataDir = m.defaultData
			}
			wikiDir := strings.TrimSpace(m.wikiInput.Value())
			if wikiDir == "" {
				wikiDir = m.defaultWiki
			}
			return m, func() tea.Msg {
				return DirectoryDoneMsg{DataDir: dataDir, WikiDir: wikiDir}
			}
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	switch m.focused {
	case dirFieldData:
		m.dataInput, cmd = m.dataInput.Update(msg)
	case dirFieldWiki:
		m.wikiInput, cmd = m.wikiInput.Update(msg)
	}
	return m, cmd
}

func (m DirectoryModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(T(m.locale, "dir.title")))
	b.WriteString("\n\n")

	// Data directory field.
	label := T(m.locale, "dir.data")
	if m.focused == dirFieldData {
		b.WriteString(selectedItemStyle.Render("▸ " + label))
	} else {
		b.WriteString(unselectedItemStyle.Render("  " + label))
	}
	b.WriteString("\n")
	b.WriteString("  " + m.dataInput.View())
	b.WriteString("\n")
	b.WriteString(descStyle.Render(fmt.Sprintf(T(m.locale, "dir.default"), m.defaultData)))
	b.WriteString("\n\n")

	// Wiki directory field.
	label = T(m.locale, "dir.wiki")
	if m.focused == dirFieldWiki {
		b.WriteString(selectedItemStyle.Render("▸ " + label))
	} else {
		b.WriteString(unselectedItemStyle.Render("  " + label))
	}
	b.WriteString("\n")
	b.WriteString("  " + m.wikiInput.View())
	b.WriteString("\n")
	b.WriteString(descStyle.Render(fmt.Sprintf(T(m.locale, "dir.default"), m.defaultWiki)))
	b.WriteString("\n\n")

	b.WriteString(helpStyle.Render(T(m.locale, "dir.navigate")))
	b.WriteString("\n")

	return containerStyle.Render(b.String())
}
