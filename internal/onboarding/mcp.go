package onboarding

import (
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// MCPDoneMsg is emitted when the user confirms MCP server selection.
type MCPDoneMsg struct {
	Servers []MCPSelection
}

// MCPSelection represents a selected MCP server for config output.
type MCPSelection struct {
	Name    string
	Command string
	Args    []string
}

type mcpEntry struct {
	name        string
	description string
	command     string
	args        []string
	requiresNpm bool
}

type mcpCategory struct {
	nameKey string
	servers []mcpEntry
}

var mcpCatalog = []mcpCategory{
	{
		nameKey: "mcp.cat.dev",
		servers: []mcpEntry{
			{name: "GitHub", description: "GitHub API — repos, issues, PRs", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-github"}, requiresNpm: true},
			{name: "Filesystem", description: "Local filesystem read/write", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-filesystem"}, requiresNpm: true},
			{name: "Git", description: "Git operations — log, diff, blame", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-git"}, requiresNpm: true},
			{name: "GitLab", description: "GitLab API integration", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-gitlab"}, requiresNpm: true},
		},
	},
	{
		nameKey: "mcp.cat.research",
		servers: []mcpEntry{
			{name: "Brave Search", description: "Web search via Brave API", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-brave-search"}, requiresNpm: true},
			{name: "Fetch", description: "HTTP fetch — retrieve web content", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-fetch"}, requiresNpm: true},
			{name: "Google Maps", description: "Google Maps places & directions", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-google-maps"}, requiresNpm: true},
		},
	},
	{
		nameKey: "mcp.cat.media",
		servers: []mcpEntry{
			{name: "ElevenLabs", description: "Text-to-speech generation", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-elevenlabs"}, requiresNpm: true},
		},
	},
	{
		nameKey: "mcp.cat.testing",
		servers: []mcpEntry{
			{name: "Playwright", description: "Browser automation & testing", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-playwright"}, requiresNpm: true},
			{name: "Puppeteer", description: "Headless Chrome automation", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-puppeteer"}, requiresNpm: true},
		},
	},
	{
		nameKey: "mcp.cat.data",
		servers: []mcpEntry{
			{name: "PostgreSQL", description: "PostgreSQL database access", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-postgres"}, requiresNpm: true},
			{name: "SQLite", description: "SQLite database access", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-sqlite"}, requiresNpm: true},
			{name: "Memory", description: "In-memory knowledge graph", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-memory"}, requiresNpm: true},
			{name: "Sequential Thinking", description: "Dynamic problem decomposition", command: "npx", args: []string{"-y", "@modelcontextprotocol/server-sequential-thinking"}, requiresNpm: true},
		},
	},
}

// flatItem maps a cursor position to a catalog entry.
type flatItem struct {
	isCat    bool
	catIdx   int
	srvIdx   int
	globalID int // unique ID for selection tracking
}

// MCPModel is the Bubbletea model for the MCP server catalog.
type MCPModel struct {
	locale   Locale
	items    []flatItem
	cursor   int
	selected map[int]bool // globalID → selected
	hasNpm   bool
}

// DetectNpm checks whether npm and npx are available on PATH.
func DetectNpm() bool {
	_, npmErr := exec.LookPath("npm")
	_, npxErr := exec.LookPath("npx")
	return npmErr == nil && npxErr == nil
}

// NewMCPModel creates a new MCP catalog model.
// hasNpm should be obtained from DetectNpm() once at startup.
func NewMCPModel(locale Locale, hasNpm bool) MCPModel {
	items := buildFlatItems()

	return MCPModel{
		locale:   locale,
		items:    items,
		cursor:   firstServerIndex(items),
		selected: make(map[int]bool),
		hasNpm:   hasNpm,
	}
}

func buildFlatItems() []flatItem {
	var items []flatItem
	id := 0
	for catIdx, cat := range mcpCatalog {
		items = append(items, flatItem{isCat: true, catIdx: catIdx, globalID: -1})
		for srvIdx := range cat.servers {
			items = append(items, flatItem{isCat: false, catIdx: catIdx, srvIdx: srvIdx, globalID: id})
			id++
		}
	}
	return items
}

func firstServerIndex(items []flatItem) int {
	for i, item := range items {
		if !item.isCat {
			return i
		}
	}
	return 0
}

func (m MCPModel) Init() tea.Cmd {
	return nil
}

func (m MCPModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.cursor = m.prevServer()
		case "down", "j":
			m.cursor = m.nextServer()
		case " ":
			item := m.items[m.cursor]
			if !item.isCat {
				m.selected[item.globalID] = !m.selected[item.globalID]
			}
		case "enter":
			return m, func() tea.Msg { return MCPDoneMsg{Servers: m.selectedServers()} }
		case "esc":
			return m, func() tea.Msg { return stepBackMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m MCPModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(T(m.locale, "mcp.title")))
	b.WriteString("\n")
	b.WriteString(subtitleStyle.Render(T(m.locale, "mcp.subtitle")))
	b.WriteString("\n\n")

	if !m.hasNpm {
		b.WriteString(warningStyle.Render(T(m.locale, "mcp.npm.warning")))
	} else {
		b.WriteString(successStyle.Render(T(m.locale, "mcp.npm.ok")))
	}
	b.WriteString("\n\n")

	for i, item := range m.items {
		if item.isCat {
			cat := mcpCatalog[item.catIdx]
			b.WriteString(categoryStyle.Render(T(m.locale, cat.nameKey)))
			b.WriteString("\n")
			continue
		}

		srv := mcpCatalog[item.catIdx].servers[item.srvIdx]
		checked := m.selected[item.globalID]

		var checkbox string
		if checked {
			checkbox = checkboxOn.Render("[✓]")
		} else {
			checkbox = checkboxOff.Render("[ ]")
		}

		label := srv.name + " — " + srv.description
		if i == m.cursor {
			b.WriteString("  " + checkbox + " " + selectedItemStyle.Render(label))
		} else {
			b.WriteString("  " + checkbox + " " + unselectedItemStyle.Render(label))
		}
		b.WriteString("\n")
	}

	count := m.selectedCount()
	if count == 0 {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render(T(m.locale, "mcp.none")))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(T(m.locale, "mcp.navigate")))
	b.WriteString("\n")

	return containerStyle.Render(b.String())
}

func (m MCPModel) prevServer() int {
	for i := m.cursor - 1; i >= 0; i-- {
		if !m.items[i].isCat {
			return i
		}
	}
	return m.cursor
}

func (m MCPModel) nextServer() int {
	for i := m.cursor + 1; i < len(m.items); i++ {
		if !m.items[i].isCat {
			return i
		}
	}
	return m.cursor
}

func (m MCPModel) selectedCount() int {
	count := 0
	for _, v := range m.selected {
		if v {
			count++
		}
	}
	return count
}

func (m MCPModel) selectedServers() []MCPSelection {
	var result []MCPSelection
	for _, item := range m.items {
		if item.isCat {
			continue
		}
		if !m.selected[item.globalID] {
			continue
		}
		srv := mcpCatalog[item.catIdx].servers[item.srvIdx]
		result = append(result, MCPSelection{
			Name:    srv.name,
			Command: srv.command,
			Args:    srv.args,
		})
	}
	return result
}
