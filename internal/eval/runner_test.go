package eval

import (
	"encoding/json"
	"fmt"
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

func TestRunBaselinePlanPreservesRunResultTraceFields(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{
  "version":"v1",
  "tasks":[
    {"id":"GO-BF-002","title":"task 1","track":"bugfix","language":"go","repo_class":"cli_dev_tool","benchmark_family":"month3_canary","prompt":"fix it","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}

	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
cat > "$out" <<'EOF'
{
  "task_id":"GO-BF-002",
  "track":"bugfix",
  "language":"go",
  "success":false,
  "intervention_count":0,
  "intervention_needed":false,
  "verification_passed":false,
  "failure_family":"no_change_planning_failure",
  "duration_seconds":1,
  "changed_files":["internal/daemon/runner.go"],
  "edit_intent_detected":true,
  "final_incomplete_detected":true,
  "trace_summary":"changed_files=1; edit_intent_detected=true; final_incomplete_detected=true"
}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)

	scorecardPath := filepath.Join(dir, "current-scorecard.json")
	plan := &BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	}

	scorecard, err := RunBaselinePlan(plan)
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	if len(scorecard.Results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(scorecard.Results))
	}
	result := scorecard.Results[0]
	if got, want := result.ChangedFiles, []string{"internal/daemon/runner.go"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("ChangedFiles = %#v, want %#v", got, want)
	}
	if !result.EditIntentDetected {
		t.Fatal("EditIntentDetected = false, want true")
	}
	if !result.FinalIncompleteDetected {
		t.Fatal("FinalIncompleteDetected = false, want true")
	}
	if result.TraceSummary == "" || strings.Contains(result.TraceSummary, "RAW_TOOL_OUTPUT") {
		t.Fatalf("TraceSummary = %q, want bounded redacted summary", result.TraceSummary)
	}
	data, err := os.ReadFile(scorecardPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"changed_files"`) || !strings.Contains(text, `"edit_intent_detected": true`) || !strings.Contains(text, `"final_incomplete_detected": true`) {
		t.Fatalf("scorecard missing trace fields: %s", text)
	}
}

func TestRunBaselinePlanEnrichesPatchQuality(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{
  "version":"v1",
  "tasks":[
    {"id":"V8-PY-BUG-001","title":"task 1","track":"bugfix","language":"python","repo_class":"service_backend","benchmark_family":"v8_public_first_freeze","prompt":"fix it","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["the option propagation edge case is covered by tests"]}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}

	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
cat > "$out" <<'EOF'
{
  "task_id":"V8-PY-BUG-001",
  "track":"bugfix",
  "language":"python",
  "success":true,
  "intervention_count":0,
  "intervention_needed":false,
  "verification_passed":true,
  "duration_seconds":1,
  "changed_files":["src/requests/sessions.py"]
}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)

	scorecardPath := filepath.Join(dir, "current-scorecard.json")
	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "self",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	result := scorecard.Results[0]
	if result.PatchQuality != PatchQualityWeak {
		t.Fatalf("PatchQuality = %q, want %q; result=%+v", result.PatchQuality, PatchQualityWeak, result)
	}
	if !hasPatchQualityFinding(result.PatchQualityFindings, PatchQualityFindingMissingTestOrFixtureDiff) {
		t.Fatalf("PatchQualityFindings = %v, want %q", result.PatchQualityFindings, PatchQualityFindingMissingTestOrFixtureDiff)
	}
	data, err := os.ReadFile(scorecardPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"patch_quality": "weak"`) || !strings.Contains(text, `"missing_test_or_fixture_diff"`) {
		t.Fatalf("scorecard missing patch-quality evidence: %s", text)
	}
}

func TestRunPlanExposesTaskVerificationCommandEnv(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{
  "version":"v1",
  "tasks":[
    {
      "id":"V8-MIX-BF-001",
      "title":"task",
      "track":"brownfield_feature",
      "language":"go",
      "repo_class":"service_backend",
      "benchmark_family":"v8_public_first_freeze",
      "prompt":"fix it",
      "repo":"https://github.com/example/repo",
      "repo_ref":"deadbeef",
      "verification_command":"cd kyaml && go test ./...",
      "acceptance_criteria":["tests pass"]
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}

	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
cat > "$out" <<EOF
{
  "task_id":"V8-MIX-BF-001",
  "track":"brownfield_feature",
  "language":"go",
  "success":true,
  "intervention_count":0,
  "intervention_needed":false,
  "verification_command":"$ELNATH_BENCHMARK_TASK_VERIFICATION_COMMAND",
  "verification_passed":true,
  "duration_seconds":1
}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)

	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "self",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}} {{task_id}}`,
		OutputPath:      filepath.Join(dir, "scorecard.json"),
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	if got, want := scorecard.Results[0].VerificationCommand, "cd kyaml && go test ./..."; got != want {
		t.Fatalf("VerificationCommand from wrapper env = %q, want %q", got, want)
	}
}

func TestRunPlanRendersTaskVerificationCommandPlaceholder(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{
  "version":"v1",
  "tasks":[
    {
      "id":"V8-PY-BUG-001",
      "title":"task",
      "track":"bugfix",
      "language":"python",
      "repo_class":"service_backend",
      "benchmark_family":"v8_public_first_freeze",
      "prompt":"fix it",
      "repo":"https://github.com/example/repo",
      "repo_ref":"deadbeef",
      "verification_command":"python -m pytest tests/test_requests.py",
      "acceptance_criteria":["tests pass"]
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}

	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
verification_command="$3"
cat > "$out" <<EOF
{
  "task_id":"V8-PY-BUG-001",
  "track":"bugfix",
  "language":"python",
  "success":true,
  "intervention_count":0,
  "intervention_needed":false,
  "verification_command":"$verification_command",
  "verification_passed":true,
  "duration_seconds":1
}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)

	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "self",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}} {{task_id}} {{task_verification_command}}`,
		OutputPath:      filepath.Join(dir, "scorecard.json"),
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	if got, want := scorecard.Results[0].VerificationCommand, "python -m pytest tests/test_requests.py"; got != want {
		t.Fatalf("rendered verification command = %q, want %q", got, want)
	}
}

