//go:build integration

package onboarding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunQuickstart_DetectsCodexOAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	authDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", authDir, err)
	}
	auth := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token": "test-token",
		},
	}
	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("Marshal auth error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile(auth.json) error = %v", err)
	}

	result, err := RunQuickstart(filepath.Join(home, ".elnath", "config.yaml"), "test-version")
	if err != nil {
		t.Fatalf("RunQuickstart error = %v", err)
	}
	if result.ProviderDetected != "codex" {
		t.Fatalf("ProviderDetected = %q, want codex", result.ProviderDetected)
	}
	if result.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty", result.APIKey)
	}

	metric := MetricRecord{
		SetupStartedAt:   time.Unix(100, 0).UTC(),
		SetupCompletedAt: time.Unix(102, 0).UTC(),
		DurationSec:      2,
		Steps: MetricSteps{
			Provider:  result.ProviderDetected,
			APIKey:    false,
			SmokeTest: result.SmokeTestPassed,
			DemoTask:  false,
		},
	}
	if err := WriteMetric(metric); err != nil {
		t.Fatalf("WriteMetric error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".elnath", "data", "onboarding_metric.json")); err != nil {
		t.Fatalf("Stat(metric file) error = %v", err)
	}
}
