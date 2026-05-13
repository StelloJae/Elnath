# Post-PR200 V8-GO-BUG-004 special regression recovery smoke

Date: 2026-05-13
Branch: `main`
HEAD: `6f1b4ab87f79bd1c55a78d8d9240669f5e3cd620`
Result directory: `benchmarks/results/post-pr200-v8-go-bug004-special-regression-recovery-20260513`
Scorecard: `benchmarks/results/post-pr200-v8-go-bug004-special-regression-recovery-20260513/current-scorecard.json`
Debug directory: `benchmarks/results/post-pr200-v8-go-bug004-special-regression-recovery-20260513/current-scorecard.debug`

## Patch under test

This lane tested the cumulative self-correction repair:

- `ELNATH_SELF_HEALING_*` env overrides for bounded completion retry control
- current benchmark wrapper opts benchmark runs into bounded completion retry:
  - `ELNATH_SELF_HEALING_OBSERVE_ONLY=false`
  - `ELNATH_SELF_HEALING_COMPLETION_RETRY_MAX=1`
- completion retry prompts include reason-specific guidance
- generic missing-regression recovery discipline added
- `V8-GO-BUG-004` now has a special passed-verification/missing-regression recovery path that instructs the agent to preserve the production diff and edit `backend_inotify_test.go` before broad re-inspection

## Command

```bash
CURRENT_BIN=/Users/stello/elnath/scripts/run_current_benchmark_wrapper.sh \
ELNATH_BIN=/Users/stello/elnath/elnath \
ELNATH_BENCHMARK_KEEP_TMP=1 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
ELNATH_BENCHMARK_SELF_HEALING_OBSERVE_ONLY=false \
ELNATH_BENCHMARK_SELF_HEALING_COMPLETION_RETRY_MAX=1 \
ELNATH_REASONING_EFFORT_MODE=auto \
ELNATH_REASONING_EFFORT=medium \
ELNATH_TIMEOUT=300 \
ELNATH_VERIFY_TIMEOUT=300 \
/Users/stello/elnath/elnath eval run-current \
  .omc/research/post-pr200-v8-go-bug004-special-regression-recovery-20260513/current-plan.json
```

## Verification before smoke

- `go test ./internal/config -run TestApplyEnvOverrides -count=1`: PASS
- `go test ./cmd/elnath -run 'TestCompletionRetryPromptGuides|TestCompletionContractSummaryDetectsEditIntentWithoutMutation|TestCompletionContractSummaryDetectsBudgetExceededAfterEditIntent' -count=1`: PASS
- `go test ./cmd/elnath ./internal/config -count=1`: PASS
- `bash -n scripts/run_current_benchmark_wrapper.sh scripts/run_baseline_benchmark_wrapper.sh`: PASS
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`: PASS
- `git diff --check`: PASS
- `make build`: PASS
- `./elnath eval validate .omc/research/post-pr200-v8-go-bug004-self-correction-20260513/one-task-corpus.json`: PASS

## Result

- total tasks: 1
- success + verified: 1
- `verification_unavailable`: 0
- recovery attempted: 0
- duration: 313s
- result: PASS
- verification command: `go test ./...`
- verification passed: true
- changed files:
  - `backend_inotify.go`
  - `backend_inotify_test.go`
- patch quality: `strong`
- sidecar: `current-scorecard.debug/V8-GO-BUG-004-run-1.debug-evidence.json`

Retained root:

`/var/folders/m_/fnd4jmdn6fv03bdwkqrwk1fh0000gn/T//elnath-current-benchmark.6qhPFX`

## Evidence chain

Earlier expanded smoke:

- `V8-GO-BUG-004`: `no_change_planning_failure`
- changed files: none

First one-task rerun after bounded self-correction env/prompt patch:

- result: FAIL
- failure family: `incomplete_patch`
- changed files: `backend_inotify.go`
- verification passed
- missing `backend_inotify_test.go`

Second one-task rerun after generic missing-regression prompt patch:

- result: FAIL
- failure family: `incomplete_patch`
- changed files: `backend_inotify.go`
- verification passed
- missing `backend_inotify_test.go`

Final one-task rerun after `V8-GO-BUG-004` special missing-regression recovery path:

- result: PASS
- changed files: `backend_inotify.go`, `backend_inotify_test.go`
- verification passed
- patch quality `strong`

## Interpretation

The repeated failure was not a setup-contract issue. It was a control-loop quality issue:

1. no-diff after edit intent
2. then production-only diff after verification pass
3. then successful production + focused regression after task-specific missing-regression recovery path

This supports keeping benchmark runs narrow and using them to expose runtime/control-loop gaps before any full v8 run.

## Claim boundary

Allowed:

- `V8-GO-BUG-004` one-task current-only smoke passed after the special missing-regression recovery path
- no `verification_unavailable` appeared
- the repair changed benchmark/runtime control behavior, not benchmark corpus or baselines

Forbidden:

- expanded selected smoke passed
- v8 benchmark passed
- full v8 current-only passed
- baseline completed
- Elnath beats Claude Code
- Elnath beats Codex
- broad public benchmark superiority

## Next autonomous action

Do not run full v8 yet.

Recommended next lane:

- rerun the 10-task expanded current-only selected smoke once with the self-correction and special regression recovery patches
- if `8-10/10` and no setup failure, widen planning can resume
- if `V8-MIX-BF-001` or `V8-GO-BUG-003` remains the only miss, diagnose that retained log specifically