func TestEvalRunCurrent_KeepTmpRecordsPerTaskTempRoot(t *testing.T) {
	dir := t.TempDir()
	retainedRoot := filepath.Join(dir, "elnath-current-benchmark.unit")
	if err := os.MkdirAll(retainedRoot, 0o755); err != nil {
		t.Fatalf("mkdir retained root: %v", err)
	}
	corpusPath := writeRunnerCorpus(t, dir, []string{"TS-BF-001"})
	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
task_id="$2"
cat > "$out" <<EOF
{
  "task_id":"$task_id",
  "track":"bugfix",
  "language":"typescript",
  "success":false,
  "intervention_count":0,
  "intervention_needed":false,
  "verification_passed":false,
  "failure_family":"verification_failed",
  "duration_seconds":1,
  "debug_evidence":{"retained_temp_root":"$RETAINED_ROOT"}
}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)
	t.Setenv("ELNATH_BENCHMARK_KEEP_TMP", "1")
	t.Setenv("RETAINED_ROOT", retainedRoot)

	scorecardPath := filepath.Join(dir, "scorecard.json")
	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}} {{task_id}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	evidence := scorecard.Results[0].DebugEvidence
	if evidence == nil || evidence.SidecarPath == "" || filepath.IsAbs(evidence.SidecarPath) {
		t.Fatalf("DebugEvidence.RetainedTempRoot missing: %+v", scorecard.Results[0])
	}
	sidecarEvidence := loadRunnerDebugEvidenceSidecar(t, scorecardPath, scorecard.Results[0])
	if sidecarEvidence.RetainedTempRoot != retainedRoot {
		t.Fatalf("sidecar RetainedTempRoot = %q, want %q", sidecarEvidence.RetainedTempRoot, retainedRoot)
	}
}

func TestEvalRunCurrent_KeepTmpRecordsWrapperStderrPath(t *testing.T) {
	dir := t.TempDir()
	corpusPath := writeRunnerCorpus(t, dir, []string{"TS-BF-001"})
	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
task_id="$2"
echo "wrapper stderr marker for $task_id" >&2
cat > "$out" <<EOF
{"task_id":"$task_id","track":"bugfix","language":"typescript","success":false,"intervention_count":0,"intervention_needed":false,"failure_family":"verification_failed","duration_seconds":1}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)
	t.Setenv("ELNATH_BENCHMARK_KEEP_TMP", "1")

	scorecardPath := filepath.Join(dir, "scorecard.json")
	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}} {{task_id}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	evidence := loadRunnerDebugEvidenceSidecar(t, scorecardPath, scorecard.Results[0])
	if evidence.WrapperStderrPath == "" {
		t.Fatalf("WrapperStderrPath missing: %+v", scorecard.Results[0])
	}
	stderrData, err := os.ReadFile(evidence.WrapperStderrPath)
	if err != nil {
		t.Fatalf("read wrapper stderr: %v", err)
	}
	if !strings.Contains(string(stderrData), "wrapper stderr marker for TS-BF-001") {
		t.Fatalf("wrapper stderr sidecar missing marker: %s", string(stderrData))
	}
}

