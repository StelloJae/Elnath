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
	WikiDir        string
	DataDir        string
	PermissionMode string
	MCPServers     []MCPServerConfig
}

// RunOnboarding runs the interactive first-run setup.
// It prompts for API key, data directory, and wiki directory,
// then writes a config file and initializes directories.
// The reader and writer params allow testing with fake input/output.
func RunOnboarding(cfgPath string, reader io.Reader, writer io.Writer) (*OnboardingResult, error) {
	scanner := bufio.NewScanner(reader)

	fmt.Fprintln(writer, "Welcome to Elnath! Let's set up your environment.")
	fmt.Fprintln(writer)

	// API Key
	fmt.Fprint(writer, "Anthropic API key (ELNATH_ANTHROPIC_API_KEY): ")
	scanner.Scan()
	apiKey := strings.TrimSpace(scanner.Text())

	// Data directory
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("determine home directory: %w", err)
	}
	defaultDataDir := filepath.Join(home, ".elnath", "data")
	fmt.Fprintf(writer, "Data directory [%s]: ", defaultDataDir)
	scanner.Scan()
	dataDir := strings.TrimSpace(scanner.Text())
	if dataDir == "" {
		dataDir = defaultDataDir
	}

	// Wiki directory
	defaultWikiDir := filepath.Join(home, ".elnath", "wiki")
	fmt.Fprintf(writer, "Wiki directory [%s]: ", defaultWikiDir)
	scanner.Scan()
	wikiDir := strings.TrimSpace(scanner.Text())
	if wikiDir == "" {
		wikiDir = defaultWikiDir
	}

	result := &OnboardingResult{
		APIKey:  apiKey,
		WikiDir: wikiDir,
		DataDir: dataDir,
	}

	if err := WriteFromResult(cfgPath, result); err != nil {
		return nil, err
	}

	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Setup complete! Run 'elnath run' to start chatting.")

	return result, nil
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

	cfg := fmt.Sprintf(`# Elnath configuration
data_dir: %q
wiki_dir: %q
anthropic:
  api_key: %q
  model: claude-sonnet-4-20250514
permission:
  mode: %q
`, result.DataDir, result.WikiDir, result.APIKey, permMode)

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
