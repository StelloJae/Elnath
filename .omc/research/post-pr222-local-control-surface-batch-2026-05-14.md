# Post-PR222 local control-surface batch

Date: 2026-05-14
Branch: `codex/post-pr222-code-intel`
Status: local batch checkpoint

## Why this exists

The user explicitly flagged that PR and CI churn had become too frequent.

This batch therefore stays local first:

- no PR opened yet
- no CI triggered yet
- coherent local commits only
- focused and affected package verification first

## Local commits

- `ffa40242fda2762eb107892fcc2b72b63573ed92` — `feat(tools): add go reference lookup`
- `46415d7` — `fix(tools): enforce single active todo`

## Slice 1: code intelligence

Artifact:

- `.omc/research/code-symbols-references-go-native-2026-05-14.md`

Behavior:

- `code_symbols references`
- cursor-derived query from `file_path`, `line`, and `column`
- `code_symbols hover`
- ToolSearch schema-preview coverage for `references` and `hover`
- control-surface wording updated to symbols/definitions/references/hover

Reference inspected:

- Claude Code `LSPTool` schemas, prompt, implementation, and formatter files.

Verification:

- focused TDD failures observed before implementation
- focused `internal/tools` tests PASS
- control-surface `cmd/elnath` tests PASS
- affected package verification PASS:
  - `go test ./cmd/elnath ./internal/tools -count=1`
  - `go vet ./cmd/elnath ./internal/tools`
  - `git diff --check`

Boundary:

- Full LSP lifecycle is still not claimed.
- Code references are AST identifier based, not type resolved.

## Slice 2: todo scratchpad guard

Artifact:

- `.omc/research/todo-write-single-active-guard-2026-05-14.md`

Behavior:

- `todo_write` rejects multiple `in_progress` todos.
- scratchpad control-surface notes expose the single-active guard.

Reference inspected:

- Claude Code `TodoWriteTool` prompt.

Verification:

- focused TDD failure observed before implementation
- focused `todo_write` tests PASS
- control-surface `cmd/elnath` tests PASS
- affected package verification PASS:
  - `go test ./cmd/elnath ./internal/tools -count=1`
  - `go vet ./cmd/elnath ./internal/tools`
  - `git diff --check`

Boundary:

- UI-level task-list behavior is not claimed.
- Elnath still allows zero `in_progress` entries for all-pending or all-completed lists.

## Repository state

Main worktree `/Users/stello/elnath` remains untouched by this local batch and still has pre-existing dirty/untracked files:

- `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`
- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_benchmark_wrapper_v8_task_guidance.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`
- `.claude/`
- `docs/superpowers/plans/2026-04-30-elnath-local-managed-runtime.md`

## Benchmark / corpus / baseline

- Full v8 benchmark: no
- Baseline: no
- Codex CLI comparison: no
- Claude Code comparison: no
- Benchmark corpus mutation: no
- Baseline mutation: no

## Broad local verification

Before any PR/CI:

- `go test ./... -count=1`
  - PASS
  - notable packages:
    - `cmd/elnath 31.600s`
    - `internal/daemon 37.381s`
    - `internal/eval 22.406s`
    - `internal/telegram 16.928s`
    - `internal/tools 41.398s`
    - `internal/worktree 5.994s`
- `go vet ./...`
  - PASS
- `git diff --check HEAD~3..HEAD`
  - PASS

## Claim boundary

Allowed:

- The local branch has a tested code-intelligence/scratchpad control-surface batch.

Not allowed:

- Elnath is complete.
- Elnath has Claude Code-equivalent LSP behavior.
- v8 benchmark passed.
- Benchmark superiority is proven.

## Next recommendation

Do not open a tiny PR for each slice. Options:

1. Add one more small local control-surface slice, then open one batched PR.
2. If the user wants integration now, open one PR for this whole local batch.

Default next autonomous action: add one more small local slice only if fresh code evidence shows a clear control-loop gap; otherwise prepare a single batched PR.
