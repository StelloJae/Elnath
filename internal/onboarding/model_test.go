package onboarding

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModel_InitialStep(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	if m.step != StepWelcome {
		t.Errorf("expected StepWelcome, got %d", m.step)
	}
	if m.Done() {
		t.Error("model should not be done initially")
	}
}

func TestModel_WelcomeDoneToLanguage(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	updated, _ := m.Update(WelcomeDoneMsg{Path: PathQuick})
	model := updated.(Model)

	if model.step != StepLanguage {
		t.Errorf("expected StepLanguage after WelcomeDone, got %d", model.step)
	}
	if model.result.Path != PathQuick {
		t.Errorf("expected PathQuick, got %q", model.result.Path)
	}
}

func TestModel_LanguageDoneToAPIKey(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepLanguage

	updated, _ := m.Update(LanguageDoneMsg{Locale: Ko})
	model := updated.(Model)

	if model.step != StepAPIKey {
		t.Errorf("expected StepAPIKey after LanguageDone, got %d", model.step)
	}
	if model.locale != Ko {
		t.Errorf("expected Ko locale, got %q", model.locale)
	}
	if model.result.Locale != Ko {
		t.Errorf("expected Ko in result, got %q", model.result.Locale)
	}
}

func TestModel_APIKeyDone_QuickPath(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepAPIKey
	m.result.Path = PathQuick

	updated, _ := m.Update(APIKeyDoneMsg{Key: "sk-test"})
	model := updated.(Model)

	if model.step != StepSummary {
		t.Errorf("expected StepSummary after APIKeyDone on Quick path, got %d", model.step)
	}
	if model.result.APIKey != "sk-test" {
		t.Errorf("expected api key 'sk-test', got %q", model.result.APIKey)
	}
	if model.result.DataDir == "" {
		t.Error("expected DataDir to be auto-populated on Quick path")
	}
	if model.result.WikiDir == "" {
		t.Error("expected WikiDir to be auto-populated on Quick path")
	}
	if model.result.PermissionMode != "default" {
		t.Errorf("expected default permission on Quick path, got %q", model.result.PermissionMode)
	}
}

func TestModel_APIKeyDone_FullPath(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepAPIKey
	m.result.Path = PathFull

	updated, _ := m.Update(APIKeyDoneMsg{Key: "sk-full"})
	model := updated.(Model)

	if model.step != StepPermission {
		t.Errorf("expected StepPermission after APIKeyDone on Full path, got %d", model.step)
	}
	if model.result.APIKey != "sk-full" {
		t.Errorf("expected api key 'sk-full', got %q", model.result.APIKey)
	}
}

func TestModel_PermissionDone_ToMCP(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepPermission
	m.result.Path = PathFull

	updated, _ := m.Update(PermissionDoneMsg{Mode: "accept_edits"})
	model := updated.(Model)

	if model.step != StepMCP {
		t.Errorf("expected StepMCP after PermissionDone, got %d", model.step)
	}
	if model.result.PermissionMode != "accept_edits" {
		t.Errorf("expected accept_edits, got %q", model.result.PermissionMode)
	}
}

func TestModel_MCPDone_ToDirectory(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepMCP
	m.result.Path = PathFull

	servers := []MCPSelection{{Name: "GitHub", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}}}
	updated, _ := m.Update(MCPDoneMsg{Servers: servers})
	model := updated.(Model)

	if model.step != StepDirectory {
		t.Errorf("expected StepDirectory after MCPDone, got %d", model.step)
	}
	if len(model.result.MCPServers) != 1 {
		t.Errorf("expected 1 MCP server, got %d", len(model.result.MCPServers))
	}
}

func TestModel_DirectoryDone_ToSummary(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepDirectory
	m.result.Path = PathFull

	updated, _ := m.Update(DirectoryDoneMsg{DataDir: "/data", WikiDir: "/wiki"})
	model := updated.(Model)

	if model.step != StepSummary {
		t.Errorf("expected StepSummary after DirectoryDone, got %d", model.step)
	}
	if model.result.DataDir != "/data" {
		t.Errorf("expected /data, got %q", model.result.DataDir)
	}
	if model.result.WikiDir != "/wiki" {
		t.Errorf("expected /wiki, got %q", model.result.WikiDir)
	}
}

