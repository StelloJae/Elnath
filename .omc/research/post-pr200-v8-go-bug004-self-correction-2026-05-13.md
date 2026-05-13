# Post-PR200 V8-GO-BUG-004 self-correction smoke

Date: 2026-05-13
Branch: `main`
HEAD: `6f1b4ab87f79bd1c55a78d8d9240669f5e3cd620`
Result directory: `benchmarks/results/post-pr200-v8-go-bug004-self-correction-20260513`
Scorecard: `benchmarks/results/post-pr200-v8-go-bug004-self-correction-20260513/current-scorecard.json`
Debug directory: `benchmarks/results/post-pr200-v8-go-bug004-self-correction-20260513/current-scorecard.debug`

## Patch under test

- `ELNATH_SELF_HEALING_*` env overrides added for bounded completion retry control.
- current benchmark wrapper opts benchmark runs into:
  - `ELNATH_SELF_HEALING_OBSERVE_ONLY=false`
  - `ELNATH_SELF_HEALING_COMPLETION_RETRY_MAX=1`
- completion retry prompts now add reason-specific guidance for:
  - `edit_intent_without_mutation`
  - `budget_exceeded_after_edit_intent`
  - `verification_command_failed`
  - `unsupported_verification_success_claim`
  - `final_response_reports_incomplete`

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
  .omc/research/post-pr200-v8-go-bug004-self-correction-20260513/current-plan.json
```

## Verification before smoke

- `go test ./internal/config -run TestApplyEnvOverrides -count=1`: PASS
- `go test ./cmd/elnath -run 'TestCompletionRetryPromptGuides|TestCompletionContractSummaryDetectsEditIntentWithoutMutation|TestCompletionContractSummaryDetectsBudgetExceededAfterEditIntent' -count=1`: PASS
- `go test ./cmd/elnath ./internal/config -count=1`: PASS
- `bash -n scripts/run_current_benchmark_wrapper.sh scripts/run_baseline_benchmark_wrapper.sh`: PASS
- `git diff --check`: PASS
- `make build`: PASS
- `./elnath eval validate .omc/research/post-pr200-v8-go-bug004-self-correction-20260513/one-task-corpus.json`: PASS

## Result

- total tasks: 1
- success + verified: 0
- `verification_unavailable`: 0
- recovery attempted: 1
- duration: 614s
- result: FAIL
- failure family: `incomplete_patch`
- verification command: `go test ./...`
- verification passed: true
- changed files: `backend_inotify.go`
- sidecar: `current-scorecard.debug/V8-GO-BUG-004-run-1.debug-evidence.json`

Retained root:

`/var/folders/m_/fnd4jmdn6fv03bdwkqrwk1fh0000gn/T//elnath-current-benchmark.CMSr9x`

## Finding

The previous expanded smoke failure was `no_change_planning_failure`: edit intent was detected but the final diff was empty.

This one-task rerun improved that specific failure:

- no-change failure did not repeat
- Elnath produced a concrete production diff in `backend_inotify.go`
- verification passed

But the task still failed claim-safe acceptance:

- no focused regression was added in `backend_inotify_test.go`
- the wrapper correctly classified the result as `incomplete_patch`

Interpretation:

The first self-correction slice improved no-diff behavior but did not fully solve benchmark-quality completion. The next repair should target task-specific recovery when verification passes but required focused regression evidence is missing.

## Claim boundary

Allowed:

- one-task `V8-GO-BUG-004` current-only smoke completed
- `no_change_planning_failure` improved to concrete production diff
- verification passed but patch quality failed
- `verification_unavailable=0`

Forbidden:

- V8-GO-BUG-004 passed
- expanded selected smoke passed
- v8 benchmark passed
- full v8 current-only passed
- baseline completed
- Elnath beats Claude Code
- Elnath beats Codex

## Next autonomous action

Patch the benchmark wrapper's task-specific recovery prompt so missing-regression recovery edits the required focused test before more broad inspection.

For `V8-GO-BUG-004`, the immediate target is:

- keep `backend_inotify.go` production diff intact when present
- edit `backend_inotify_test.go` before rerunning `go test ./...`
- avoid spending the whole recovery turn re-reading the same inotify implementation

