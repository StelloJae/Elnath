package onboarding

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// Step represents a wizard screen.
type Step int

const (
	StepWelcome Step = iota
	StepLanguage
	StepAPIKey
	StepPermission
	StepMCP
	StepDirectory
	StepSummary
	StepSmokeTest
	StepDone
)

// stepBackMsg is emitted by sub-models when the user presses Esc.
type stepBackMsg struct{}

// Result holds the wizard's collected choices.
type Result struct {
	Path           PathChoice
	Locale         Locale
	APIKey         string
	PermissionMode string
	MCPServers     []MCPSelection
	DataDir        string
	WikiDir        string
}

// Option configures the root model.
type Option func(*Model)

// ExistingConfig holds values from a previous configuration for rerun mode.
type ExistingConfig struct {
	Locale         Locale
	APIKey         string
	PermissionMode string
	DataDir        string
	WikiDir        string
}

// WithRerunMode marks this as a re-run from `elnath setup` (E3 prep).
func WithRerunMode() Option {
	return func(m *Model) {
		m.rerun = true
	}
}

// WithExistingConfig provides previous config values for default display in rerun mode.
func WithExistingConfig(ec ExistingConfig) Option {
	return func(m *Model) {
		m.existing = &ec
	}
}

// Model is the root Bubbletea model that orchestrates wizard steps.
type Model struct {
	cfgPath   string
	version   string
	rerun     bool
	existing  *ExistingConfig
	step      Step
	locale    Locale
	result    Result
	welcome    WelcomeModel
	language   LanguageModel
	apikey     APIKeyModel
	permission PermissionModel
	mcp        MCPModel
	directory  DirectoryModel
	summary    SummaryModel
	smoketest  SmokeTestModel
	hasNpm     bool
	err        error
}

// New creates a new onboarding wizard model.
func New(cfgPath, version string, opts ...Option) Model {
	locale := En
	m := Model{
		cfgPath: cfgPath,
		version: version,
		step:    StepWelcome,
		locale:  locale,
		hasNpm:  DetectNpm(),
	}
	for _, opt := range opts {
		opt(&m)
	}
	// Apply existing config locale in rerun mode.
	if m.rerun && m.existing != nil && m.existing.Locale != "" {
		m.locale = m.existing.Locale
	}
	m.welcome = NewWelcomeModel(m.locale, version, m.rerun)
	m.language = NewLanguageModel(m.locale)
	return m
}

func (m Model) Init() tea.Cmd {
	return m.welcome.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case stepBackMsg:
		return m.goBack()

	case WelcomeDoneMsg:
		m.result.Path = msg.Path
		m.step = StepLanguage
		m.language = NewLanguageModel(m.locale)
		return m, m.language.Init()

	case LanguageDoneMsg:
		m.locale = msg.Locale
		m.result.Locale = msg.Locale
		m.step = StepAPIKey
		m.apikey = m.newAPIKeyModel()
		return m, m.apikey.Init()

	case APIKeyDoneMsg:
		m.result.APIKey = msg.Key
		return m.afterAPIKey()

	case PermissionDoneMsg:
		m.result.PermissionMode = msg.Mode
		return m.afterPermission()

	case MCPDoneMsg:
		m.result.MCPServers = msg.Servers
		return m.afterMCP()

	case DirectoryDoneMsg:
		m.result.DataDir = msg.DataDir
		m.result.WikiDir = msg.WikiDir
		return m.afterDirectory()

	case SummaryDoneMsg:
		return m.afterSummary()

	case SummaryEditMsg:
		m.step = msg.Step
		switch msg.Step {
		case StepAPIKey:
			m.apikey = m.newAPIKeyModel()
			return m, m.apikey.Init()
		case StepPermission:
			m.permission = m.newPermissionModel()
			return m, m.permission.Init()
		case StepMCP:
			m.mcp = NewMCPModel(m.locale, m.hasNpm)
			return m, m.mcp.Init()
		case StepDirectory:
			m.directory = m.newDirectoryModel()
			return m, m.directory.Init()
		default:
			m.apikey = m.newAPIKeyModel()
			return m, m.apikey.Init()
		}

	case SmokeTestDoneMsg:
		m.step = StepDone
		return m, tea.Quit
	}

	return m.updateCurrentStep(msg)
}

