# Command execution policy Milestone D (2026-05-14)

## Problem

Elnath's Bash tool already emits structured `BASH RESULT` metadata:

- status
- exit code
- duration
- cwd
- timed_out
- canceled
- classification
- truncation fields

But completion receipts do not preserve command policy information as first-class runtime evidence.

Current completion logic can detect a verification command, but does not produce a general command-execution receipt answering:

- was this command inspect, edit, focused verify, broad verify, diagnostic, or background candidate?
- did it time out?
- was it canceled?
- was a long-running command better suited for `process_start`?
- was an explicit timeout requested?
- did the command use a working directory override?

That leaves command policy weaker than Claude Code / Hermes-style runtime discipline.

## Reference pattern

Claude Code source inspected:

- `/Users/stello/claude-code-src/src/tools/BashTool/BashTool.tsx`
- `/Users/stello/claude-code-src/src/tools/BashTool/prompt.ts`
- `/Users/stello/claude-code-src/src/services/tools/StreamingToolExecutor.ts`

Relevant pattern:

- Bash input schema carries timeout/background intent.
- Bash execution yields progress and structured terminal results.
- long-running commands can be backgrounded explicitly or automatically.
- Bash errors can cancel sibling subprocesses.
- model-facing Bash prompt strongly prefers dedicated tools and background process discipline.

Hermes references inspected:

- `/Users/stello/.hermes/hermes-agent/AGENTS.md`
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.8.0.md`
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.9.0.md`

Relevant pattern:

- background process notifications are explicit gateway policy
- inactivity/timeout behavior is runtime-visible
- progress/background/watch-pattern behavior is a first-class operational surface

Elnath source inspected:

- `internal/tools/bash.go`
- `internal/tools/bash_output.go`
- `internal/tools/process_tools.go`
- `internal/agent/executor.go`
- `cmd/elnath/runtime_completion_observability.go`

## Chosen Elnath-native design

First slice: command policy receipts in completion summaries.

Add `ShellCommandReceipts` to completion observability, learning outcomes, and completion gate receipts.

Receipt fields:

- `tool`
- `action`
- `command_class`
- `status`
- `classification`
- `timed_out`
- `canceled`
- `is_error`
- `timeout_ms`
- `working_dir_set`
- `command_len`
- `background_recommended`

Command classes:

- `focused_verify`
- `broad_verify`
- `edit`
- `inspect`
- `diagnostic`
- `background`
- `unknown`

This does not add auto-backgrounding yet. It makes command policy visible and receipt-backed first.

## Planned files

Production:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `internal/agentic/completion/gate.go`
- `internal/learning/outcome.go`

Tests:

- `cmd/elnath/runtime_completion_observability_test.go`

## Verification plan

Focused tests:

- bash broad verification command records `broad_verify`, timeout metadata, working-dir override, error status
- long-running dev/watch command records `background` and `background_recommended`
- outcome/gate conversion preserves shell command receipts

Commands:

```text
go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsShellCommandReceipts|TestCompletionContractSummaryFlagsBackgroundShellCommand|TestRecordOutcomePersistsCompletionObservability|TestCompletionGateContextProviderConsumesRuntimeSummary' -count=1
go test ./cmd/elnath ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1
git diff --check
```

## Benchmark policy

No benchmark run for this milestone.

Forbidden:

- full v8
- baseline
- Codex CLI comparison
- Claude Code comparison
- public superiority claim

## Implemented behavior

Milestone D first slice added shell command policy receipts.

Behavior added:

- completion summaries now record `ShellCommandReceipts`
- Bash tool uses are classified without storing raw command text
- command classes include:
  - `focused_verify`
  - `broad_verify`
  - `edit`
  - `inspect`
  - `diagnostic`
  - `background`
  - `unknown`
- `BASH RESULT` header metadata is parsed into receipts:
  - `status`
  - `classification`
  - `timed_out`
  - `canceled`
  - `is_error`
- model-requested execution bounds are preserved:
  - `timeout_ms`
  - `working_dir_set`
  - `command_len`
  - `background_recommended`
- learning outcomes and agentic completion gate contexts preserve shell command receipts

Changed production files:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `internal/agentic/completion/gate.go`
- `internal/learning/outcome.go`

Changed test files:

- `cmd/elnath/runtime_completion_observability_test.go`

## Verification results

Focused command policy receipt tests:

```text
go test ./cmd/elnath -run 'TestCompletionContractSummaryRecordsShellCommandReceipts|TestCompletionContractSummaryFlagsBackgroundShellCommand|TestRecordOutcomePersistsCompletionObservability|TestCompletionGateContextProviderConsumesRuntimeSummary' -count=1
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

## Final claim boundary

Allowed:

- Milestone D first slice made Bash command policy visible in completion receipts.
- Elnath now records command class, timeout/cancel status, timeout request, working-dir override, and background recommendation for Bash tool uses.
- Raw Bash command text is not persisted in the new receipt.

Not claimed:

- Elnath now auto-backgrounds long commands like Claude Code
- Bash errors now cancel sibling tool execution
- all command execution policy parity is complete
- v8 benchmark passed
- Elnath beats Claude Code or Codex

## Remaining risk

- This slice is observability-first. It does not yet change Bash runtime behavior.
- Background recommendation is heuristic, not a policy-enforced redirect to `process_start`.
- Sibling cancellation policy remains explicit future work.
- Existing unrelated dirty files remain outside this milestone and must not be included in commit/PR scope.

## Next milestone recommendation

Next structural blocker: Milestone E, tool/prompt guidance parity.

Recommended focus:

- add Elnath-native Bash/process guidance based on the new command policy receipts
- steer long-running commands toward `process_start`
- steer broad verification failures away from edit permission
- keep receipt language closed-enum and prompt-injection safe
