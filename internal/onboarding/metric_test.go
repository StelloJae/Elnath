package onboarding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/config"
)

func TestWriteMetric_CreatesAndOverwritesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	first := MetricRecord{
		SetupStartedAt:   time.Unix(100, 0).UTC(),
		SetupCompletedAt: time.Unix(160, 0).UTC(),
		DurationSec:      60,
		Steps: MetricSteps{
			Provider:  "anthropic",
			APIKey:    true,
			SmokeTest: true,
			DemoTask:  false,
		},
	}
	if err := WriteMetric(first); err != nil {
		t.Fatalf("WriteMetric(first) error = %v", err)
	}

	path := filepath.Join(config.DefaultDataDir(), "onboarding_metric.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var got MetricRecord
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal metric error = %v", err)
	}
	if got != first {
		t.Fatalf("metric = %#v, want %#v", got, first)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("metric file perm = %#o, want %#o", perm, 0o600)
	}

	second := MetricRecord{
		SetupStartedAt:   time.Unix(200, 0).UTC(),
		SetupCompletedAt: time.Unix(205, 0).UTC(),
		DurationSec:      5,
		Steps: MetricSteps{
			Provider:  "codex",
			APIKey:    false,
			SmokeTest: false,
			DemoTask:  true,
		},
	}
	if err := WriteMetric(second); err != nil {
		t.Fatalf("WriteMetric(second) error = %v", err)
	}

	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) after overwrite error = %v", path, err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal overwritten metric error = %v", err)
	}
	if got != second {
		t.Fatalf("overwritten metric = %#v, want %#v", got, second)
	}
}
