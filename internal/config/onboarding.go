package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// NeedsOnboarding returns true if the config file does not exist yet.
func NeedsOnboarding(cfgPath string) bool {
	_, err := os.Stat(cfgPath)
	return os.IsNotExist(err)
}

// OnboardingResult holds the user's choices from the onboarding flow.
type OnboardingResult struct {
	Provider                       string
	APIKey                         string
	OpenAIResponsesAPIKey          string
	OpenAIResponsesBaseURL         string
	OpenAIResponsesModel           string
	OpenAIResponsesReasoningEffort string
	Locale                         string
	WikiDir                        string
	DataDir                        string
	PermissionMode                 string
	MCPServers                     []MCPServerConfig
}

// RunOnboarding runs the text-based first-run setup for non-interactive environments.
// Environment variables take priority: ELNATH_OPENAI_RESPONSES_API_KEY,
// ELNATH_ANTHROPIC_API_KEY, ELNATH_DATA_DIR, ELNATH_WIKI_DIR,
// ELNATH_PERMISSION_MODE, ELNATH_LOCALE.
// If reader is nil (fully non-interactive), only env vars and defaults are used.
func RunOnboarding(cfgPath string, reader io.Reader, writer io.Writer) (*OnboardingResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("determine home directory: %w", err)
	}
	defaultDataDir := filepath.Join(home, ".elnath", "data")
	defaultWikiDir := filepath.Join(home, ".elnath", "wiki")

	// Start from env vars.
	provider := ""
	apiKey := os.Getenv("ELNATH_ANTHROPIC_API_KEY")
	openAIResponsesAPIKey := os.Getenv("ELNATH_OPENAI_RESPONSES_API_KEY")
	openAIResponsesBaseURL := os.Getenv("ELNATH_OPENAI_RESPONSES_BASE_URL")
	openAIResponsesModel := os.Getenv("ELNATH_OPENAI_RESPONSES_MODEL")
	openAIResponsesReasoningEffort := os.Getenv("ELNATH_OPENAI_RESPONSES_REASONING_EFFORT")
	if openAIResponsesAPIKey != "" {
		provider = "openai_responses"
		apiKey = openAIResponsesAPIKey
	} else if apiKey != "" {
		provider = "anthropic"
	}
	dataDir := os.Getenv("ELNATH_DATA_DIR")
	wikiDir := os.Getenv("ELNATH_WIKI_DIR")
	permMode := os.Getenv("ELNATH_PERMISSION_MODE")
	locale := os.Getenv("ELNATH_LOCALE")

	// Fill defaults.
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	if wikiDir == "" {
		wikiDir = defaultWikiDir
	}

	// If reader is available, prompt for missing values interactively.
	if reader != nil {
		scanner := bufio.NewScanner(reader)

		fmt.Fprintln(writer, "Welcome to Elnath! Let's set up your environment.")
		fmt.Fprintln(writer)

		if apiKey == "" {
			fmt.Fprint(writer, "OpenAI Responses-compatible API key or Anthropic API key: ")
			scanner.Scan()
			apiKey = strings.TrimSpace(scanner.Text())
			provider = detectOnboardingProviderFromKey(apiKey)
			if provider == "openai_responses" {
				openAIResponsesAPIKey = apiKey
			}
		}

		fmt.Fprintf(writer, "Data directory [%s]: ", dataDir)
		scanner.Scan()
		if v := strings.TrimSpace(scanner.Text()); v != "" {
			dataDir = v
		}

		fmt.Fprintf(writer, "Wiki directory [%s]: ", wikiDir)
		scanner.Scan()
		if v := strings.TrimSpace(scanner.Text()); v != "" {
			wikiDir = v
		}
	}

	result := &OnboardingResult{
		Provider:                       provider,
		APIKey:                         apiKey,
		OpenAIResponsesAPIKey:          openAIResponsesAPIKey,
		OpenAIResponsesBaseURL:         openAIResponsesBaseURL,
		OpenAIResponsesModel:           openAIResponsesModel,
		OpenAIResponsesReasoningEffort: openAIResponsesReasoningEffort,
		Locale:                         locale,
		WikiDir:                        wikiDir,
		DataDir:                        dataDir,
		PermissionMode:                 permMode,
	}

	if err := WriteFromResult(cfgPath, result); err != nil {
		return nil, err
	}

	if writer != nil {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "Setup complete! Run 'elnath run' to start chatting.")
	}

	return result, nil
}

