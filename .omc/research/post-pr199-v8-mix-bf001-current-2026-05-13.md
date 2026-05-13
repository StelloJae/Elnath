# Post-PR199 V8-MIX-BF-001 current-only retry

Date: 2026-05-13
Branch: `main` for run, diagnosis branch `codex/v8-mix-regression-insertion-guard`
HEAD during run: `ec8ac7bf27bebb66bd14f7fd61f71523dd8d47b2`
Verdict: FAIL WITH NEW RUNTIME EVIDENCE

## Purpose

Validate PR #199 in the smallest benchmark/readiness lane:

- one task only: `V8-MIX-BF-001`
- current-only
- retained temp enabled
- no full v8 benchmark
- no baseline
- no Codex/Claude comparison

## Command

```bash
CURRENT_BIN=/Users/stello/elnath/scripts/run_current_benchmark_wrapper.sh \
ELNATH_BIN=/Users/stello/elnath/elnath \
ELNATH_BENCHMARK_KEEP_TMP=1 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
ELNATH_REASONING_EFFORT_MODE=auto \
ELNATH_REASONING_EFFORT=medium \
ELNATH_TIMEOUT=300 \
ELNATH_VERIFY_TIMEOUT=300 \
/Users/stello/elnath/elnath eval run-current .omc/research/post-pr199-v8-mix-bf001-current-20260513/current-plan.json
```

Plan:

- `.omc/research/post-pr199-v8-mix-bf001-current-20260513/current-plan.json`

Scorecard:

- `benchmarks/results/post-pr199-v8-mix-bf001-current-20260513/current-scorecard.json`

Retained root:

- `/var/folders/m_/fnd4jmdn6fv03bdwkqrwk1fh0000gn/T//elnath-current-benchmark.46huGW`

## Result

- total tasks: 1
- success + verified: 0
- verification passed: 1
- failure_family: `incomplete_patch`
- recovery_attempted: true
- `verification_unavailable`: 0
- duration: 445 seconds

Changed files:

- `api/internal/accumulator/namereferencetransformer.go`
- `go.work.sum`

Missing:

- `api/internal/accumulator/namereferencetransformer_test.go`

## What Improved

PR #199 worked as intended.

The first attempt recorded:

- `finish_reason=budget_exceeded`
- `completion_warning=budget_exceeded_after_edit_intent`
- `retry_decision=retry_smaller_scope`
- `retry_reason=budget_exceeded_after_edit_intent`

Evidence:

- retained `outcomes.jsonl`

This means the runtime completion-contract guard now catches the budget/edit
intent blind spot and routes it into bounded recovery.

## Remaining Root Cause

The first attempt did try to add a regression using a Python edit script.

The generated script used this marker:

- `func TestNameReferenceInCustomResources(t *testing.T) {`

That marker does not exist in the target file. Existing anchors include:

- `func TestNameReferenceHappyRun(t *testing.T) {`
- `func TestNameReferenceUnhappyRun(t *testing.T) {`
- `func TestNameReferenceCandidateSelection(t *testing.T) {`
- `func TestNameReferenceCandidateDisambiguationByNamespace(t *testing.T) {`

The script exited successfully, ran `cd kyaml && go test ./...`, and produced no
test diff. The wrapper then correctly failed closed because the final changed
files still lacked `namereferencetransformer_test.go`.

## Diagnosis

This run no longer points primarily at `budget_exceeded` detection.

It points at task-specific recovery/edit-application guidance:

- recovery must use a real anchor or append fallback
- recovery must prove the test file changed before running broad verification
- a successful no-op Python edit script must not be enough evidence

## Claim Boundary

Allowed:

- Post-PR199 one-task `V8-MIX-BF-001` current-only retry failed with
  `incomplete_patch`.
- PR #199 completion warning fired correctly.
- `verification_unavailable=0`.

Forbidden:

- one-task retry passed.
- expanded smoke passed.
- full v8 benchmark passed.
- baseline/comparison completed.
- Elnath beats Claude Code.
- Elnath beats Codex.
- broad public benchmark superiority.

## Next Action

Patch `V8-MIX-BF-001` recovery guidance narrowly:

- name a real insertion anchor such as `TestNameReferenceUnhappyRun`
- allow append-at-end fallback if anchor is missing
- require `git diff --name-only -- api/internal/accumulator/namereferencetransformer_test.go`
  to show the test file before running `cd kyaml && go test ./...`
