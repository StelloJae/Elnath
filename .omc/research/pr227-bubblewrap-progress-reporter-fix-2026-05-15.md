# PR #227 Bubblewrap progress reporter fix - 2026-05-15

## Summary

Branch: `codex/product-runtime-user-input`
PR: `https://github.com/StelloJae/Elnath/pull/227`

PR #227 failed Bubblewrap CI in `internal/telegram`.

Failure:

- Check: Bubblewrap substrate
- Command: `go test -race -count=1 ./...`
- Failing test: `TestProgressReporterBatchesTools`
- Observed output sometimes contained only the first one or two tool lines,
  e.g. `bash` without `read_file` / `edit_file`.

## Root cause

`ProgressReporter` flushed the first progress event immediately because
`lastFlush` started as the zero time.

Under the race detector and Linux scheduler timing, the reporter goroutine could
flush after the first tool event before later tool events were drained. The test
expected quick consecutive tool events to batch together, but the production
logic did not guarantee an initial debounce window.

## Change

Changed files:

- `internal/telegram/progress_reporter.go`
- `internal/telegram/progress_reporter_test.go`

Behavior:

- Initialize `lastFlush` to `time.Now()` so the first progress update also uses
  the normal debounce interval.
- Update progress reporter tests to wait for `progressEditInterval + 100ms`
  instead of a fixed `500ms`.

This makes the tested behavior match the product intent: quickly queued tool
events batch into one progress message instead of racing against the first
flush.

## Verification

Reproduction:

```bash
go test -race ./internal/telegram -run TestProgressReporterBatchesTools -count=20
```

Result before fix: FAIL reproduced locally.

Focused verification:

```bash
go test -race ./internal/telegram -run 'TestProgressReporterBatchesTools|TestProgressReporterEditsExisting|TestProgressReporterFinishFlushes|TestProgressReporterDedup|TestProgressReporterStage' -count=20
```

Result: PASS (`ok github.com/stello/elnath/internal/telegram 72.813s`).

Package verification:

```bash
go test ./internal/telegram -count=1
```

Result: PASS (`ok github.com/stello/elnath/internal/telegram 16.198s`).

```bash
go test -race -count=1 ./internal/telegram
```

Result: PASS (`ok github.com/stello/elnath/internal/telegram 20.247s`).

```bash
git diff --check
```

Result: PASS.

## Benchmark boundary

No benchmark run.
No full v8.
No baseline.
No Codex/Claude comparison.
No benchmark superiority claim.
No corpus or baseline mutation.

## Remaining risk

This only fixes the PR #227 Bubblewrap race failure. The full GitHub CI must
rerun after push.

## Next action

Commit and push this fix to PR #227, then re-check Bubblewrap and Seatbelt CI.