// RunNonInteractiveOnboarding creates a config purely from env vars and defaults.
// Used when --non-interactive flag is set or stdin is not a TTY.
func RunNonInteractiveOnboarding(cfgPath string) (*OnboardingResult, error) {
	return RunOnboarding(cfgPath, nil, io.Discard)
}

func detectOnboardingProviderFromKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	if strings.HasPrefix(apiKey, "sk-ant-") {
		return "anthropic"
	}
	return "openai_responses"
}

// WriteFromResult persists an OnboardingResult to disk: creates directories,
// writes config.yaml, and creates the getting-started wiki page.
// Shared by both the interactive TUI wizard and the legacy text-based onboarding.
func WriteFromResult(cfgPath string, result *OnboardingResult) error {
	if err := os.MkdirAll(result.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := os.MkdirAll(result.WikiDir, 0o755); err != nil {
		return fmt.Errorf("create wiki dir: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	permMode := result.PermissionMode
	if permMode == "" {
		permMode = "default"
	}
	locale := result.Locale
	if locale == "" {
		locale = "en"
	}
	existingPrincipalUserID := ""
	if existing, err := Load(cfgPath); err == nil {
		existingPrincipalUserID = strings.TrimSpace(existing.Principal.UserID)
	}

	cfg := fmt.Sprintf(`# Elnath configuration
data_dir: %q
wiki_dir: %q
locale: %q
`, result.DataDir, result.WikiDir, locale)

	if usesOpenAIResponsesOnboarding(result) {
		apiKey := result.OpenAIResponsesAPIKey
		if apiKey == "" {
			apiKey = result.APIKey
		}
		cfg += fmt.Sprintf(`provider: "openai_responses"
openai_responses:
  api_key: %q
`, apiKey)
		if result.OpenAIResponsesBaseURL != "" {
			cfg += fmt.Sprintf("  base_url: %q\n", result.OpenAIResponsesBaseURL)
		}
		if result.OpenAIResponsesModel != "" {
			cfg += fmt.Sprintf("  model: %q\n", result.OpenAIResponsesModel)
		}
		if result.OpenAIResponsesReasoningEffort != "" {
			cfg += fmt.Sprintf("  reasoning_effort: %q\n", result.OpenAIResponsesReasoningEffort)
		}
	} else {
		cfg += fmt.Sprintf(`anthropic:
  api_key: %q
  model: claude-sonnet-4-6
`, result.APIKey)
	}

	cfg += fmt.Sprintf(`reasoning:
  effort_mode: auto
permission:
  mode: %q
`, permMode)
	if existingPrincipalUserID != "" {
		cfg += fmt.Sprintf("principal:\n  user_id: %q\n", existingPrincipalUserID)
	}

	if len(result.MCPServers) > 0 {
		cfg += "mcp_servers:\n"
		for _, s := range result.MCPServers {
			cfg += fmt.Sprintf("  - name: %q\n    command: %q\n", s.Name, s.Command)
			if len(s.Args) > 0 {
				cfg += "    args:\n"
				for _, a := range s.Args {
					cfg += fmt.Sprintf("      - %q\n", a)
				}
			}
		}
	}

	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	gettingStarted := filepath.Join(result.WikiDir, "getting-started.md")
	if _, err := os.Stat(gettingStarted); os.IsNotExist(err) {
		content := `---
title: Getting Started
type: entity
tags: [elnath, setup]
---

Welcome to your Elnath wiki! This is your personal knowledge base.

Use wiki tools to create, search, and manage pages.
`
		_ = os.WriteFile(gettingStarted, []byte(content), 0o644)
	}

	return nil
}

func usesOpenAIResponsesOnboarding(result *OnboardingResult) bool {
	if result == nil {
		return false
	}
	if NormalizeProviderName(result.Provider) == "openai-responses" {
		return true
	}
	return strings.TrimSpace(result.OpenAIResponsesAPIKey) != ""
}
