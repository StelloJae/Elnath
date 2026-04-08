package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeedsOnboarding_NoConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	if !NeedsOnboarding(cfgPath) {
		t.Error("expected NeedsOnboarding to return true when config file does not exist")
	}
}

func TestNeedsOnboarding_ConfigExists(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(cfgPath, []byte("data_dir: /tmp\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if NeedsOnboarding(cfgPath) {
		t.Error("expected NeedsOnboarding to return false when config file exists")
	}
}

func TestRunOnboarding_AllDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Empty lines for data dir and wiki dir to accept defaults.
	input := "sk-ant-test-key\n\n\n"
	var out bytes.Buffer

	result, err := RunOnboarding(cfgPath, strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("RunOnboarding failed: %v", err)
	}

	if result.APIKey != "sk-ant-test-key" {
		t.Errorf("expected APIKey %q, got %q", "sk-ant-test-key", result.APIKey)
	}
	if result.DataDir == "" {
		t.Error("expected DataDir to be set")
	}
	if result.WikiDir == "" {
		t.Error("expected WikiDir to be set")
	}

	// Verify config file was created.
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("config file not created: %v", err)
	}
}

func TestRunOnboarding_CustomPaths(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	customData := filepath.Join(dir, "mydata")
	customWiki := filepath.Join(dir, "mywiki")

	input := "sk-ant-custom\n" + customData + "\n" + customWiki + "\n"
	var out bytes.Buffer

	result, err := RunOnboarding(cfgPath, strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("RunOnboarding failed: %v", err)
	}

	if result.APIKey != "sk-ant-custom" {
		t.Errorf("expected APIKey %q, got %q", "sk-ant-custom", result.APIKey)
	}
	if result.DataDir != customData {
		t.Errorf("expected DataDir %q, got %q", customData, result.DataDir)
	}
	if result.WikiDir != customWiki {
		t.Errorf("expected WikiDir %q, got %q", customWiki, result.WikiDir)
	}

	// Verify config contents include custom paths.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, customData) {
		t.Errorf("config missing custom data_dir %q", customData)
	}
	if !strings.Contains(content, customWiki) {
		t.Errorf("config missing custom wiki_dir %q", customWiki)
	}
	if !strings.Contains(content, "sk-ant-custom") {
		t.Error("config missing api_key")
	}
}

func TestRunOnboarding_CreatesDirs(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")

	input := "mykey\n" + dataDir + "\n" + wikiDir + "\n"
	var out bytes.Buffer

	if _, err := RunOnboarding(cfgPath, strings.NewReader(input), &out); err != nil {
		t.Fatalf("RunOnboarding failed: %v", err)
	}

	info, err := os.Stat(dataDir)
	if err != nil || !info.IsDir() {
		t.Errorf("expected dataDir %q to be created", dataDir)
	}
	info, err = os.Stat(wikiDir)
	if err != nil || !info.IsDir() {
		t.Errorf("expected wikiDir %q to be created", wikiDir)
	}
}

func TestRunOnboarding_CreatesGettingStartedPage(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	wikiDir := filepath.Join(dir, "wiki")

	input := "mykey\n" + filepath.Join(dir, "data") + "\n" + wikiDir + "\n"
	var out bytes.Buffer

	if _, err := RunOnboarding(cfgPath, strings.NewReader(input), &out); err != nil {
		t.Fatalf("RunOnboarding failed: %v", err)
	}

	gsPath := filepath.Join(wikiDir, "getting-started.md")
	data, err := os.ReadFile(gsPath)
	if err != nil {
		t.Fatalf("getting-started.md not created: %v", err)
	}
	if !strings.Contains(string(data), "Getting Started") {
		t.Error("getting-started.md missing expected content")
	}
}

func TestRunOnboarding_ConfigFilePermissions(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	input := "mykey\n\n\n"
	var out bytes.Buffer

	if _, err := RunOnboarding(cfgPath, strings.NewReader(input), &out); err != nil {
		t.Fatalf("RunOnboarding failed: %v", err)
	}

	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected config permissions 0600, got %04o", perm)
	}
}
