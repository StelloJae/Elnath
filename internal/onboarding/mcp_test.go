package onboarding

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMCPModel_InitialState(t *testing.T) {
	m := NewMCPModel(En, true)
	if len(m.items) == 0 {
		t.Error("expected non-empty items")
	}
	if m.items[m.cursor].isCat {
		t.Error("cursor should start on a server, not a category")
	}
	if len(m.selected) != 0 {
		t.Error("expected no initial selections")
	}
}

func TestMCPModel_CursorSkipsCategories(t *testing.T) {
	m := NewMCPModel(En, true)
	startCursor := m.cursor

	// Move down
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(MCPModel)
	if m.items[m.cursor].isCat {
		t.Error("cursor should skip category headers when moving down")
	}
	if m.cursor <= startCursor {
		t.Error("cursor should have advanced")
	}

	// Move up back
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(MCPModel)
	if m.items[m.cursor].isCat {
		t.Error("cursor should skip category headers when moving up")
	}
}

func TestMCPModel_SpaceTogglesSelection(t *testing.T) {
	m := NewMCPModel(En, true)
	item := m.items[m.cursor]

	// Toggle on
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(MCPModel)
	if !m.selected[item.globalID] {
		t.Error("expected item to be selected after space")
	}

	// Toggle off
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(MCPModel)
	if m.selected[item.globalID] {
		t.Error("expected item to be deselected after second space")
	}
}

func TestMCPModel_EnterConfirms(t *testing.T) {
	m := NewMCPModel(En, true)

	// Select first item
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(MCPModel)

	// Confirm
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter")
	}

	msg := cmd()
	done, ok := msg.(MCPDoneMsg)
	if !ok {
		t.Fatalf("expected MCPDoneMsg, got %T", msg)
	}
	if len(done.Servers) != 1 {
		t.Errorf("expected 1 selected server, got %d", len(done.Servers))
	}
}

func TestMCPModel_EnterWithNoSelection(t *testing.T) {
	m := NewMCPModel(En, true)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd on enter")
	}
	msg := cmd()
	done, ok := msg.(MCPDoneMsg)
	if !ok {
		t.Fatalf("expected MCPDoneMsg, got %T", msg)
	}
	if len(done.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(done.Servers))
	}
}

func TestMCPModel_EscGoesBack(t *testing.T) {
	m := NewMCPModel(En, true)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cmd on esc")
	}
	msg := cmd()
	if _, ok := msg.(stepBackMsg); !ok {
		t.Fatalf("expected stepBackMsg, got %T", msg)
	}
}

func TestMCPModel_CtrlCQuits(t *testing.T) {
	m := NewMCPModel(En, true)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("expected quit cmd on ctrl+c")
	}
}

func TestMCPModel_MultipleSelections(t *testing.T) {
	m := NewMCPModel(En, true)

	// Select first server
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(MCPModel)

	// Move down and select second
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(MCPModel)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(MCPModel)

	if m.selectedCount() != 2 {
		t.Errorf("expected 2 selected, got %d", m.selectedCount())
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	done := msg.(MCPDoneMsg)
	if len(done.Servers) != 2 {
		t.Errorf("expected 2 servers in result, got %d", len(done.Servers))
	}
}

func TestMCPModel_CatalogHasAllCategories(t *testing.T) {
	expectedCats := 5 // Development, Research, Media, Testing, Data
	if len(mcpCatalog) != expectedCats {
		t.Errorf("expected %d categories, got %d", expectedCats, len(mcpCatalog))
	}

	totalServers := 0
	for _, cat := range mcpCatalog {
		if len(cat.servers) == 0 {
			t.Errorf("category %q has no servers", cat.nameKey)
		}
		totalServers += len(cat.servers)
	}
	if totalServers < 10 || totalServers > 20 {
		t.Errorf("expected 10-20 servers in catalog, got %d", totalServers)
	}
}

func TestMCPModel_ViewRendersLocales(t *testing.T) {
	for _, locale := range []Locale{En, Ko} {
		t.Run(string(locale), func(t *testing.T) {
			m := NewMCPModel(locale, true)
			view := m.View()
			if view == "" {
				t.Error("expected non-empty view")
			}
		})
	}
}

func TestBuildFlatItems(t *testing.T) {
	items := buildFlatItems()
	if len(items) == 0 {
		t.Fatal("expected non-empty flat items")
	}

	catCount := 0
	srvCount := 0
	for _, item := range items {
		if item.isCat {
			catCount++
		} else {
			srvCount++
		}
	}
	if catCount != len(mcpCatalog) {
		t.Errorf("expected %d categories, got %d", len(mcpCatalog), catCount)
	}
	if srvCount == 0 {
		t.Error("expected server items")
	}
}

func TestMCPModel_NpmWarningShown(t *testing.T) {
	m := NewMCPModel(En, false)
	view := m.View()
	if !containsStr(view, "npm") {
		t.Error("expected npm warning in view when hasNpm=false")
	}
}

func containsStr(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestMCPSelection_HasRequiredFields(t *testing.T) {
	m := NewMCPModel(En, true)
	// Select first server
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(MCPModel)

	servers := m.selectedServers()
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	s := servers[0]
	if s.Name == "" {
		t.Error("expected non-empty Name")
	}
	if s.Command == "" {
		t.Error("expected non-empty Command")
	}
}
