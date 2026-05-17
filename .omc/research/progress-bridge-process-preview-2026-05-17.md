# Progress Bridge Process Preview Slice

Date: 2026-05-17 KST

Branch: `codex/progress-bridge-events`

Goal lane:

- Codex-Claude-Hermes convergence
- Product/runtime first
- Benchmark second
- Current slice: async progress bridge polish for process tools

## Summary

The current gap map says Elnath needs better async progress visibility. Direct
code inspection shows Elnath already has a typed event bus and tool progress
events:

- `internal/event/types.go`
- `internal/agent/agent.go`
- `cmd/elnath/runtime.go`
- `internal/tools/process_tools.go`

The smallest current gap is not absence of progress events. It is that
`process_monitor`, `process_wait`, and `process_stop` emit generic progress
lines without useful process context because `extractToolPreview` does not
understand `process_id`, `wait_ms`, or `watch_text`.

## References Inspected

Elnath:

- `internal/event/types.go`
- `internal/agent/agent.go`
- `internal/agent/executor.go`
- `internal/tools/process_tools.go`
- `cmd/elnath/runtime.go`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`

Hermes:

- `/Users/stello/.hermes/hermes-agent/gateway/stream_consumer.py`
- `/Users/stello/.hermes/hermes-agent/website/docs/user-guide/features/lsp.md`

Claude/Codex local patterns:

- tool progress is event-stream oriented, not raw log dump oriented.

## Chosen Design

Add process-aware preview formatting inside `internal/agent.extractToolPreview`:

- `process_monitor`: `process_id=<id>`
- `process_wait`: `process_id=<id> wait_ms=<ms> watch_text=<text>`
- `process_stop`: `process_id=<id>`

This keeps the existing event bus and daemon progress protocol unchanged.

## Non-Goals

- no new event bus type;
- no Telegram platform expansion;
- no long-running line-watch streaming;
- no benchmark run;
- no broad UX parity claim.

## Acceptance

- focused test proves process previews contain `process_id`, `wait_ms`, and
  `watch_text` where applicable;
- existing tool progress phase tests still pass;
- no unrelated runtime changes.

## Claim Boundary

Allowed after this slice if verified:

- Elnath process tool progress messages are more informative.
- The async progress bridge substrate remains unchanged.

Forbidden:

- Hermes-grade stream bridge parity;
- native Telegram/Discord progress UX parity;
- benchmark readiness claim.

## Implementation Result

Changed files:

- `internal/agent/agent.go`
- `internal/agent/agent_test.go`

Behavior added:

- `process_monitor` and `process_stop` tool progress previews now show
  `process_id=<id>`.
- `process_wait` tool progress previews now show `process_id=<id>`,
  `wait_ms=<ms>`, and `watch=<text>` when provided.
- Existing planned/running/done tool progress phases stay unchanged.

## Verification

Commands run from `/Users/stello/elnath/.worktrees/progress-bridge-events`:

- `go test ./internal/agent -run 'TestExtractToolPreviewIncludesProcessContext|TestExecuteToolsEmitsToolExecutionProgressPhases' -count=1`
  - PASS: `ok github.com/stello/elnath/internal/agent 0.547s`
- `go test ./internal/agent ./cmd/elnath -count=1`
  - PASS: `internal/agent 11.663s`, `cmd/elnath 17.091s`
- `git diff --check`
  - PASS
- `go vet ./...`
  - PASS
- `go test ./internal/... ./cmd/elnath -count=1`
  - PASS: all listed internal packages and `cmd/elnath`

## Remaining Risk

- This is progress preview polish, not a full Hermes-style stream bridge.
- It improves user-visible process context but does not add platform-native
  buttons, interrupt UI, or line-by-line gateway streaming.

## Next Recommendation

If this slice remains clean after broader checks, commit as one small progress
UX milestone. Next structural blocker after this should stay in product/runtime
UX, likely session handoff/resume recap or richer process progress delivery.