func (m Model) View() string {
	var content string
	switch m.step {
	case StepWelcome:
		content = m.welcome.View()
	case StepLanguage:
		content = m.language.View()
	case StepAPIKey:
		content = m.apikey.View()
	case StepPermission:
		content = m.permission.View()
	case StepMCP:
		content = m.mcp.View()
	case StepDirectory:
		content = m.directory.View()
	case StepSummary:
		content = m.summary.View()
	case StepSmokeTest:
		content = m.smoketest.View()
	case StepDone:
		return ""
	default:
		return ""
	}

	quick := m.result.Path == PathQuick
	progress := RenderProgress(m.locale, m.step, quick)
	if progress != "" {
		return progress + "\n" + content
	}
	return content
}

// Done returns true when the wizard has completed.
func (m Model) Done() bool {
	return m.step == StepDone
}

// WizardResult returns the collected wizard choices.
func (m Model) WizardResult() Result {
	return m.result
}

// Err returns any error that occurred during the wizard.
func (m Model) Err() error {
	return m.err
}

// afterAPIKey routes to the next step based on the selected path.
// Quick: skip to Done with defaults.
// Full: go to Permission step.
func (m Model) afterAPIKey() (tea.Model, tea.Cmd) {
	switch m.result.Path {
	case PathQuick:
		home, _ := os.UserHomeDir()
		base := filepath.Join(home, ".elnath")
		m.result.DataDir = filepath.Join(base, "data")
		m.result.WikiDir = filepath.Join(base, "wiki")
		m.result.PermissionMode = "default"
		m.step = StepSummary
		m.summary = NewSummaryModel(m.locale, m.result, true)
		return m, m.summary.Init()
	default:
		m.step = StepPermission
		m.permission = m.newPermissionModel()
		return m, m.permission.Init()
	}
}

// afterPermission routes to MCP catalog (Full path only).
func (m Model) afterPermission() (tea.Model, tea.Cmd) {
	m.step = StepMCP
	m.mcp = NewMCPModel(m.locale, m.hasNpm)
	return m, m.mcp.Init()
}

// afterMCP routes to Directory step (Full path only).
func (m Model) afterMCP() (tea.Model, tea.Cmd) {
	m.step = StepDirectory
	m.directory = m.newDirectoryModel()
	return m, m.directory.Init()
}

// afterDirectory routes to Summary step (Full path only).
func (m Model) afterDirectory() (tea.Model, tea.Cmd) {
	m.step = StepSummary
	m.summary = NewSummaryModel(m.locale, m.result, m.result.Path == PathQuick)
	return m, m.summary.Init()
}

// afterSummary routes to SmokeTest step.
func (m Model) afterSummary() (tea.Model, tea.Cmd) {
	m.step = StepSmokeTest
	m.smoketest = NewSmokeTestModel(m.locale, m.result.APIKey)
	return m, m.smoketest.Init()
}

// goBack moves to the previous step.
func (m Model) goBack() (tea.Model, tea.Cmd) {
	switch m.step {
	case StepLanguage:
		m.step = StepWelcome
		m.welcome = NewWelcomeModel(m.locale, m.version, m.rerun)
		return m, m.welcome.Init()
	case StepAPIKey:
		m.step = StepLanguage
		m.language = NewLanguageModel(m.locale)
		return m, m.language.Init()
	case StepPermission:
		m.step = StepAPIKey
		m.apikey = m.newAPIKeyModel()
		return m, m.apikey.Init()
	case StepMCP:
		m.step = StepPermission
		m.permission = m.newPermissionModel()
		return m, m.permission.Init()
	case StepDirectory:
		m.step = StepMCP
		m.mcp = NewMCPModel(m.locale, m.hasNpm)
		return m, m.mcp.Init()
	case StepSummary:
		if m.result.Path == PathQuick {
			m.step = StepAPIKey
			m.apikey = m.newAPIKeyModel()
			return m, m.apikey.Init()
		}
		m.step = StepDirectory
		m.directory = m.newDirectoryModel()
		return m, m.directory.Init()
	case StepSmokeTest:
		m.step = StepSummary
		m.summary = NewSummaryModel(m.locale, m.result, m.result.Path == PathQuick)
		return m, m.summary.Init()
	}
	return m, nil
}

