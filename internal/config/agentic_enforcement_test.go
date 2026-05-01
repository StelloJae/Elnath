package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgenticEnforcementConfig_DefaultsToObservePassThrough(t *testing.T) {
	cfg := DefaultConfig()

	if got := cfg.Agentic.Enforcement.Mode; got != AgenticEnforcementModeObserve {
		t.Fatalf("agentic.enforcement.mode = %q, want %q", got, AgenticEnforcementModeObserve)
	}
	if got := cfg.Agentic.CompletionGate.Mode; got != AgenticCompletionGateModeObserve {
		t.Fatalf("agentic.completion_gate.mode = %q, want %q", got, AgenticCompletionGateModeObserve)
	}
}

func TestAgenticEnforcementConfig_LoadGatewayMode(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
data_dir: `+filepath.Join(dir, "data")+`
wiki_dir: `+wikiDir+`
permission:
  mode: bypass
agentic:
  enforcement:
    mode: gateway
  completion_gate:
    mode: verification
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Agentic.Enforcement.Mode; got != AgenticEnforcementModeGateway {
		t.Fatalf("agentic.enforcement.mode = %q, want %q", got, AgenticEnforcementModeGateway)
	}
	if got := cfg.Agentic.CompletionGate.Mode; got != AgenticCompletionGateModeVerification {
		t.Fatalf("agentic.completion_gate.mode = %q, want %q", got, AgenticCompletionGateModeVerification)
	}
}

func TestAgenticEnforcementConfig_RejectsUnknownMode(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfg := &Config{
		DataDir: filepath.Join(dir, "data"),
		WikiDir: wikiDir,
		Permission: PermissionConfig{
			Mode: "default",
		},
		Agentic: AgenticConfig{
			Enforcement: AgenticEnforcementConfig{Mode: "global"},
		},
	}

	err := validate(cfg)
	if err == nil {
		t.Fatal("validate error = nil, want unsupported agentic enforcement mode")
	}
	if !strings.Contains(err.Error(), "agentic.enforcement.mode") {
		t.Fatalf("validate error = %q, want agentic.enforcement.mode", err.Error())
	}
}

func TestAgenticCompletionGateConfig_RejectsUnknownMode(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfg := &Config{
		DataDir: filepath.Join(dir, "data"),
		WikiDir: wikiDir,
		Permission: PermissionConfig{
			Mode: "default",
		},
		Agentic: AgenticConfig{
			Enforcement:    AgenticEnforcementConfig{Mode: AgenticEnforcementModeObserve},
			CompletionGate: AgenticCompletionGateConfig{Mode: "global"},
		},
	}

	err := validate(cfg)
	if err == nil {
		t.Fatal("validate error = nil, want unsupported completion gate mode")
	}
	if !strings.Contains(err.Error(), "agentic.completion_gate.mode") {
		t.Fatalf("validate error = %q, want agentic.completion_gate.mode", err.Error())
	}
}
