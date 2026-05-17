# Telegram Progress Text Delivery

Date: 2026-05-17 KST

Branch:

- `codex/progress-alive-status`

## Scope

This milestone improves gateway/delivery progress visibility for Telegram.

It does not change task execution, model behavior, benchmark wrappers, corpus,
or baselines.

## Reference Files Inspected

Elnath:

- `internal/telegram/sink.go`
- `internal/telegram/progress_reporter.go`
- `internal/telegram/sink_test.go`
- `internal/daemon/delivery.go`
- `internal/daemon/progress.go`

Claude Code source:

- `/Users/stello/claude-code-src/src/cli/remoteIO.ts`
- `/Users/stello/claude-code-src/src/remote/sdkMessageAdapter.ts`

Hermes references:

- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.8.0.md`
- `/Users/stello/.hermes/hermes-agent/cron/scheduler.py`

## Finding

Telegram progress delivery already handled:

- structured tool progress;
- stage markers;
- summary stream markers.

But generic daemon text progress, such as `daemon.TextProgressEvent("checking
repository status")`, was parsed and then dropped by `TelegramSink.OnProgress`.

That meant gateway progress could still become silent even though the daemon
emitted a valid progress event.

## Change

- Added generic text-event support to `ProgressReporter`.
- Added `ReportText` and a bounded escaped bullet-line renderer.
- Routed otherwise-unhandled daemon text progress into Telegram progress output.
- Preserved existing special routes for tool, stage, and summary events.

## TDD Evidence

Red:

- `go test ./internal/telegram -run TestSinkNotifyProgressRendersTextEvent -count=1`
  failed because no Telegram sent/edited message contained the text progress.

Green:

- `go test ./internal/telegram -run 'TestSinkNotifyProgressRendersTextEvent|TestSinkOnProgressRoutesToProgressReporter|TestSinkOnProgressSummaryRoutesToStream' -count=1`
  passed.

## Claim Boundary

Allowed:

- Telegram task progress no longer drops generic text progress events.
- Gateway/delivery progress visibility is stronger for long-running tasks.

Forbidden:

- Full rich TUI progress timeline.
- Claude Code remote keep-alive parity.
- Full Hermes continuity parity.
- Benchmark readiness or superiority claim.

## Remaining Risk

- ProgressReporter still uses throttled message edit batching, not a full
  timeline store.
- Telegram progress text is display-only and bounded to short escaped lines.
- Non-Telegram gateways may still need equivalent formatting.

## Next Recommendation

Run focused Telegram and daemon delivery tests, then treat this branch as one
coherent progress/alive-status PR candidate.
