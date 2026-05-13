# V8-MIX-BF-001 regression insertion guard

Date: 2026-05-13
Branch: `codex/v8-mix-regression-insertion-guard`
Base HEAD: `ec8ac7bf27bebb66bd14f7fd61f71523dd8d47b2`
Status: local implementation slice

## Purpose

Repair the remaining post-PR199 `V8-MIX-BF-001` miss without widening
benchmark lanes.

PR #199 correctly detected:

- `finish_reason=budget_exceeded`
- `completion_warning=budget_exceeded_after_edit_intent`
- `retry_decision=retry_smaller_scope`

The remaining miss was task-specific recovery quality:

- Elnath generated a Python edit script for
  `api/internal/accumulator/namereferencetransformer_test.go`
- the script used a non-existent insertion marker:
  `func TestNameReferenceInCustomResources(t *testing.T) {`
- the script exited successfully and ran `cd kyaml && go test ./...`
- no test diff was left behind

## Change

`V8-MIX-BF-001` recovery guidance now tells Elnath to:

- use a real existing anchor:
  `func TestNameReferenceUnhappyRun(t *testing.T) {`
- append to the end if the anchor is missing
- run
  `git diff --name-only -- api/internal/accumulator/namereferencetransformer_test.go`
  after editing
- treat empty output from that diff check as a no-op edit before broad
  verification

Changed files:

- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`

## Verification

Red check before implementation:

```bash
bash scripts/test_current_benchmark_wrapper_completion_guards.sh
```

Result before fix:

- FAIL
- missing required snippets:
  - `TestNameReferenceUnhappyRun`
  - `git diff --name-only -- api/internal/accumulator/namereferencetransformer_test.go`

Checks after implementation:

```bash
bash -n scripts/run_current_benchmark_wrapper.sh
bash -n scripts/test_current_benchmark_wrapper_completion_guards.sh
bash scripts/test_current_benchmark_wrapper_completion_guards.sh
git diff --check
```

Results:

- PASS: wrapper syntax
- PASS: wrapper guard syntax
- PASS: wrapper completion guard cases
- PASS: `git diff --check`

## Claim Boundary

Allowed:

- V8-MIX recovery guidance now includes a real insertion anchor and explicit
  test-file diff check.
- This is a wrapper/recovery guidance fix based on retained post-PR199 evidence.

Forbidden:

- `V8-MIX-BF-001` passed after this fix.
- expanded smoke passed.
- full v8 benchmark passed.
- baseline/comparison completed.
- Elnath beats Claude Code.
- Elnath beats Codex.
- broad public benchmark superiority.

## Next Action

After merge, rerun only the one-task `V8-MIX-BF-001` current-only lane before
any 4-task or larger smoke.
