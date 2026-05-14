# Supervisor scope-drift guard Milestone A (2026-05-14)

## 요약

Milestone A implemented the first structural guard from:

- `/Users/stello/elnath/.omc/research/elnath-control-loop-structural-correction-2026-05-14.md`
- `/Users/stello/elnath/.omc/research/claude-code-vs-elnath-control-loop-diagnosis-2026-05-14.md`

This is not a benchmark rerun lane. It is a runtime/control-loop correction.

Primary behavior added:

- Elnath can now carry a correction scope into workflow completion/retry logic.
- Runtime completion summaries record allowed recovery paths, forbidden recovery paths, observed mutating paths, and out-of-scope changed files.
- Out-of-scope successful edit/write tool mutations are classified as `scope_drift`.
- `scope_drift` is fail-closed: it does not schedule another automatic retry.
- If a bounded correction retry drifts out of scope, the correction is marked failed with `correction_failure_family=scope_drift`.
- Retry prompts now include explicit scope-lock guidance when scope is configured.
- `ELNATH_CORRECTION_SCOPE_LABEL`, `ELNATH_CORRECTION_SCOPE_ALLOWED_PATHS`, and `ELNATH_CORRECTION_SCOPE_FORBIDDEN_PATHS` can inject scope from harnesses/wrappers without changing prompts.

## Branch

- Branch: `codex/supervisor-scope-drift-guard`
- PR: none yet
- Commit: pending at artifact write time

## Changed files

Milestone A implementation files:

- `internal/orchestrator/types.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `internal/agentic/completion/gate.go`
- `internal/learning/outcome.go`

Milestone A test files:

- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime_test.go`

Existing dirty files from the prior benchmark-wrapper lane were present before this milestone and are not Milestone A implementation scope:

- `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`
- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_benchmark_wrapper_v8_task_guidance.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`

## Verification

Focused test:

```text
go test ./cmd/elnath -run 'TestCompletion(ContractSummaryDetectsScopeDriftForOutOfScopeEdit|ContractSummaryAllowsInScopeEdit|RetryPromptIncludesScopeLock|RetryFailsClosedOnScopeDrift)|TestRuntimeCorrectionScopeFromEnv' -count=1
```

Result:

```text
ok  	github.com/stello/elnath/cmd/elnath	1.179s
```

Broader proportional test:

```text
go test ./cmd/elnath ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1
```

Result:

```text
ok  	github.com/stello/elnath/cmd/elnath	39.500s
ok  	github.com/stello/elnath/internal/orchestrator	1.562s
ok  	github.com/stello/elnath/internal/agentic/completion	2.436s
ok  	github.com/stello/elnath/internal/learning	3.988s
```

Whitespace check:

```text
git diff --check
```

Result: PASS, no output.

## Benchmark policy

Not run:

- full v8 benchmark
- selected v8 smoke
- baseline
- Codex CLI comparison
- Claude Code comparison

Mutations:

- benchmark corpus changed: no
- baseline changed: no

## Claim boundary

Allowed:

- Milestone A added runtime support for scope-locked correction/retry.
- Out-of-scope edit/write tool mutations can now be detected as `scope_drift` when correction scope is configured.
- Bounded correction retry now fails closed on `scope_drift`.
- Harnesses can pass correction scope through `ELNATH_CORRECTION_SCOPE_*` environment variables.

Not allowed:

- v8 benchmark passed.
- Elnath is better than Claude Code.
- Elnath is better than Codex.
- Broad benchmark superiority is proven.
- All possible shell-based out-of-scope mutations are detected. This milestone detects explicit `edit_file` and `write_file` paths; shell diff/file-family enforcement remains a later supervisor/harness layer.

## Remaining risk

This is the first structural guard, not the whole supervisor refactor.

Remaining gaps:

- shell-based mutations do not yet produce exact mutated paths in runtime completion summaries
- benchmark wrappers still need to opt into `ELNATH_CORRECTION_SCOPE_*` for real benchmark enforcement
- broad/focused verification ownership classifier is still Milestone B
- command class receipts are still not fully supervisor-owned

## Next recommendation

Next milestone: Milestone B, verification ownership classifier.

Do not widen benchmark yet. First wire a small harness or wrapper path to set `ELNATH_CORRECTION_SCOPE_*`, then run one retained one-task current-only check only if needed to prove the guard blocks real scope drift.
