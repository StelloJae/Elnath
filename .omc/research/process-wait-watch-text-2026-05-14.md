# Process wait watch text

Date: 2026-05-14
Branch: `codex/post-pr217-next`
Lane: final completion program / streaming process observation slice
Status: implemented locally

## Problem

After PR #211, Elnath had bounded `process_wait`, but it could only return when
a process became terminal or `wait_ms` expired. It could not return early when a
specific output marker appeared.

Hermes has watch-pattern process notifications with rate limits and global
circuit breakers. Elnath does not need that full async notification system yet,
but it does need a bounded, receipt-backed way to wait for an output marker
without sleep polling.

## References inspected

- Elnath: `internal/tools/process_tools.go`
- Elnath: `internal/tools/process_tools_test.go`
- Elnath: `cmd/elnath/runtime_completion_observability.go`
- Hermes: `/Users/stello/.hermes/hermes-agent/tools/process_registry.py`
- claw-code: `/Users/stello/claw-code/rust/crates/tools/src/lib.rs`

Hermes watch patterns and claw-code process wait/poll behavior were used as
behavior references only. Elnath keeps a smaller Go-native bounded wait design.

## Design

Add optional literal `watch_text` to `process_wait`:

- `watch_text` is trimmed and capped at 200 runes
- wait returns early when `watch_text` appears in stdout or stderr tail
- output records `watch_text`, `watch_matched`, and `watch_stream`
- receipt records the same fields
- completion, learning, and agentic receipt paths preserve the fields
- if no watch text is supplied, existing `process_wait` behavior is unchanged

This is not full streaming line-watch or async notification. It is the smallest
bounded observation primitive that removes common sleep-poll loops.

## Changed files

- `internal/tools/process_tools.go`
- `internal/tools/process_tools_test.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `internal/learning/outcome.go`
- `internal/agentic/completion/gate.go`
- `.omc/research/process-wait-watch-text-2026-05-14.md`

## Verification

- Initial TDD check:
  - `go test ./internal/tools -run 'TestProcessWaitReturnsWhenWatchTextAppears|TestProcessExecutionPolicySnapshot' -count=1`
  - FAIL as expected before implementation: `process_wait` saw `READY` in stdout but still returned `wait_timed_out=true`; policy snapshot lacked watch receipt fields.
- Focused after implementation:
  - `go test ./internal/tools ./cmd/elnath -run 'TestProcessWaitReturnsWhenWatchTextAppears|TestProcessExecutionPolicySnapshot|TestCompletionContractSummaryRecordsProcessToolReceipts|TestProcessControlReceiptsConvertToLearningAndAgentic' -count=1`
  - PASS: `internal/tools 0.712s`, `cmd/elnath 0.578s`
- Broader affected packages:
  - `go test ./internal/tools ./cmd/elnath ./internal/learning ./internal/agentic/completion -count=1`
  - PASS: `internal/tools 41.288s`, `cmd/elnath 23.557s`, `internal/learning 1.375s`, `internal/agentic/completion 1.485s`
- Vet:
  - `go vet ./internal/tools ./cmd/elnath ./internal/learning ./internal/agentic/completion`
  - PASS
- Whitespace:
  - `git diff --check`
  - PASS

## Benchmark / corpus / baseline

- Full v8 benchmark: no
- Current-only smoke: no
- Baseline: no
- Codex CLI comparison: no
- Claude Code comparison: no
- Benchmark corpus mutation: no
- Baseline mutation: no

## Claim boundary

Allowed:

- `process_wait` can now return early when bounded literal `watch_text` appears
  in stdout or stderr.
- Watch evidence is preserved in process receipts, learning records, and
  agentic completion context.

Forbidden:

- Full streaming line-watch notification exists.
- Hermes-style async watch-pattern queue exists.
- Benchmark success or superiority.

## Remaining risk

- Matching is literal substring only, not regex.
- Matching is based on bounded output tails, not unbounded historical output.
- No async notification or global rate-limit circuit breaker is implemented.

## Next autonomous action

Commit this process observation slice, open one coherent PR, and use CI as the
merge gate. Do not run benchmark lanes for this slice.
