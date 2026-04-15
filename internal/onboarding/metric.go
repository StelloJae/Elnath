package onboarding

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/config"
)

// MetricRecord captures the onboarding timeline for local diagnostics.
type MetricRecord struct {
	SetupStartedAt   time.Time   `json:"setup_started_at"`
	SetupCompletedAt time.Time   `json:"setup_completed_at"`
	DurationSec      int         `json:"duration_sec"`
	Steps            MetricSteps `json:"steps"`
}

type MetricSteps struct {
	Provider  string `json:"provider"`
	APIKey    bool   `json:"api_key"`
	SmokeTest bool   `json:"smoke_test"`
	DemoTask  bool   `json:"demo_task"`
}

// WriteMetric persists the record to config.DefaultDataDir()/onboarding_metric.json.
func WriteMetric(rec MetricRecord) error {
	dir := config.DefaultDataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("onboarding metric: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("onboarding metric: marshal: %w", err)
	}
	path := filepath.Join(dir, "onboarding_metric.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("onboarding metric: write: %w", err)
	}
	return nil
}