func TestEvalRunCurrent_KeepTmpRecordsVerificationAndRecoveryLogPathsWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	retainedRoot := filepath.Join(dir, "elnath-current-benchmark.logs")
	verifyLog := filepath.Join(retainedRoot, "verify.log")
	recoveryLog := filepath.Join(retainedRoot, "recovery.log")
	diffPath := filepath.Join(retainedRoot, "diff.patch")
	if err := os.MkdirAll(retainedRoot, 0o755); err != nil {
		t.Fatalf("mkdir retained root: %v", err)
	}
	for _, path := range []string{verifyLog, recoveryLog, diffPath} {
		if err := os.WriteFile(path, []byte("debug pointer only"), 0o644); err != nil {
			t.Fatalf("write artifact %s: %v", path, err)
		}
	}
	corpusPath := writeRunnerCorpus(t, dir, []string{"TS-BF-001"})
	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
cat > "$out" <<EOF
{
  "task_id":"TS-BF-001",
  "track":"bugfix",
  "language":"typescript",
  "success":false,
  "intervention_count":0,
  "intervention_needed":false,
  "verification_passed":false,
  "failure_family":"verification_failed",
  "duration_seconds":1,
  "debug_evidence":{
    "retained_temp_root":"$RETAINED_ROOT",
    "verification_log_path":"$VERIFY_LOG_PATH",
    "recovery_log_path":"$RECOVERY_LOG_PATH",
    "diff_path":"$DIFF_PATH"
  }
}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)
	t.Setenv("RETAINED_ROOT", retainedRoot)
	t.Setenv("VERIFY_LOG_PATH", verifyLog)
	t.Setenv("RECOVERY_LOG_PATH", recoveryLog)
	t.Setenv("DIFF_PATH", diffPath)
	t.Setenv("ELNATH_BENCHMARK_KEEP_TMP", "1")

	scorecardPath := filepath.Join(dir, "scorecard.json")
	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	evidence := loadRunnerDebugEvidenceSidecar(t, scorecardPath, scorecard.Results[0])
	if evidence == nil || evidence.VerificationLogPath != verifyLog || evidence.RecoveryLogPath != recoveryLog || evidence.DiffPath != diffPath {
		t.Fatalf("debug log paths not preserved: %+v", evidence)
	}
}

func TestEvalRunCurrent_NoKeepTmpDoesNotPreserveTempRoots(t *testing.T) {
	dir := t.TempDir()
	corpusPath := writeRunnerCorpus(t, dir, []string{"TS-BF-001"})
	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
cat > "$out" <<'EOF'
{"task_id":"TS-BF-001","track":"bugfix","language":"typescript","success":true,"intervention_count":0,"intervention_needed":false,"duration_seconds":1}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)

	scorecardPath := filepath.Join(dir, "scorecard.json")
	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	if evidence := scorecard.Results[0].DebugEvidence; evidence != nil {
		t.Fatalf("DebugEvidence = %+v, want nil without KEEP_TMP", evidence)
	}
	if _, err := os.Stat(debugEvidenceDir(scorecardPath)); !os.IsNotExist(err) {
		t.Fatalf("debug directory should not be created without KEEP_TMP, stat err=%v", err)
	}
}

func TestEvalRunCurrent_PerTaskTraceSurvivesMultiTaskRun(t *testing.T) {
	dir := t.TempDir()
	corpusPath := writeRunnerCorpus(t, dir, []string{"TS-BF-001", "TS-BF-002"})
	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
task_id="$2"
retained="$RETAINED_BASE/elnath-current-benchmark.$task_id"
mkdir -p "$retained"
echo "stderr for $task_id" >&2
cat > "$out" <<EOF
{
  "task_id":"$task_id",
  "track":"bugfix",
  "language":"typescript",
  "success":false,
  "intervention_count":0,
  "intervention_needed":false,
  "failure_family":"verification_failed",
  "duration_seconds":1,
  "debug_evidence":{"retained_temp_root":"$retained"}
}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)
	t.Setenv("ELNATH_BENCHMARK_KEEP_TMP", "1")
	t.Setenv("RETAINED_BASE", filepath.Join(dir, "retained"))

	scorecardPath := filepath.Join(dir, "scorecard.json")
	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}} {{task_id}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	if len(scorecard.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(scorecard.Results))
	}
	first := scorecard.Results[0].DebugEvidence
	second := scorecard.Results[1].DebugEvidence
	if first == nil || second == nil {
		t.Fatalf("missing per-task debug evidence: %+v", scorecard.Results)
	}
	if first.SidecarPath == second.SidecarPath {
		t.Fatalf("debug evidence was not per-task distinct: first=%+v second=%+v", first, second)
	}
	firstSidecar := loadRunnerDebugEvidenceSidecar(t, scorecardPath, scorecard.Results[0])
	secondSidecar := loadRunnerDebugEvidenceSidecar(t, scorecardPath, scorecard.Results[1])
	if firstSidecar.RetainedTempRoot == secondSidecar.RetainedTempRoot || firstSidecar.WrapperStderrPath == secondSidecar.WrapperStderrPath {
		t.Fatalf("sidecar debug evidence was not per-task distinct: first=%+v second=%+v", firstSidecar, secondSidecar)
	}
}

