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
	APIKey         string
	Locale         string
	WikiDir        string
	DataDir        string
	PermissionMode string
	MCPServers     []MCPServerConfig
}

// RunOnboarding runs the text-based first-run setup for non-interactive environments.
// Environment variables take priority: ELNATH_ANTHROPIC_API_KEY, ELNATH_DATA_DIR,
// ELNATH_WIKI_DIR, ELNATH_PERMISSION_MODE, ELNATH_LOCALE.
// If reader is nil (fully non-interactive), only env vars and defaults are used.
func RunOnboarding(cfgPath string, reader io.Reader, writer io.Writer) (*OnboardingResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("determine home directory: %w", err)
	}
	defaultDataDir := filepath.Join(home, ".elnath", "data")
	defaultWikiDir := filepath.Join(home, ".elnath", "wiki")

	// Start from env vars.
	apiKey := os.Getenv("ELNATH_ANTHROPIC_API_KEY")
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
			fmt.Fprint(writer, "Anthropic API key (ELNATH_ANTHROPIC_API_KEY): ")
			scanner.Scan()
			apiKey = strings.TrimSpace(scanner.Text())
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
		APIKey:         apiKey,
		Locale:         locale,
		WikiDir:        wikiDir,
		DataDir:        dataDir,
		PermissionMode: permMode,
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
anthropic:
  api_key: %q
  model: claude-sonnet-4-20250514
permission:
  mode: %q
`, result.DataDir, result.WikiDir, locale, result.APIKey, permMode)
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
