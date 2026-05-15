package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdDoctorJSONReportsLocalReadiness(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("mkdir wiki: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgData := "data_dir: " + dataDir + "\n" +
		"wiki_dir: " + wikiDir + "\n" +
		"provider: openai_responses\n" +
		"openai_responses:\n" +
		"  api_key: test-key\n" +
		"  base_url: https://api.openai.com/v1\n" +
		"  model: gpt-5.5\n" +
		"  reasoning_effort: high\n" +
		"  timeout_seconds: 45\n" +
		"daemon:\n" +
		"  socket_path: " + filepath.Join(dir, "daemon.sock") + "\n" +
		"permission:\n  mode: default\n"
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("ELNATH_PROVIDER", "")
	t.Setenv("ELNATH_OPENAI_API_KEY", "")
	t.Setenv("ELNATH_OPENAI_RESPONSES_API_KEY", "")
	t.Setenv("ELNATH_ANTHROPIC_API_KEY", "")
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, stderr := captureOutput(t, func() {
		if err := executeCommand(context.Background(), "doctor", []string{"--json"}); err != nil {
			t.Fatalf("executeCommand(doctor --json): %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	var got doctorReport
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not doctor JSON: %v\n%s", err, stdout)
	}
	if got.Status != doctorStatusWarn {
		t.Fatalf("doctor status = %q, want warn because daemon is not running", got.Status)
	}
	checks := map[string]doctorCheck{}
	for _, check := range got.Checks {
		checks[check.Name] = check
	}
	for _, name := range []string{"config", "provider", "provider_proxy", "data_dir", "wiki_dir", "daemon_socket", "timeouts"} {
		if _, ok := checks[name]; !ok {
			t.Fatalf("doctor JSON missing check %q; got %+v", name, got.Checks)
		}
	}
	if checks["config"].Status != doctorStatusPass {
		t.Fatalf("config status = %q, want pass", checks["config"].Status)
	}
	if checks["provider"].Status != doctorStatusPass {
		t.Fatalf("provider status = %q, want pass", checks["provider"].Status)
	}
	if checks["provider_proxy"].Status != doctorStatusPass {
		t.Fatalf("provider_proxy status = %q, want pass", checks["provider_proxy"].Status)
	}
	if checks["daemon_socket"].Status != doctorStatusWarn {
		t.Fatalf("daemon_socket status = %q, want warn", checks["daemon_socket"].Status)
	}
}

func TestCmdDoctorReturnsErrorOnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgData := "data_dir: " + filepath.Join(dir, "data") + "\n" +
		"wiki_dir: " + filepath.Join(dir, "wiki") + "\n" +
		"locale: klingon\n"
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()

	stdout, stderr := captureOutput(t, func() {
		err := executeCommand(context.Background(), "doctor", []string{"--json"})
		if err == nil {
			t.Fatal("executeCommand(doctor --json) error = nil, want invalid config error")
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	var got doctorReport
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not doctor JSON: %v\n%s", err, stdout)
	}
	if got.Status != doctorStatusFail {
		t.Fatalf("doctor status = %q, want fail", got.Status)
	}
	if len(got.Checks) != 1 || got.Checks[0].Name != "config" || got.Checks[0].Status != doctorStatusFail {
		t.Fatalf("checks = %+v, want single failed config check", got.Checks)
	}
}

func TestCmdDoctorUsage(t *testing.T) {
	stdout, stderr := captureOutput(t, func() {
		if err := cmdDoctor(context.Background(), []string{"help"}); err != nil {
			t.Fatalf("cmdDoctor help: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Usage: elnath doctor") {
		t.Fatalf("stdout = %q, want doctor usage", stdout)
	}
}
