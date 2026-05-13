# Sleep Tool Parity Slice - 2026-05-13

## Summary

Branch: `codex/correction-attempt-details`

This slice adds a bounded `sleep` model-callable tool for polling/backoff
workflows. It is reference-backed by the Claude Code SleepTool prompt shape:
use a dedicated wait tool instead of holding a shell process with `sleep`.

## Reference Notes

Claude Code reference checked:

- `/Users/stello/claude-code-src/src/tools/SleepTool/prompt.ts`
- `/Users/stello/claude-code-src/src/tools/BashTool/BashTool.tsx`
- `/Users/stello/claude-code-src/src/tools/BashTool/prompt.ts`

Adapted behavior, not copied text:

- Elnath uses lowercase `sleep` to match existing snake-case tool naming.
- Elnath accepts `duration_ms` and caps waits at 5000 ms.
- Elnath records a compact `timer_wait` receipt.
- Elnath marks the tool read-only, reversible, concurrency-safe, and deferred
  from the initial tool schema.

## Change

- Added `internal/tools/sleep.go`.
- Added focused sleep tool tests.
- Registered `sleep` in the runtime tool registry.
- Added `tool_search` discovery coverage for deferred `sleep`.
- Added completion observability extraction for `sleep` receipts.

## Claim Boundary

Allowed:

- Elnath now has a dedicated bounded timer-wait tool.
- The tool avoids shell-process sleeps for short polling/backoff waits.
- The tool is discoverable through `tool_search` and receipt-backed in
  completion observability.

Not claimed:

- No long-running monitor loop added.
- No background process watcher added.
- No full Claude Code SleepTool parity.
- No v8 benchmark, baseline, Codex CLI comparison, or Claude Code comparison.

## Verification

Passed:

- `go test ./internal/tools -run 'TestSleepTool|TestToolSearchFindsSleepAsDeferredTimerWait|TestToolSearchReportsDeclaredDeferReason' -count=1`
- `go test ./cmd/elnath -run 'TestHelperBuilders|TestExecutionRuntimeRegistersDeferredControlSurfaceTools|TestCompletionContractSummaryRecordsSleepToolReceipt' -count=1`
- `go test ./internal/tools ./cmd/elnath -count=1`

## Remaining Risk

- The tool intentionally caps at 5000 ms. Longer autonomous waiting should use
  daemon tasks, task monitor wait, process monitor, or future scheduler/heartbeat
  surfaces instead of blocking a model turn.

## Next Recommendation

Keep batching this branch locally. Next useful slice: command/local slash
execution receipt polish or one small missing high-value command surface.
