# Process Wait Tool

Date: 2026-05-14
Branch: `codex/process-wait-tool`
Status: implemented locally

## Goal

Add a bounded `process_wait` tool for session-local background processes.

This reduces waste from model-driven `sleep` polling loops and makes
long-running command wait behavior explicit, bounded, and receipt-backed.

## Reference Check

- Elnath has `process_start`, `process_monitor`, and `process_stop`, but no
  bounded blocking wait tool.
- Claude Code-style execution keeps background/interrupt/runtime state visible
  as control events.
- Hermes cron runtime treats waiting/inactivity as explicit scheduler policy.
- claw-code bash runtime returns structured timeout/interruption state.

Design choice: add an Elnath-native wait surface with its own receipt fields.
Do not copy reference implementation source, prompts, or errors.

## Intended Behavior

- `process_wait` waits for a process to become terminal up to a bounded
  `wait_ms`.
- Default wait should be short and capped.
- If the process becomes terminal, receipt records terminal status and output
  tail metadata.
- If wait expires while process is still running, receipt records
  `wait_timed_out=true` and a follow-up tool.
- Process runtime timeout remains separate from wait timeout:
  - `timed_out` means the process hit its own runtime timeout.
  - `wait_timed_out` means the wait call stopped waiting.

## Changed Files

- `internal/tools/process_tools.go`
- `internal/tools/process_tools_test.go`
- `internal/tools/schema_test.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_command_tool_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `internal/learning/outcome.go`
- `internal/agentic/completion/gate.go`

## Behavior Added

- Added `process_wait`.
- Added `wait_ms`, `wait_elapsed_ms`, and `wait_timed_out` receipt fields.
- Added wait policy to `ProcessExecutionPolicySnapshot()`.
- Registered `process_wait` in runtime, ToolSearch/control-surface metadata, and
  completion-control receipt collection.
- Propagated wait receipt fields into learning and agentic completion receipts.
- Updated `elnath explain timeouts` process policy output to include wait
  defaults and wait follow-up tool.

## Verification

- Initial TDD proof failed as expected:
  - `go test ./internal/tools -run 'TestProcess(Wait|ExecutionPolicySnapshot|ToolsAreDeferredInToolSearch)|TestToolDescriptionsMentionToolBoundaries' -count=1`
    - failed: `ProcessWaitTool`, `ProcessWaitToolName`, and wait policy fields
      undefined
  - `go test ./cmd/elnath -run 'TestExecutionRuntimeRegistersProcessTools|TestCmdExplainTimeouts(JSON|TextShowsCorrectionPolicy)|TestProcessControlReceiptsConvertToLearningAndAgentic' -count=1`
    - failed: `ProcessWaitToolName` and wait receipt fields undefined
- Focused verification after implementation:
  - `go test ./internal/tools -run 'TestProcess(Wait|ExecutionPolicySnapshot|ToolsAreDeferredInToolSearch)|TestToolDescriptionsMentionToolBoundaries' -count=1`
    - PASS: `ok github.com/stello/elnath/internal/tools 0.663s`
  - `go test ./cmd/elnath -run 'TestExecutionRuntimeRegistersProcessTools|TestCmdExplainTimeouts(JSON|TextShowsCorrectionPolicy)|TestProcessControlReceiptsConvertToLearningAndAgentic' -count=1`
    - PASS: `ok github.com/stello/elnath/cmd/elnath 0.680s`
- Proportional broader verification:
  - `go test ./internal/tools ./cmd/elnath ./internal/learning ./internal/agentic/completion -count=1`
    - PASS: `internal/tools 41.196s`, `cmd/elnath 21.810s`,
      `internal/learning 0.818s`, `internal/agentic/completion 1.582s`
  - `go vet ./internal/tools ./cmd/elnath ./internal/learning ./internal/agentic/completion`
    - PASS
  - `git diff --check`
    - PASS

## Boundary

- Full v8 benchmark: not run.
- Baseline: not run.
- Codex/Claude comparison: not run.
- Benchmark corpus mutation: none.
- Baseline artifact mutation: none.

## Claim Boundary

Allowed:

- Elnath now has a bounded `process_wait` tool for session-local background
  process waiting.
- `process_wait` emits receipt-backed wait metadata.
- Wait metadata is preserved into learning and agentic completion receipts.

Forbidden:

- Elnath benchmark success.
- Elnath is better than Claude Code or Codex.
- Full autonomous completion program is done.

## Remaining Risk

- This is bounded wait, not full streaming line-watch.
- UI-level interrupt/abort polish remains separate.

## Next Recommendation

Commit this milestone, open one PR, wait for CI, merge if green, then continue
to the next structural blocker without returning to benchmark loops.
