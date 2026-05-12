package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCmdExplainTimeoutsJSON(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.yaml")
	dataDir := filepath.Join(root, "data")
	wikiDir := filepath.Join(root, "wiki")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(data): %v", err)
	}
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(wiki): %v", err)
	}
	raw := "data_dir: " + dataDir + "\n" +
		"wiki_dir: " + wikiDir + "\n" +
		"locale: en\n" +
		"anthropic:\n  timeout_seconds: 90\n" +
		"openai:\n  timeout_seconds: 45\n" +
		"openai_responses:\n  timeout_seconds: 77\n" +
		"daemon:\n" +
		"  inactivity_timeout_seconds: 12\n" +
		"  wall_clock_timeout_seconds: 34\n" +
		"  max_recoveries: 5\n" +
		"  workspace_retention: keep\n" +
		"self_healing:\n" +
		"  enabled: true\n" +
		"  observe_only: false\n" +
		"  timeout_seconds: 17\n" +
		"  completion_retry_max: 1\n" +
		"telegram:\n  poll_timeout_seconds: 11\n"
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	withArgs(t, []string{"elnath", "--config", cfgPath})

	stdout, _ := captureOutput(t, func() {
		if err := cmdExplain(context.Background(), []string{"timeouts", "--json"}); err != nil {
			t.Fatalf("cmdExplain(timeouts --json): %v", err)
		}
	})

	var out struct {
		ProviderRequestTimeouts []struct {
			Provider       string `json:"provider"`
			ConfigKey      string `json:"config_key"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		} `json:"provider_request_timeouts"`
		Daemon struct {
			InactivityTimeoutSeconds int    `json:"inactivity_timeout_seconds"`
			WallClockTimeoutSeconds  int    `json:"wall_clock_timeout_seconds"`
			MaxRecoveries            int    `json:"max_recoveries"`
			WorkspaceRetention       string `json:"workspace_retention"`
		} `json:"daemon"`
		SelfHealing struct {
			Enabled            bool `json:"enabled"`
			ObserveOnly        bool `json:"observe_only"`
			TimeoutSeconds     int  `json:"timeout_seconds"`
			CompletionRetryMax int  `json:"completion_retry_max"`
		} `json:"self_healing"`
		Telegram struct {
			PollTimeoutSeconds int `json:"poll_timeout_seconds"`
		} `json:"telegram"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}

	seen := map[string]int{}
	for _, entry := range out.ProviderRequestTimeouts {
		seen[entry.Provider] = entry.TimeoutSeconds
		if entry.ConfigKey == "" {
			t.Fatalf("provider timeout entry missing config_key: %+v", entry)
		}
	}
	if seen["anthropic"] != 90 || seen["openai"] != 45 || seen["openai_responses"] != 77 || seen["codex_oauth"] != 77 {
		t.Fatalf("provider timeouts = %+v, want anthropic/openai/openai_responses/codex_oauth values", seen)
	}
	if out.Daemon.InactivityTimeoutSeconds != 12 || out.Daemon.WallClockTimeoutSeconds != 34 || out.Daemon.MaxRecoveries != 5 || out.Daemon.WorkspaceRetention != "keep" {
		t.Fatalf("daemon policy = %+v, want configured daemon timeout policy", out.Daemon)
	}
	if !out.SelfHealing.Enabled || out.SelfHealing.ObserveOnly || out.SelfHealing.TimeoutSeconds != 17 || out.SelfHealing.CompletionRetryMax != 1 {
		t.Fatalf("self_healing policy = %+v, want configured self-healing timeout policy", out.SelfHealing)
	}
	if out.Telegram.PollTimeoutSeconds != 11 {
		t.Fatalf("telegram poll timeout = %d, want 11", out.Telegram.PollTimeoutSeconds)
	}
}
