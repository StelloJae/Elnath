# Explain Process Timeout Policy

Date: 2026-05-14
Branch: `codex/explain-timeout-policy`
Status: implemented locally

## Goal

Make Elnath's long-running process timeout and monitor policy visible through
`elnath explain timeouts`.

This follows the final completion program requirement that long-running command,
timeout, background, abort, and monitor behavior be explicit and tested before
benchmark-readiness validation widens again.

## Reference check

- Elnath process tools already carry process timeout state through
  `process_start`, `process_monitor`, and `process_stop` receipts.
- Claude Code remote session flow treats long-running runtime/control events as
  callback-visible state instead of hidden internal state.
- Hermes cron scheduler documents inactivity and delivery behavior around
  scheduled long-running jobs.
- claw-code bash runtime returns structured timeout/interruption metadata in
  command output.

Design choice: expose Elnath-native process policy as stable inspectable
metadata, without copying reference implementation names, prompts, or errors.

## Changed Files

- `internal/tools/process_tools.go`
- `internal/tools/process_tools_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Behavior Added

- Added `ProcessExecutionPolicySnapshot()` in `internal/tools`.
- `elnath explain timeouts --json` now includes `process` policy:
  - default timeout: `600000ms`
  - max timeout: `3600000ms`
  - kill grace: `2000ms`
  - default tail bytes: `4000`
  - max tail bytes: `20000`
  - monitor follow-up tool: `process_monitor`
  - receipt fields including `status`, `terminal`, `timed_out`, `timeout_ms`,
    `followup_tool`, `command_intent`, and `intent_source`
- Text output now prints the same process policy in the timeout report.

## Verification

- Initial TDD proof failed as expected:
  - `go test ./internal/tools -run TestProcessExecutionPolicySnapshot -count=1`
    - failed: `undefined: ProcessExecutionPolicySnapshot`
  - `go test ./cmd/elnath -run 'TestCmdExplainTimeouts(JSON|TextShowsCorrectionPolicy)' -count=1`
    - failed: process policy missing from JSON/text output
- Focused verification after implementation:
  - `go test ./internal/tools -run TestProcessExecutionPolicySnapshot -count=1`
    - PASS: `ok github.com/stello/elnath/internal/tools 0.604s`
  - `go test ./cmd/elnath -run 'TestCmdExplainTimeouts(JSON|TextShowsCorrectionPolicy)' -count=1`
    - PASS: `ok github.com/stello/elnath/cmd/elnath 0.571s`
- Proportional broader verification:
  - `go test ./cmd/elnath ./internal/tools -count=1`
    - PASS: `cmd/elnath 23.041s`, `internal/tools 40.402s`
  - `go vet ./cmd/elnath ./internal/tools`
    - PASS
  - `git diff --check`
    - PASS

## Benchmark / Corpus Boundary

- Full v8 benchmark: not run.
- Baseline: not run.
- Codex/Claude comparison: not run.
- Benchmark corpus mutation: none.
- Baseline artifact mutation: none.
- Benchmark superiority claim: none.

## Claim Boundary

Allowed:

- Process command timeout and monitor policy is now visible through
  `elnath explain timeouts`.
- Process timeout policy output is covered by focused Go tests.

Forbidden:

- Elnath benchmark success.
- Elnath is better than Claude Code or Codex.
- Full autonomous completion program is done.

## Remaining Risk

- This only makes timeout policy observable. It does not add new runtime timeout
  behavior.
- Streaming line-watch monitor and richer abort UX remain outside this slice.

## Next Recommendation

Commit this coherent milestone, open one PR, wait for CI, merge if green, then
continue to the next structural blocker instead of returning to benchmark loops.
