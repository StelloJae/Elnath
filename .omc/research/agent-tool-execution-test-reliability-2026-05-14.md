# Agent Tool Execution Test Reliability

Date: 2026-05-14
Branch: `codex/agent-test-reliability`
Base: `origin/main` (`89438dc`)

## Problem

The final completion control document lists `internal/agent` partition tests as
a known timing-sensitive risk. The current focused and broad package reruns
passed, but the tests still contain wall-clock upper/lower bounds such as
`elapsed < 100ms` and `elapsed >= 100ms`.

Those checks can fail under host load even when the scheduler semantics are
correct. They also prove less than the actual contract:

- compatible tools should overlap in execution
- conflicting tools should not overlap
- a conservative write-like tool should finish before a later read starts
- read batches after a blocking write should still run together

## References Checked

- Elnath: `internal/agent/executor.go`, `internal/agent/executor_test.go`
- Claude Code: `/Users/stello/claude-code-src/src/services/tools/toolOrchestration.ts`
- Claude Code: `/Users/stello/claude-code-src/src/services/tools/StreamingToolExecutor.ts`
- Hermes: `/Users/stello/.hermes/hermes-agent/model_tools.py`
- Hermes: `/Users/stello/.hermes/hermes-agent/environments/agent_loop.py`

## Reference Pattern

Claude Code and Hermes emphasize explicit execution state, cancellation
boundaries, and observable tool lifecycle behavior. The useful pattern for this
milestone is not to copy their implementation, but to make the test oracle
assert the intended execution relationship directly instead of relying on a
machine-speed wall-clock budget.

## Chosen Design

Test-only correction:

- keep production `partitionToolCalls` / `executeToolBatch` unchanged
- add a deterministic execution gate for parallel batch tests
- replace fragile elapsed-time assertions with interval relationship assertions
- require every expected parallel interval pair to overlap
- keep disjoint interval assertions for serial batches
- retain focused and broad Go test verification

## Changed Files

- `internal/agent/executor_test.go`
- `.omc/research/agent-tool-execution-test-reliability-2026-05-14.md`

## Verification

Initial baseline checks before the patch:

- `go test ./internal/agent -run 'TestPartition_WritesDifferentPaths_Parallel|TestPartition_BashBlocksReads' -count=20`
  - PASS: `ok github.com/stello/elnath/internal/agent 2.951s`
- `go test ./cmd/elnath ./internal/agent ./internal/tools ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1`
  - PASS:
    - `cmd/elnath` 35.160s
    - `internal/agent` 12.460s
    - `internal/tools` 51.376s
    - `internal/orchestrator` 2.805s
    - `internal/agentic/completion` 1.688s
    - `internal/learning` 3.825s

Post-patch checks:

- `gofmt -w internal/agent/executor_test.go`
  - PASS
- `go test ./internal/agent -run 'TestPartition_(AllReadsParallel|WritesDifferentPaths_Parallel|WritesSamePath_Serial|BashBlocksReads|ConservativeScopeSerializes)' -count=20`
  - PASS: `ok github.com/stello/elnath/internal/agent 7.752s`
- `go test ./internal/agent -count=1`
  - PASS: `ok github.com/stello/elnath/internal/agent 10.390s`
- `go test ./cmd/elnath ./internal/agent ./internal/tools ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1`
  - PASS:
    - `cmd/elnath` 33.423s
    - `internal/agent` 15.283s
    - `internal/tools` 48.136s
    - `internal/orchestrator` 2.837s
    - `internal/agentic/completion` 1.707s
    - `internal/learning` 2.768s
- `git diff --check`
  - PASS

## Impact / Risk

Runtime behavior changed: no.
Benchmark run: no.
Corpus/baseline changed: no.

Remaining risk: cancellation tests still use duration-based assertions to prove
cancel/non-cancel behavior. They are longer-duration and not the known failing
partition tests, so they were left unchanged for scope control.

## Next Milestone Recommendation

After this milestone is committed and PR-gated, continue to command execution /
process policy: classify command intent, timeout, background, abort, and monitor
behavior with receipt-backed tests.

## Claim Boundary

This milestone proves test reliability hardening for agent tool partition
semantics. It does not claim new runtime behavior, benchmark success, or
Claude/Codex superiority.