func TestModel_SummaryDone_ToSmokeTest(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepSummary
	m.result.APIKey = "sk-test"

	updated, _ := m.Update(SummaryDoneMsg{})
	model := updated.(Model)

	if model.step != StepSmokeTest {
		t.Errorf("expected StepSmokeTest after SummaryDone, got %d", model.step)
	}
}

func TestModel_SummaryEdit_GoesToStep(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepSummary

	updated, _ := m.Update(SummaryEditMsg{Step: StepPermission})
	model := updated.(Model)

	if model.step != StepPermission {
		t.Errorf("expected StepPermission after SummaryEdit, got %d", model.step)
	}
}

func TestModel_SmokeTestDone(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepSmokeTest

	updated, cmd := m.Update(SmokeTestDoneMsg{})
	model := updated.(Model)

	if !model.Done() {
		t.Error("expected Done after SmokeTestDone")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

func TestModel_StepBack_SummaryToDirectory_FullPath(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepSummary
	m.result.Path = PathFull

	updated, _ := m.Update(stepBackMsg{})
	model := updated.(Model)

	if model.step != StepDirectory {
		t.Errorf("expected StepDirectory after back from Summary (full), got %d", model.step)
	}
}

func TestModel_StepBack_SummaryToAPIKey_QuickPath(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepSummary
	m.result.Path = PathQuick

	updated, _ := m.Update(stepBackMsg{})
	model := updated.(Model)

	if model.step != StepAPIKey {
		t.Errorf("expected StepAPIKey after back from Summary (quick), got %d", model.step)
	}
}

func TestModel_StepBack_SmokeTestToSummary(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepSmokeTest

	updated, _ := m.Update(stepBackMsg{})
	model := updated.(Model)

	if model.step != StepSummary {
		t.Errorf("expected StepSummary after back from SmokeTest, got %d", model.step)
	}
}

func TestModel_StepBack_LanguageToWelcome(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepLanguage

	updated, _ := m.Update(stepBackMsg{})
	model := updated.(Model)

	if model.step != StepWelcome {
		t.Errorf("expected StepWelcome after back from Language, got %d", model.step)
	}
}

func TestModel_StepBack_APIKeyToLanguage(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepAPIKey

	updated, _ := m.Update(stepBackMsg{})
	model := updated.(Model)

	if model.step != StepLanguage {
		t.Errorf("expected StepLanguage after back from APIKey, got %d", model.step)
	}
}

func TestModel_StepBack_PermissionToAPIKey(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepPermission

	updated, _ := m.Update(stepBackMsg{})
	model := updated.(Model)

	if model.step != StepAPIKey {
		t.Errorf("expected StepAPIKey after back from Permission, got %d", model.step)
	}
}

func TestModel_StepBack_MCPToPermission(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepMCP

	updated, _ := m.Update(stepBackMsg{})
	model := updated.(Model)

	if model.step != StepPermission {
		t.Errorf("expected StepPermission after back from MCP, got %d", model.step)
	}
}

func TestModel_StepBack_DirectoryToMCP(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	m.step = StepDirectory

	updated, _ := m.Update(stepBackMsg{})
	model := updated.(Model)

	if model.step != StepMCP {
		t.Errorf("expected StepMCP after back from Directory, got %d", model.step)
	}
}

func TestModel_CtrlCQuits(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("expected quit cmd on ctrl+c")
	}
}

func TestModel_WithRerunMode(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0", WithRerunMode())
	if !m.rerun {
		t.Error("expected rerun to be true")
	}
}

func TestModel_WithExistingConfig_Locale(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0",
		WithRerunMode(),
		WithExistingConfig(ExistingConfig{
			Locale:         Ko,
			APIKey:         "sk-existing",
			PermissionMode: "accept_edits",
			DataDir:        "/existing/data",
			WikiDir:        "/existing/wiki",
		}),
	)
	if m.locale != Ko {
		t.Errorf("expected Ko locale from existing config, got %q", m.locale)
	}
	if !m.rerun {
		t.Error("expected rerun to be true")
	}
	if m.existing == nil {
		t.Fatal("expected existing config to be set")
	}
	if m.existing.APIKey != "sk-existing" {
		t.Errorf("expected sk-existing, got %q", m.existing.APIKey)
	}
}

