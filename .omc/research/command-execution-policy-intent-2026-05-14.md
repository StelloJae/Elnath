# Command Execution Policy Intent

Date: 2026-05-14
Branch: `codex/command-process-policy`
Base: `origin/main` (`6fec17b`)

## Problem

The final completion control document lists command execution / process policy
as the next structural blocker. The previous shell/diff scope milestone added
changed-file supervision, but foreground and background command execution still
lacks an explicit command-intent field in the tool contract and receipts.

Current state:

- `bash` exposes timeout/cancel/status metadata, but not command intent or
  explicit foreground execution policy in the LLM-facing header.
- `process_start` / `process_monitor` / `process_stop` expose JSON receipts,
  but the lifecycle receipt does not carry why the command was started.
- Tool descriptions tell the model to prefer focused verification and
  `process_start` for long-running commands, but receipts do not preserve that
  policy decision.

## References Checked

- Elnath: `internal/tools/bash.go`
- Elnath: `internal/tools/bash_runner.go`
- Elnath: `internal/tools/bash_runner_direct.go`
- Elnath: `internal/tools/bash_output.go`
- Elnath: `internal/tools/process_tools.go`
- Elnath: `.omc/research/supervisor-shell-diff-scope-milestone-c-2026-05-14.md`
- Claude Code: `/Users/stello/claude-code-src/src/services/tools/StreamingToolExecutor.ts`
- Claude Code: `/Users/stello/claude-code-src/src/tools/BashTool/BashTool.tsx`
- Hermes: `/Users/stello/.hermes/hermes-agent/cron/scheduler.py`
- Hermes: `/Users/stello/.hermes/hermes-agent/model_tools.py`

## Reference Pattern

Claude Code treats bash execution as a policy-bearing tool event near the main
execution loop. Hermes makes long-running/background execution explicit through
timeouts, polling, cancellation, and visible status.

The Elnath-native adaptation is not to copy their code. The useful flow is to
make command intent, foreground/background policy, timeout, and follow-up
expectations durable in tool output and receipts.

## Chosen Design

Small slice:

- add a closed enum command intent:
  - `inspect`
  - `edit`
  - `focused_verify`
  - `broad_verify`
  - `diagnostic`
  - `background`
- add optional `intent` to `bash`
- add optional `intent` to `process_start`
- default `bash` intent to `diagnostic`
- default `process_start` intent to `background`
- reject unknown intents fail-closed
- surface intent and intent source in:
  - `BASH RESULT` metadata
  - `BashRunResult`
  - bash telemetry fields
  - `process_start` output and receipt
  - `process_monitor` snapshot and receipt
  - `process_stop` receipt

This is a policy/receipt improvement only. It does not change process execution
semantics, sandboxing, cancellation, or command allow/deny decisions.

## Test Plan

- `TestBashCommandIntentMetadata`
- `TestBashRejectsInvalidCommandIntent`
- `TestProcessToolsStartMonitorTerminalReceipt`
- `TestProcessToolsReportRunningMonitorFollowup`
- `TestProcessStartRejectsInvalidCommandIntent`

Focused commands after implementation:

```text
go test ./internal/tools -run 'TestBashCommandIntentMetadata|TestBashRejectsInvalidCommandIntent|TestProcessTools(StartMonitorTerminalReceipt|ReportRunningMonitorFollowup)|TestProcessStartRejectsInvalidCommandIntent' -count=1
go test ./internal/tools -count=1
git diff --check
```

## Implemented Behavior

Changed production files:

- `internal/tools/command_intent.go`
- `internal/tools/bash.go`
- `internal/tools/bash_runner.go`
- `internal/tools/bash_runner_direct.go`
- `internal/tools/bash_output.go`
- `internal/tools/process_tools.go`

Changed test files:

- `internal/tools/bash_test.go`
- `internal/tools/process_tools_test.go`

Behavior added:

- `bash` accepts optional `intent`
- `process_start` accepts optional `intent`
- allowed intents are closed enum:
  - `inspect`
  - `edit`
  - `focused_verify`
  - `broad_verify`
  - `diagnostic`
  - `background`
- invalid intent fails closed as a tool error
- `bash` defaults to `diagnostic` with `intent_source=default`
- `process_start` defaults to `background` with `intent_source=default`
- `BASH RESULT` now includes:
  - `execution_policy`
  - `command_intent`
  - `intent_source`
  - `timeout_ms`
- `BashRunResult` and bash telemetry now carry the same policy fields
- bare runner-level `BashRunRequest` values default empty policy fields to
  `foreground_shell` / `diagnostic` / `default` in rendered results
- process lifecycle snapshots/receipts carry `command_intent` and
  `intent_source` when a process exists

## Verification Results

Red test before implementation:

```text
go test ./internal/tools -run 'TestBashCommandIntentMetadata|TestBashRejectsInvalidCommandIntent|TestProcessTools(StartMonitorTerminalReceipt|ReportRunningMonitorFollowup)|TestProcessStartRejectsInvalidCommandIntent' -count=1
FAIL
```

Expected failures:

- `BASH RESULT` lacked `execution_policy`, `command_intent`,
  `intent_source`, and `timeout_ms`
- invalid bash/process intent was accepted
- process receipts did not carry command intent

Post-implementation:

```text
gofmt -w internal/tools/command_intent.go internal/tools/bash.go internal/tools/bash_runner.go internal/tools/bash_runner_direct.go internal/tools/bash_output.go internal/tools/process_tools.go internal/tools/bash_test.go internal/tools/process_tools_test.go
PASS

go test ./internal/tools -run 'TestBashCommandIntentMetadata|TestBashRejectsInvalidCommandIntent|TestProcessTools(StartMonitorTerminalReceipt|ReportRunningMonitorFollowup)|TestProcessStartRejectsInvalidCommandIntent' -count=1
PASS: ok github.com/stello/elnath/internal/tools 0.644s

go test ./internal/tools -count=1
PASS: ok github.com/stello/elnath/internal/tools 39.657s

go test ./cmd/elnath ./internal/agent ./internal/tools -count=1
PASS:
- cmd/elnath 22.221s
- internal/agent 14.137s
- internal/tools 39.856s

git diff --check
PASS
```

## Impact / Risk

Runtime command execution semantics changed: no.
Sandbox behavior changed: no.
Timeout behavior changed: no.
Benchmark run: no.
Corpus/baseline changed: no.

Known risk:

- This adds explicit intent receipts but does not yet auto-infer intent from
  arbitrary shell text.
- `process_monitor` for a missing process cannot know intent because the
  process record is absent.
- Streaming line-watch monitor remains deferred.

## Next Milestone Recommendation

Next structural blocker: command timeout/monitor policy clarity.

Recommended next slice:

- add explicit timeout/terminal status classification to process monitor docs or
  receipt policy where still vague
- consider an inactivity/line-watch monitor only after process lifecycle intent
  is stable
- keep full v8/baseline/comparison lanes blocked

## Claim Boundary

Allowed after tests pass:

- command intent is a closed enum for `bash` and `process_start`
- foreground/background process intent appears in receipts/metadata

Not claimed:

- new runtime isolation behavior
- full streaming line-watch monitor
- benchmark success
- Elnath beats Claude Code or Codex