// updateCurrentStep delegates the message to the active step's sub-model.
func (m Model) updateCurrentStep(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch m.step {
	case StepWelcome:
		updated, c := m.welcome.Update(msg)
		wm, ok := updated.(WelcomeModel)
		if !ok {
			m.err = fmt.Errorf("unexpected model type from welcome update: %T", updated)
			return m, tea.Quit
		}
		m.welcome = wm
		cmd = c
	case StepLanguage:
		updated, c := m.language.Update(msg)
		lm, ok := updated.(LanguageModel)
		if !ok {
			m.err = fmt.Errorf("unexpected model type from language update: %T", updated)
			return m, tea.Quit
		}
		m.language = lm
		cmd = c
	case StepAPIKey:
		updated, c := m.apikey.Update(msg)
		am, ok := updated.(APIKeyModel)
		if !ok {
			m.err = fmt.Errorf("unexpected model type from apikey update: %T", updated)
			return m, tea.Quit
		}
		m.apikey = am
		cmd = c
	case StepPermission:
		updated, c := m.permission.Update(msg)
		pm, ok := updated.(PermissionModel)
		if !ok {
			m.err = fmt.Errorf("unexpected model type from permission update: %T", updated)
			return m, tea.Quit
		}
		m.permission = pm
		cmd = c
	case StepMCP:
		updated, c := m.mcp.Update(msg)
		mm, ok := updated.(MCPModel)
		if !ok {
			m.err = fmt.Errorf("unexpected model type from mcp update: %T", updated)
			return m, tea.Quit
		}
		m.mcp = mm
		cmd = c
	case StepDirectory:
		updated, c := m.directory.Update(msg)
		dm, ok := updated.(DirectoryModel)
		if !ok {
			m.err = fmt.Errorf("unexpected model type from directory update: %T", updated)
			return m, tea.Quit
		}
		m.directory = dm
		cmd = c
	case StepSummary:
		updated, c := m.summary.Update(msg)
		sm, ok := updated.(SummaryModel)
		if !ok {
			m.err = fmt.Errorf("unexpected model type from summary update: %T", updated)
			return m, tea.Quit
		}
		m.summary = sm
		cmd = c
	case StepSmokeTest:
		updated, c := m.smoketest.Update(msg)
		st, ok := updated.(SmokeTestModel)
		if !ok {
			m.err = fmt.Errorf("unexpected model type from smoketest update: %T", updated)
			return m, tea.Quit
		}
		m.smoketest = st
		cmd = c
	}

	return m, cmd
}

// newAPIKeyModel creates an APIKeyModel with existing key if in rerun mode.
func (m Model) newAPIKeyModel() APIKeyModel {
	if m.existing != nil {
		return NewAPIKeyModel(m.locale, m.existing.APIKey)
	}
	return NewAPIKeyModel(m.locale)
}

// newPermissionModel creates a PermissionModel with existing mode if in rerun mode.
func (m Model) newPermissionModel() PermissionModel {
	if m.existing != nil {
		return NewPermissionModel(m.locale, m.existing.PermissionMode)
	}
	return NewPermissionModel(m.locale)
}

// newDirectoryModel creates a DirectoryModel with existing dirs if in rerun mode.
func (m Model) newDirectoryModel() DirectoryModel {
	if m.existing != nil {
		return NewDirectoryModel(m.locale, m.existing.DataDir, m.existing.WikiDir)
	}
	return NewDirectoryModel(m.locale)
}

// Run starts the onboarding wizard and returns the user's choices.
// This is the public entry point called from cmdRun.
func Run(cfgPath, version string, opts ...Option) (*Result, error) {
	m := New(cfgPath, version, opts...)
	p := tea.NewProgram(m, tea.WithAltScreen())

	finalModel, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("onboarding wizard: %w", err)
	}

	final, ok := finalModel.(Model)
	if !ok {
		return nil, fmt.Errorf("onboarding wizard: unexpected model type")
	}

	if !final.Done() {
		return nil, fmt.Errorf("onboarding wizard: cancelled by user")
	}

	if final.Err() != nil {
		return nil, final.Err()
	}

	result := final.WizardResult()
	return &result, nil
}
