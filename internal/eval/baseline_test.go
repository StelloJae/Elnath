package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineRunPlan(t *testing.T) {
	plan := NewBaselineRunPlan("benchmarks/public-corpus.v1.json")
	if plan.Baseline == "" || plan.CommandTemplate == "" || plan.OutputPath == "" {
		t.Fatalf("incomplete plan: %+v", plan)
	}
	if len(plan.RequiredEnv) == 0 {
		t.Fatal("expected required env")
	}

	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := WriteBaselineRunPlan(path, plan); err != nil {
		t.Fatalf("WriteBaselineRunPlan: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"baseline": "claude-codex-omx-omc"`) {
		t.Fatalf("unexpected file contents: %s", string(data))
	}
	if !strings.Contains(string(data), `"runtime_policy": ""`) {
		t.Fatalf("expected runtime_policy placeholder in scaffold: %s", string(data))
	}
}

func TestCurrentRunPlan(t *testing.T) {
	plan := NewCurrentRunPlan("benchmarks/public-corpus.v1.json")
	if plan.System != "elnath-current" || plan.Baseline != "self" {
		t.Fatalf("unexpected current plan: %+v", plan)
	}
	if plan.RuntimePolicy != "" {
		t.Fatalf("expected empty runtime policy placeholder, got %q", plan.RuntimePolicy)
	}
	if len(plan.RequiredEnv) == 0 || plan.RequiredEnv[0] != "CURRENT_BIN" {
		t.Fatalf("unexpected required env: %+v", plan.RequiredEnv)
	}
}