func TestEvalTraceMetadata_BoundedAndRedacted(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`{
  "version":"v1",
  "tasks":[
    {"id":"TS-BF-001","title":"task 1","track":"bugfix","language":"typescript","repo_class":"service_backend","benchmark_family":"month3_canary","prompt":"SECRET_PROMPT_BODY","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
echo "RAW_TOOL_OUTPUT_SECRET" >&2
cat > "$out" <<'EOF'
{"task_id":"TS-BF-001","track":"bugfix","language":"typescript","success":false,"intervention_count":0,"intervention_needed":false,"failure_family":"verification_failed","duration_seconds":1,"debug_evidence":{"retained_temp_root":"/tmp/elnath-current-benchmark.redacted"}}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)
	t.Setenv("ELNATH_BENCHMARK_KEEP_TMP", "1")

	scorecardPath := filepath.Join(dir, "scorecard.json")
	if _, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}} {{task_id}} {{task_prompt}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	}); err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	data, err := os.ReadFile(scorecardPath)
	if err != nil {
		t.Fatalf("read scorecard: %v", err)
	}
	scorecardText := string(data)
	if strings.Contains(scorecardText, "SECRET_PROMPT_BODY") || strings.Contains(scorecardText, "RAW_TOOL_OUTPUT_SECRET") {
		t.Fatalf("scorecard leaked raw prompt/tool output: %s", scorecardText)
	}
	if strings.Contains(scorecardText, "wrapper_stderr_path") {
		t.Fatalf("scorecard should not inline sensitive wrapper paths: %s", scorecardText)
	}
	if !strings.Contains(scorecardText, "sidecar_path") {
		t.Fatalf("scorecard should keep bounded debug sidecar pointer: %s", scorecardText)
	}
}

func TestEvalRunCurrent_FailedTaskHasEnoughDebugPointers(t *testing.T) {
	dir := t.TempDir()
	retainedRoot := filepath.Join(dir, "elnath-current-benchmark.failed")
	verifyLog := filepath.Join(retainedRoot, "verify.log")
	diffPath := filepath.Join(retainedRoot, "diff.patch")
	if err := os.MkdirAll(retainedRoot, 0o755); err != nil {
		t.Fatalf("mkdir retained root: %v", err)
	}
	if err := os.WriteFile(verifyLog, []byte("verification failed"), 0o644); err != nil {
		t.Fatalf("write verify log: %v", err)
	}
	if err := os.WriteFile(diffPath, []byte("diff --git a/file b/file"), 0o644); err != nil {
		t.Fatalf("write diff: %v", err)
	}
	corpusPath := writeRunnerCorpus(t, dir, []string{"TS-BF-002"})
	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
echo "wrapper failed after verification" >&2
cat > "$out" <<EOF
{
  "task_id":"TS-BF-002",
  "track":"bugfix",
  "language":"typescript",
  "success":false,
  "intervention_count":0,
  "intervention_needed":false,
  "verification_passed":false,
  "failure_family":"verification_failed",
  "duration_seconds":1,
  "debug_evidence":{
    "retained_temp_root":"$RETAINED_ROOT",
    "verification_log_path":"$VERIFY_LOG_PATH",
    "diff_path":"$DIFF_PATH"
  }
}
EOF
exit 1
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)
	t.Setenv("RETAINED_ROOT", retainedRoot)
	t.Setenv("VERIFY_LOG_PATH", verifyLog)
	t.Setenv("DIFF_PATH", diffPath)
	t.Setenv("ELNATH_BENCHMARK_KEEP_TMP", "1")

	scorecardPath := filepath.Join(dir, "scorecard.json")
	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	result := scorecard.Results[0]
	if result.Success || result.FailureFamily != "verification_failed" {
		t.Fatalf("unexpected failed result: %+v", result)
	}
	evidence := loadRunnerDebugEvidenceSidecar(t, scorecardPath, result)
	if evidence == nil || evidence.RetainedTempRoot == "" || evidence.VerificationLogPath == "" || evidence.DiffPath == "" || evidence.WrapperStderrPath == "" {
		t.Fatalf("failed task lacks debug pointers: %+v", evidence)
	}
}

func TestEvalRunCurrent_DropsDebugPointersOutsideRetainedRoots(t *testing.T) {
	dir := t.TempDir()
	retainedRoot := filepath.Join(dir, "elnath-current-benchmark.scrub")
	if err := os.MkdirAll(retainedRoot, 0o755); err != nil {
		t.Fatalf("mkdir retained root: %v", err)
	}
	insideDiff := filepath.Join(retainedRoot, "diff.patch")
	outsideLog := filepath.Join(dir, "outside-verify.log")
	if err := os.WriteFile(insideDiff, []byte("diff"), 0o644); err != nil {
		t.Fatalf("write inside diff: %v", err)
	}
	if err := os.WriteFile(outsideLog, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside log: %v", err)
	}
	corpusPath := writeRunnerCorpus(t, dir, []string{"TS-BF-001"})
	wrapperPath := filepath.Join(dir, "wrapper.sh")
	wrapper := `#!/bin/sh
out="$1"
cat > "$out" <<EOF
{
  "task_id":"TS-BF-001",
  "track":"bugfix",
  "language":"typescript",
  "success":false,
  "intervention_count":0,
  "intervention_needed":false,
  "verification_passed":false,
  "failure_family":"verification_failed",
  "duration_seconds":1,
  "debug_evidence":{
    "retained_temp_root":"$RETAINED_ROOT",
    "verification_log_path":"$OUTSIDE_LOG",
    "diff_path":"$INSIDE_DIFF"
  }
}
EOF
`
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	t.Setenv("CURRENT_BIN", wrapperPath)
	t.Setenv("RETAINED_ROOT", retainedRoot)
	t.Setenv("OUTSIDE_LOG", outsideLog)
	t.Setenv("INSIDE_DIFF", insideDiff)
	t.Setenv("ELNATH_BENCHMARK_KEEP_TMP", "1")

	scorecardPath := filepath.Join(dir, "scorecard.json")
	scorecard, err := RunBaselinePlan(&BaselineRunPlan{
		Version:         "v1",
		System:          "elnath-current",
		Baseline:        "elnath-current",
		CorpusPath:      corpusPath,
		CommandTemplate: `"$CURRENT_BIN" {{task_output}}`,
		OutputPath:      scorecardPath,
		RepeatedRuns:    1,
		RequiredEnv:     []string{"CURRENT_BIN"},
	})
	if err != nil {
		t.Fatalf("RunBaselinePlan: %v", err)
	}
	evidence := loadRunnerDebugEvidenceSidecar(t, scorecardPath, scorecard.Results[0])
	if evidence.VerificationLogPath != "" {
		t.Fatalf("outside verification log path should be omitted: %+v", evidence)
	}
	if evidence.DiffPath != insideDiff {
		t.Fatalf("inside diff path = %q, want %q", evidence.DiffPath, insideDiff)
	}
}

func loadRunnerDebugEvidenceSidecar(t *testing.T, scorecardPath string, result RunResult) *DebugEvidence {
	t.Helper()
	if result.DebugEvidence == nil || result.DebugEvidence.SidecarPath == "" {
		t.Fatalf("result missing debug evidence sidecar path: %+v", result)
	}
	if filepath.IsAbs(result.DebugEvidence.SidecarPath) {
		t.Fatalf("scorecard sidecar path should be relative: %s", result.DebugEvidence.SidecarPath)
	}
	sidecarPath := filepath.Join(filepath.Dir(scorecardPath), result.DebugEvidence.SidecarPath)
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read debug evidence sidecar: %v", err)
	}
	var evidence DebugEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("parse debug evidence sidecar: %v", err)
	}
	return &evidence
}

func writeRunnerCorpus(t *testing.T, dir string, taskIDs []string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"version":"v1","tasks":[`)
	for i, taskID := range taskIDs {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%q,"title":"task","track":"bugfix","language":"typescript","repo_class":"service_backend","benchmark_family":"month3_canary","prompt":"fix it","repo":"https://github.com/example/repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]}`, taskID)
	}
	b.WriteString(`]}`)
	path := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	return path
}
