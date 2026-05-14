# Supervisor shell/diff scope Milestone C (2026-05-14)

## Problem

Milestone A detects scope drift when explicit file tools expose paths:

- `edit_file`
- `write_file`

But shell-style mutations are weaker:

- `bash`
- `worktree_run`
- `apply_patch`
- formatter commands
- generated edits through scripts

Current Elnath can often tell that a shell command is mutating, but cannot reliably know which files changed from the completion messages alone.

That means a bounded correction retry can still mutate an unrelated file through shell and avoid `scope_drift` classification.

## Reference pattern

Claude Code reference inspected:

- `/Users/stello/claude-code-src/src/services/tools/StreamingToolExecutor.ts`
- `/Users/stello/claude-code-src/src/tools/BashTool/BashTool.tsx`

Relevant pattern:

- Bash execution is treated as policy-bearing execution, not generic text.
- Tool execution is kept close to query/control-loop state.
- Bash failure can cancel sibling subprocesses.
- Bash has richer metadata around timeout/background behavior.
- File updates are tracked through file-history/edit notification paths when possible.

Hermes reference inspected:

- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.7.0.md`
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.8.0.md`
- `/Users/stello/.hermes/hermes-agent/RELEASE_v0.9.0.md`

Relevant pattern:

- inline diff / changed-state visibility matters for agent trust
- background/timeout behavior is explicit runtime policy
- gateway/runtime events should be visible and scoped, not inferred from prose

Elnath source inspected:

- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_retry.go`
- `internal/tools/bash.go`
- `internal/tools/bash_runner_direct.go`
- `internal/agent/executor.go`

## Chosen Elnath-native design

Smallest durable fix:

Capture changed files around bounded correction retries using git status snapshots when available.

Behavior:

- before `runSmallerScopeCompletionRetry`, snapshot changed files in the active tool workspace
- after retry, snapshot changed files again
- compute newly changed files introduced by the retry
- merge those files into completion `MutatedPaths`
- compare them against `CorrectionScope`
- if any newly changed file is outside scope, classify as `scope_drift`
- record changed files in correction attempt detail
- fail closed instead of treating unrelated shell mutation as success

This is intentionally not a full shell AST path extractor. It covers shell/apply_patch/script mutations by observing the worktree result.

## Planned files

Production:

- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `internal/agentic/completion/gate.go`
- `internal/learning/outcome.go`

Tests:

- `cmd/elnath/runtime_test.go`
- `cmd/elnath/runtime_completion_observability_test.go`

## Verification plan

Focused tests:

- correction retry that mutates unrelated file through shell/diff snapshot fails closed as `scope_drift`
- correction attempt receipt records `changed_files`
- explicit file-tool scope drift behavior remains unchanged

Commands:

```text
go test ./cmd/elnath -run 'TestCompletionRetry.*Scope|TestCompletion.*ScopeDrift|TestCompletion.*CorrectionAttempt' -count=1
go test ./cmd/elnath ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1
git diff --check
```

## Benchmark policy

No benchmark run for this milestone unless local verification proves insufficient.

Forbidden:

- full v8
- baseline
- Codex CLI comparison
- Claude Code comparison
- public superiority claim

## Initial claim boundary

Allowed after implementation and tests pass:

- Elnath can detect newly changed files introduced during bounded correction retry when a git worktree is available.
- Out-of-scope retry-introduced shell/diff mutations fail closed as `scope_drift`.

Not allowed:

- all shell mutations are globally detected
- v8 benchmark passed
- Elnath beats Claude Code or Codex
- broad public benchmark superiority

## Implemented behavior

Milestone C implemented git-status snapshot supervision around bounded smaller-scope correction retries.

Behavior added:

- before a bounded correction retry, Elnath snapshots changed files in the active tool workspace when it is a git worktree
- after the retry, Elnath snapshots changed files again
- newly changed files introduced by the retry are merged into `MutatedPaths`
- newly changed files are compared against configured `CorrectionScope`
- out-of-scope retry-introduced changes are classified as `scope_drift`
- `scope_drift` remains fail-closed and clears retry decision/reason
- correction attempt detail now records `changed_files`
- agentic completion gate and learning outcome receipts preserve `changed_files`

Changed production files:

- `cmd/elnath/runtime_completion_diff.go`
- `cmd/elnath/runtime_completion_retry.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_gate_context.go`
- `cmd/elnath/runtime.go`
- `internal/agentic/completion/gate.go`
- `internal/learning/outcome.go`

Changed test files:

- `cmd/elnath/runtime_test.go`

## Verification results

Focused shell/diff scope test:

```text
go test ./cmd/elnath -run 'TestCompletionRetryFailsClosedOnShellDiffScopeDrift' -count=1
PASS
```

Focused completion retry/scope/receipt tests:

```text
go test ./cmd/elnath -run 'TestCompletionRetry.*Scope|TestCompletion.*ScopeDrift|TestCompletion.*CorrectionAttempt|TestRecordOutcomePersistsCompletionObservability|TestCompletionGateContextProviderConsumesRuntimeSummary' -count=1
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

- Milestone C added git-worktree-based changed-file delta supervision for bounded correction retry.
- Retry-introduced shell/diff mutations outside `CorrectionScope` fail closed as `scope_drift` when a git snapshot is available.
- Correction attempt receipts now expose `changed_files`.

Not claimed:

- all shell mutations are globally detected
- dirty files that existed before retry are newly classified as retry-introduced drift
- non-git workspaces get diff supervision
- v8 benchmark passed
- Elnath beats Claude Code or Codex
- broad public benchmark superiority

## Remaining risk

- Diff supervision only works when the active tool workspace is a git worktree.
- The delta method detects newly changed files. If a retry mutates a file already dirty before the retry, this first slice may not distinguish it.
- This does not yet add Claude Code-style foreground/background command policy or sibling cancellation changes.
- Existing unrelated dirty files remain outside this milestone and must not be included in commit/PR scope.

## Next milestone recommendation

Next structural blocker: Milestone D command execution policy parity.

Recommended focus:

- classify command intent as inspect, edit, focused_verify, broad_verify, diagnostic, or background
- make timeout/background guidance explicit in receipts and tool docs
- decide and test whether Bash error should cancel sibling tool execution in selected modes
- keep benchmark lanes blocked until command policy evidence is clearer
