# Supervisor verification ownership Milestone B (2026-05-14)

## Problem

Elnath currently treats verification failure as one generic warning:

- `verification_command_failed`

That is too coarse. A focused task-local verifier failure can safely trigger a bounded correction retry. A broad or harness-owned verifier failure should not automatically become model edit permission.

This is the same structural gap documented in:

- `/Users/stello/elnath/.omc/research/claude-code-vs-elnath-control-loop-diagnosis-2026-05-14.md`
- `/Users/stello/elnath/.omc/research/elnath-control-loop-structural-correction-2026-05-14.md`
- `/Users/stello/elnath/.omc/research/supervisor-scope-drift-guard-milestone-a-2026-05-14.md`

## Reference pattern

Claude Code reference inspected:

- `/Users/stello/claude-code-src/src/query.ts`
- `/Users/stello/claude-code-src/src/tools/BashTool/BashTool.tsx`

Relevant pattern:

- model call, tool execution, abort, and follow-up are held close inside the query loop
- Bash command schema has explicit timeout/background fields
- tool execution results are normalized before continuation
- command behavior is treated as policy-bearing execution, not just free text

Hermes reference inspected:

- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.8.0.md`
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.5.0.md`

Relevant pattern:

- long-running/background/timeout behavior is surfaced as runtime policy
- retry/fallback and gateway execution events are visible to the user
- inactivity/timeout policy distinguishes active work from stale work

Elnath source inspected:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_retry.go`
- `internal/orchestrator/types.go`
- `cmd/elnath/runtime.go`

## Chosen Elnath-native fix

Add a verification ownership classifier to completion summaries.

First slice:

- classify verification command as `focused`, `broad`, or `unknown`
- classify ownership as `model`, `harness`, `diagnostic`, or `unknown`
- focused model-owned verification failure keeps existing behavior:
  - `completion_warning=verification_command_failed`
  - retry decision remains `retry_smaller_scope`
- broad or harness-owned verification failure fails closed:
  - `completion_warning=broad_verification_failed` or `harness_verification_failed`
  - no automatic correction retry
- record classification in outcome/gate receipts
- support environment override:
  - `ELNATH_VERIFICATION_CLASS`
  - `ELNATH_VERIFICATION_OWNERSHIP`

This does not try to solve log-level unrelated-failure detection yet. It prevents the worst current behavior: broad verifier failure becoming automatic edit permission.

## Planned files

Production:

- `internal/orchestrator/types.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `internal/agentic/completion/gate.go`
- `internal/learning/outcome.go`

Tests:

- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime_test.go`

## Verification plan

Focused tests:

- focused verifier failure remains retryable
- broad verifier failure stops retry
- env ownership/class overrides are recorded

Commands:

```text
go test ./cmd/elnath -run 'TestCompletion.*Verification|TestRuntimeVerificationPolicyFromEnv' -count=1
go test ./cmd/elnath ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1
git diff --check
```

## Benchmark policy

No benchmark run for this milestone unless local tests expose a need for one retained one-task proof after implementation.

Forbidden in this milestone:

- full v8
- baseline
- Codex CLI comparison
- Claude Code comparison
- public superiority claim

## Implemented behavior

Milestone B adds receipt-backed verification ownership classification to the completion contract.

Behavior added:

- failed focused/model-owned verification remains retryable as `verification_command_failed`
- failed broad verification fails closed as `broad_verification_failed`
- failed harness-owned verification fails closed as `harness_verification_failed`
- broad/harness verification failures clear retry decision/reason instead of granting automatic edit permission
- completion gate receipts now include `verification_class` and `verification_ownership`
- learning outcome records now include `verification_class` and `verification_ownership`
- runtime env overrides are supported:
  - `ELNATH_VERIFICATION_CLASS`
  - `ELNATH_VERIFICATION_OWNERSHIP`

Changed production files:

- `internal/orchestrator/types.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `internal/agentic/completion/gate.go`
- `internal/learning/outcome.go`

Changed test files:

- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime_test.go`

## Verification results

Focused verification:

```text
go test ./cmd/elnath -run 'TestCompletion.*Verification|TestRuntimeVerificationPolicyFromEnv' -count=1
PASS
```

Proportional package verification:

```text
go test ./cmd/elnath ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1
PASS
```

Whitespace/check verification:

```text
git diff --check
PASS
```

Benchmark:

- not run
- no full v8
- no baseline
- no Codex CLI comparison
- no Claude Code comparison

Corpus/baseline:

- benchmark corpus not changed
- baseline artifacts not changed

## Claim boundary

Allowed:

- Milestone B implemented verification ownership classification.
- Broad or harness-owned verification failure no longer becomes automatic correction retry permission.
- Focused model-owned verification failure keeps existing bounded retry behavior.

Not claimed:

- v8 benchmark passed
- Elnath beats Claude Code
- Elnath beats Codex
- broad benchmark superiority
- full self-correction parity with Claude Code

## Remaining risk

- Broad/focused classification is still heuristic plus explicit env override, not a deep log-semantic analyzer.
- This milestone blocks one important waste pattern but does not yet fully separate focused verifier ownership from unrelated broad CI/package failures at log-line granularity.
- Existing unrelated dirty files remain outside this milestone and must not be included in its commit.

## Next milestone recommendation

Milestone C should implement focused-vs-broad verifier separation at the verifier-result level:

- record verifier source and scope explicitly when available
- classify unrelated broad failures separately from task-owned failures
- keep retry authority tied to task-owned focused failures only
- continue using Claude Code/Hermes flow references without copying proprietary code or prompts
