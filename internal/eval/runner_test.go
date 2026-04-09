package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBaselinePlan(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{
  "version":"v1",
  "tasks":[
    {"id":"BF-001","title":"task 1","track":"brownfield_feature","language":"go","repo_class":"cli_dev_tool","benchmark_family":"brownfield_primary","prompt":"do 1","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]},
    {"id":"BUG-001","title":"task 2","track":"bugfix","language":"typescript","repo_class":"service_backend","benchmark_family":"brownfield_holdout","prompt":"do 2","repo":"https://github.com/example/repo2","repo_ref":"feedface","acceptance_criteria":["tests pass"]}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}

	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
task_id="$2"
track="$3"
language="$4"
cat > "$out" <<EOF
{"task_id":"$task_id","track":"$track","language":"$language","success":true,"intervention_count":0,"intervention_needed":false,"duration_seconds":1}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("BASELINE_BIN", wrapperPath)

	scorecardPath := filepath.Join(dir, "baseline-scorecard.json")
	plan := &BaselineRunPlan{
		Version:         "v1",
		Baseline:        "claude-codex-omx-omc",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$BASELINE_BIN" {{task_output}} {{task_id}} {{task_track}} {{task_language}}`,
		OutputPath:      scorecardPath,
		RuntimePolicy:   "sandbox=workspace-write, approvals=never",
		RepeatedRuns:    2,
		RequiredEnv:     []string{"BASELINE_BIN"},
	}

	scorecard, err := RunBaselinePlan(plan)
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	if scorecard.System != "baseline-runner" || scorecard.RepeatedRuns != 2 || len(scorecard.Results) != 4 {
		t.Fatalf("unexpected scorecard: %+v", scorecard)
	}
	if scorecard.RuntimePolicy != "sandbox=workspace-write, approvals=never" {
		t.Fatalf("unexpected runtime policy: %+v", scorecard)
	}
	data, err := os.ReadFile(scorecardPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), `"repeated_runs": 2`) || !strings.Contains(string(data), `"runtime_policy": "sandbox=workspace-write, approvals=never"`) {
		t.Fatalf("unexpected scorecard file: %s", string(data))
	}
}

func TestRunBaselinePlanUsesWrapperResultEvenIfCommandExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{
  "version":"v1",
  "tasks":[
    {"id":"BF-001","title":"task 1","track":"brownfield_feature","language":"go","repo_class":"cli_dev_tool","benchmark_family":"brownfield_primary","prompt":"do 1","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]},
    {"id":"BUG-001","title":"task 2","track":"bugfix","language":"typescript","repo_class":"service_backend","benchmark_family":"bugfix_primary","prompt":"do 2","repo":"https://github.com/example/repo2","repo_ref":"feedface","acceptance_criteria":["tests pass"]}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}

	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
task_id="$2"
track="$3"
language="$4"
if [ "$task_id" = "BUG-001" ]; then
cat > "$out" <<EOF
{"task_id":"$task_id","track":"$track","language":"$language","success":false,"intervention_count":0,"intervention_needed":false,"failure_family":"execution_failed","duration_seconds":1}
EOF
exit 1
fi
cat > "$out" <<EOF
{"task_id":"$task_id","track":"$track","language":"$language","success":true,"intervention_count":0,"intervention_needed":false,"duration_seconds":1}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("BASELINE_BIN", wrapperPath)

	scorecardPath := filepath.Join(dir, "baseline-scorecard.json")
	plan := &BaselineRunPlan{
		Version:         "v1",
		Baseline:        "claude-codex-omx-omc",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$BASELINE_BIN" {{task_output}} {{task_id}} {{task_track}} {{task_language}} {{task_prompt}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"BASELINE_BIN"},
	}

	scorecard, err := RunBaselinePlan(plan)
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	if len(scorecard.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(scorecard.Results))
	}
	if scorecard.Results[0].TaskID != "BF-001" || !scorecard.Results[0].Success {
		t.Fatalf("unexpected first result: %+v", scorecard.Results[0])
	}
	if scorecard.Results[1].TaskID != "BUG-001" || scorecard.Results[1].FailureFamily != "execution_failed" {
		t.Fatalf("unexpected second result: %+v", scorecard.Results[1])
	}
	data, err := os.ReadFile(scorecardPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"task_id": "BF-001"`) || !strings.Contains(text, `"task_id": "BUG-001"`) {
		t.Fatalf("partial scorecard missing results: %s", text)
	}
}
