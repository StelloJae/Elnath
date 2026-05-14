# Tool guidance policy milestone E (2026-05-14)

## Status

Implemented and locally verified.

## Branch

`codex/supervisor-scope-drift-guard`

## Problem found

Elnath now records shell command policy receipts, but the model-facing tool descriptions still under-specify supervisor discipline.

The weak spot is before execution: the model can still choose foreground `bash` for long-running work, jump from broad verification failure into unrelated edits, or skip focused verification order unless the task prompt happens to remind it.

## References inspected

- `/Users/stello/elnath/.omc/research/elnath-completion-program-control-2026-05-14.md`
- `/Users/stello/elnath/.omc/research/claude-code-vs-elnath-control-loop-diagnosis-2026-05-14.md`
- `/Users/stello/elnath/.omc/research/elnath-control-loop-structural-correction-2026-05-14.md`
- `/Users/stello/claude-code-src/src/tools/BashTool/prompt.ts`
- `/Users/stello/claude-code-src/src/tools/BashTool/BashTool.tsx`
- `/Users/stello/.hermes/hermes-agent/cli-config.yaml.example`
- `/Users/stello/.hermes/hermes-agent/website/docs/user-guide/skills/bundled/software-development/software-development-requesting-code-review.md`

## Reference findings

Claude Code puts command discipline directly in Bash guidance:

- use dedicated file/search/edit tools before shell
- use background execution for long-running commands
- avoid sleep/poll loops when background completion can notify or be monitored
- make timeout explicit

Claude Code also keeps foreground/background handling close to the Bash tool execution path, including progress, timeout, explicit backgrounding, and auto-background behavior.

Hermes makes the same operating boundary explicit through terminal timeout, background process notification, and bounded auto-fix guidance.

## Chosen Elnath-native design

Do not copy prompts or code.

Add short Elnath-native guidance to existing tool descriptions:

- `bash`: prefer dedicated tools, use `process_start` for long/background commands, prefer focused verification before broad verification, treat broad failures as diagnostic evidence rather than unrelated edit permission, and stop on scope-lock drift.
- `process_start`: explicitly frames long-running/background execution and `process_monitor` follow-up.
- `process_monitor`: ties monitoring to `process_start`.

This is a small guidance-layer milestone, not a new runtime behavior milestone.

## Changed files

- `/Users/stello/elnath/internal/tools/bash.go`
- `/Users/stello/elnath/internal/tools/process_tools.go`
- `/Users/stello/elnath/internal/tools/schema_test.go`
- `/Users/stello/elnath/.omc/research/tool-guidance-policy-milestone-e-2026-05-14.md`

## Implemented behavior

- `bash` now tells the model to use `process_start` for long-running/background commands and `process_monitor` for progress.
- `bash` now tells the model to prefer focused verification before broad verification.
- `bash` now states broad verification failure is diagnostic evidence, not permission to edit unrelated files.
- `bash` now states scope lock must be obeyed and recovery must stop if it would edit outside allowed scope.
- `process_start` now explicitly describes long-running/background use and `process_monitor` follow-up.
- `process_monitor` now explicitly describes monitoring a process started by `process_start`.
- `TestBuiltinToolDescriptions` now locks this guidance in regression coverage.

## Verification

- `go test ./internal/tools -run TestBuiltinToolDescriptions -count=1` -> PASS (`ok github.com/stello/elnath/internal/tools 0.765s`)
- `go test ./internal/tools -count=1` -> PASS (`ok github.com/stello/elnath/internal/tools 55.828s`)
- `git diff --check` -> PASS

## Benchmark

Not run.

## Corpus / baseline mutation

No corpus or baseline mutation.

## Unrelated dirty files excluded

Known unrelated dirty files remain excluded from this milestone:

- `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`
- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_benchmark_wrapper_v8_task_guidance.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`
- `.claude/`
- `docs/superpowers/plans/2026-04-30-elnath-local-managed-runtime.md`

## Remaining risk

Model-facing guidance reduces wrong tool choice but does not by itself enforce behavior. Enforcement remains covered by previous supervisor/receipt milestones and future callable control-surface work.

## Next autonomous action

Run focused tool package tests, update this artifact with exact results, then commit Milestone E as one coherent local milestone.
