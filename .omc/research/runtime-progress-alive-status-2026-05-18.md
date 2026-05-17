# Runtime Progress / Alive Status Milestone

Date: 2026-05-18 KST

Branch:

- `codex/runtime-progress-status`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second

## Summary

This milestone adds typed runtime phase progress events so long Elnath runs can
surface what the runtime is doing between model/tool output bursts.

The change reuses Elnath's existing event bus and daemon progress envelope
instead of adding a new progress subsystem.

## References Inspected

Elnath:

- `.omc/research/elnath-ultimate-goal-codex-claude-hermes-convergence-2026-05-17.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`
- `internal/event/types.go`
- `internal/event/adapter.go`
- `internal/daemon/progress.go`
- `internal/daemon/delivery.go`
- `internal/telegram/progress.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_retry.go`
- `internal/agent/executor.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/gateway/stream_consumer.py`

Claude Code:

- `/Users/stello/claude-code-src/src/tools/AgentTool/UI.tsx`

## Changed Behavior

Added:

- `event.RuntimeProgressEvent`
- daemon progress kind `runtime`
- daemon-compatible runtime progress encoding/parsing
- terminal rendering for runtime progress
- daemon `ProgressObserver` forwarding for runtime progress
- legacy CLI callback rendering for runtime progress
- `task_monitor` / `elnath task monitor --json` parsed `progress_event`
  metadata for structured progress envelopes

Runtime now emits phase progress for:

- `prompt_build`
- `workflow_running`
- `completion_check`
- `session_persist`
- `completion_retry`
- `verification_retry`

## Product Impact

Before:

- long runs could look wedged between workflow routing, model calls,
  completion checks, retry, verification, and session persistence.

After:

- CLI/daemon progress consumers can distinguish runtime phase movement from
  model text and tool execution.
- Existing tool progress heartbeat remains unchanged.
- Telegram/daemon delivery can reuse the same structured progress envelope.

## Changed Files

- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_test.go`
- `cmd/elnath/cmd_task.go`
- `cmd/elnath/cmd_task_test.go`
- `internal/daemon/progress.go`
- `internal/daemon/task_tools.go`
- `internal/daemon/task_tools_test.go`
- `internal/event/types.go`
- `internal/event/adapter.go`
- `internal/event/event_test.go`
- `internal/event/adapter_test.go`
- `.omc/research/runtime-progress-alive-status-2026-05-18.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

## Verification

Focused verification:

- `go test ./internal/event ./internal/daemon ./cmd/elnath -run 'TestRuntimeProgressEvent|TestOnTextToSinkRuntimeProgressEncodesJSON|TestProgressObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestLegacyCallbackObserverDispatchesRepresentativeEventTypesAndIgnoresUnknown|TestExecutionRuntimeRunTaskEmitsRuntimePhaseProgress|TestDeliveryRouter_OnProgressParsesAndRoutes|TestCmdDaemonStatusRendersStructuredProgressEnvelope' -count=1`
- Result: PASS

- `go test ./internal/daemon ./cmd/elnath -run 'TestTaskMonitorToolReturnsParsedProgressEvent|TestCmdTaskMonitorWithQueueJSONIncludesParsedProgressEvent' -count=1`
- Result: PASS

Broader proportional verification:

- `go test ./cmd/elnath ./internal/event ./internal/daemon -count=1`
- Result: PASS

- `go vet ./...`
- Result: PASS

- `git diff --check`
- Result: PASS

## Benchmark Boundary

No benchmark lane was run.

- Full v8 benchmark: not run
- Baseline: not run
- Codex comparison: not run
- Claude comparison: not run
- Corpus mutation: none
- Baseline mutation: none

## Claim Boundary

Allowed:

- Elnath now has typed runtime phase progress events.
- CLI and daemon progress consumers can receive runtime phase updates.
- This improves alive-status visibility for long runs.

Not claimed:

- full Hermes streaming UX parity;
- full Claude Code TUI progress parity;
- live runtime migration;
- benchmark success;
- Codex/Claude/Hermes superiority.

## Remaining Risk

- Runtime progress is phase-level, not a rich interactive timeline.
- Telegram rendering is still through existing progress delivery surfaces.
- No native UI transcript grouping like Claude Code AgentTool UI yet.

## Next Recommendation

Continue product/runtime completion with one of:

- richer operator timeline/status view for task monitor;
- skill lifecycle curator policy;
- session handoff/resume recap polish.