func TestModel_RerunMode_WelcomeShowsReconfigure(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0", WithRerunMode())
	view := m.View()
	// The welcome view should contain the reconfigure indicator.
	if !strings.Contains(view, "Reconfiguration") && !strings.Contains(view, "재설정") {
		t.Error("expected rerun mode indicator in welcome view")
	}
}

func TestModel_RerunMode_ExistingAPIKey_Kept(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0",
		WithRerunMode(),
		WithExistingConfig(ExistingConfig{APIKey: "sk-keep-me"}),
	)
	m.step = StepAPIKey
	m.apikey = m.newAPIKeyModel()

	// Simulate pressing enter with empty input (keep existing key).
	updated, cmd := m.apikey.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.apikey = updated.(APIKeyModel)
	if cmd == nil {
		t.Fatal("expected cmd from enter")
	}
	msg := cmd()
	done, ok := msg.(APIKeyDoneMsg)
	if !ok {
		t.Fatal("expected APIKeyDoneMsg")
	}
	if done.Key != "sk-keep-me" {
		t.Errorf("expected existing key to be kept, got %q", done.Key)
	}
}

func TestModel_RerunMode_PermissionPreselected(t *testing.T) {
	ec := ExistingConfig{PermissionMode: "plan"}
	m := New("/tmp/config.yaml", "0.3.0",
		WithRerunMode(),
		WithExistingConfig(ec),
	)
	pm := m.newPermissionModel()
	// "plan" is index 2 in permModes (default=0, accept_edits=1, plan=2, bypass=3).
	if pm.cursor != 2 {
		t.Errorf("expected cursor at index 2 (plan), got %d", pm.cursor)
	}
}


func TestModel_FullFlowEndToEnd(t *testing.T) {
	m := New("/tmp/config.yaml", "0.3.0")

	// Welcome → Language
	updated, _ := m.Update(WelcomeDoneMsg{Path: PathFull})
	m = updated.(Model)
	if m.step != StepLanguage {
		t.Fatalf("expected StepLanguage, got %d", m.step)
	}

	// Language → APIKey
	updated, _ = m.Update(LanguageDoneMsg{Locale: En})
	m = updated.(Model)
	if m.step != StepAPIKey {
		t.Fatalf("expected StepAPIKey, got %d", m.step)
	}

	// APIKey → Permission (Full path)
	updated, _ = m.Update(APIKeyDoneMsg{Key: "sk-test-key"})
	m = updated.(Model)
	if m.step != StepPermission {
		t.Fatalf("expected StepPermission, got %d", m.step)
	}

	// Permission → MCP
	updated, _ = m.Update(PermissionDoneMsg{Mode: "default"})
	m = updated.(Model)
	if m.step != StepMCP {
		t.Fatalf("expected StepMCP, got %d", m.step)
	}

	// MCP → Directory
	updated, _ = m.Update(MCPDoneMsg{Servers: nil})
	m = updated.(Model)
	if m.step != StepDirectory {
		t.Fatalf("expected StepDirectory, got %d", m.step)
	}

	// Directory → Summary
	updated, _ = m.Update(DirectoryDoneMsg{DataDir: "/custom/data", WikiDir: "/custom/wiki"})
	m = updated.(Model)
	if m.step != StepSummary {
		t.Fatalf("expected StepSummary, got %d", m.step)
	}

	// Summary → SmokeTest
	updated, _ = m.Update(SummaryDoneMsg{})
	m = updated.(Model)
	if m.step != StepSmokeTest {
		t.Fatalf("expected StepSmokeTest, got %d", m.step)
	}

	// SmokeTest → Done
	updated, _ = m.Update(SmokeTestDoneMsg{})
	m = updated.(Model)
	if !m.Done() {
		t.Fatal("expected Done")
	}

	r := m.WizardResult()
	if r.Path != PathFull {
		t.Errorf("expected PathFull, got %q", r.Path)
	}
	if r.Locale != En {
		t.Errorf("expected En, got %q", r.Locale)
	}
	if r.APIKey != "sk-test-key" {
		t.Errorf("expected sk-test-key, got %q", r.APIKey)
	}
	if r.PermissionMode != "default" {
		t.Errorf("expected default permission, got %q", r.PermissionMode)
	}
	if r.DataDir != "/custom/data" {
		t.Errorf("expected /custom/data, got %q", r.DataDir)
	}
}
