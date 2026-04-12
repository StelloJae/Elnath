package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdEvalBenchmarkSubcommand(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{"version":"v1","tasks":[
		{"id":"T1","title":"task 1","track":"bugfix","language":"go","repo_class":"service_backend","benchmark_family":"bugfix_primary","prompt":"do it","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]},
		{"id":"T2","title":"task 2","track":"bugfix","language":"go","repo_class":"service_backend","benchmark_family":"bugfix_primary","prompt":"do it","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]}
	]}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	wrapperPath := filepath.Join(dir, "benchmark-wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
task_id="$2"
if [ "$task_id" = "T1" ]; then
cat > "$out" <<'EOF'
{"task_id":"T1","track":"bugfix","language":"go","success":true,"regression_triggered":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1}
EOF
else
cat > "$out" <<'EOF'
{"task_id":"T2","track":"bugfix","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1}
EOF
fi
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("BENCHMARK_BIN", wrapperPath)
	planPath := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planPath, []byte(`{
		"version":"v1",
		"system":"elnath-current",
		"baseline":"self",
		"corpus_path":"`+corpusPath+`",
		"command_template":"\"$BENCHMARK_BIN\" {{task_output}} {{task_id}}",
		"output_path":"`+filepath.Join(dir, "scorecard.json")+`",
		"runtime_policy":"sandbox=workspace-write, approvals=never",
		"required_env":["BENCHMARK_BIN"]
	}`), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"benchmark", planPath}); err != nil {
			t.Fatalf("cmdEval benchmark: %v", err)
		}
	})
	for _, needle := range []string{
		"Benchmark complete",
		"success_rate",
		"success_and_verified_rate",
		"intervention_rate",
		"intervention_mean",
		"verification_pass_rate",
		"recovery_success_rate",
		"success_duration_mean",
		"regression_rate",
	} {
		if !strings.Contains(stdout, needle) {
			t.Fatalf("stdout = %q, want %q", stdout, needle)
		}
	}
}

func TestCmdEvalSummarizeIncludesRegression(t *testing.T) {
	dir := t.TempDir()
	scorecardPath := filepath.Join(dir, "scorecard.json")
	if err := os.WriteFile(scorecardPath, []byte(`{
		"version":"v1",
		"system":"elnath",
		"results":[
			{"task_id":"T1","track":"bugfix","language":"go","success":true,"regression_triggered":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1},
			{"task_id":"T2","track":"bugfix","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write scorecard: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"summarize", scorecardPath}); err != nil {
			t.Fatalf("cmdEval summarize: %v", err)
		}
	})
	if !strings.Contains(stdout, "regression_rate=0.5000") {
		t.Fatalf("stdout = %q, want regression_rate=0.5000", stdout)
	}
	for _, needle := range []string{
		"success_and_verified_rate=1.00",
		"intervention_mean=0.00",
		"success_duration_mean=1.00",
	} {
		if !strings.Contains(stdout, needle) {
			t.Fatalf("stdout = %q, want %q", stdout, needle)
		}
	}
}

func TestCmdEvalDiffIncludesRegressionDelta(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "current.json")
	baselinePath := filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(currentPath, []byte(`{
		"version":"v1",
		"system":"elnath",
		"results":[
			{"task_id":"T1","track":"bugfix","language":"go","success":true,"regression_triggered":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1},
			{"task_id":"T2","track":"bugfix","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write current: %v", err)
	}
	if err := os.WriteFile(baselinePath, []byte(`{
		"version":"v1",
		"system":"baseline",
		"results":[
			{"task_id":"T1","track":"bugfix","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1},
			{"task_id":"T2","track":"bugfix","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"verification_passed":true,"duration_seconds":1}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"diff", currentPath, baselinePath}); err != nil {
			t.Fatalf("cmdEval diff: %v", err)
		}
	})
	if !strings.Contains(stdout, "regression_rate_delta=0.5000") {
		t.Fatalf("stdout = %q, want regression_rate_delta=0.5000", stdout)
	}
	for _, needle := range []string{
		"success_and_verified_rate_delta=",
		"intervention_mean_delta=",
		"success_duration_mean_delta=",
	} {
		if !strings.Contains(stdout, needle) {
			t.Fatalf("stdout = %q, want %q", stdout, needle)
		}
	}
}

func TestCmdEvalMonth3Gate(t *testing.T) {
	dir := t.TempDir()
	baselinePath := filepath.Join(dir, "baseline.json")
	run1Path := filepath.Join(dir, "run1.json")
	run2Path := filepath.Join(dir, "run2.json")
	run3Path := filepath.Join(dir, "run3.json")

	baseline := `{
		"version":"v1",
		"system":"baseline",
		"results":[
			{"task_id":"BF-1","track":"brownfield_feature","language":"go","success":true,"verification_passed":true,"intervention_count":2,"intervention_needed":false,"duration_seconds":10,"regression_triggered":true},
			{"task_id":"BF-2","track":"brownfield_feature","language":"go","success":false,"verification_passed":false,"intervention_count":2,"intervention_needed":false,"duration_seconds":20},
			{"task_id":"BUG-1","track":"bugfix","language":"go","success":true,"verification_passed":true,"intervention_count":2,"intervention_needed":false,"duration_seconds":10},
			{"task_id":"BUG-2","track":"bugfix","language":"go","success":false,"verification_passed":false,"intervention_count":2,"intervention_needed":false,"duration_seconds":20}
		]
	}`
	passingRun := `{
		"version":"v1",
		"system":"elnath",
		"results":[
			{"task_id":"BF-1","track":"brownfield_feature","language":"go","success":true,"verification_passed":true,"intervention_count":1,"intervention_needed":false,"duration_seconds":8},
			{"task_id":"BF-2","track":"brownfield_feature","language":"go","success":true,"verification_passed":true,"intervention_count":1,"intervention_needed":false,"duration_seconds":8},
			{"task_id":"BUG-1","track":"bugfix","language":"go","success":true,"verification_passed":true,"intervention_count":1,"intervention_needed":false,"duration_seconds":10},
			{"task_id":"BUG-2","track":"bugfix","language":"go","success":false,"verification_passed":false,"intervention_count":1,"intervention_needed":false,"duration_seconds":12}
		]
	}`

	for path, body := range map[string]string{
		baselinePath: baseline,
		run1Path:     passingRun,
		run2Path:     passingRun,
		run3Path:     passingRun,
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdEval(context.Background(), []string{"month3-gate", run1Path, run2Path, run3Path, baselinePath}); err != nil {
			t.Fatalf("cmdEval month3-gate: %v", err)
		}
	})
	for _, needle := range []string{
		"Month 3 gate: PASS",
		"Run 1 H1: PASS",
		"Average H1: PASS",
		"T_brownfield",
	} {
		if !strings.Contains(stdout, needle) {
			t.Fatalf("stdout = %q, want %q", stdout, needle)
		}
	}
}
