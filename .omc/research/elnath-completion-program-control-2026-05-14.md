# Elnath completion program control document (2026-05-14)

## Purpose

This is the standing control document for goal-driven Elnath completion work.

The `$goal` prompt should stay short and point here. This document carries the long rules, priorities, stop conditions, and completion definition.

## Non-negotiable interpretation

This is a completion program, not a one-milestone goal.

Do not mark the active goal complete when one milestone finishes.

Milestone completion means:

- artifact written
- focused tests run
- coherent commit or PR-ready state reached
- next structural blocker chosen

Goal completion means the whole Elnath completion program reaches the success condition in this document and a final closeout artifact exists.

## Primary objective

Complete Elnath by fixing real supervisor, runtime, control-loop, tool, provider, and self-correction gaps.

Do not burn time on repeated benchmark symptom loops.

Every implementation milestone must be reference-driven:

1. inspect current Elnath code
2. inspect matching Claude Code source
3. inspect Hermes and claw-code references when relevant
4. identify the structural gap
5. design the smallest durable Elnath-native fix
6. add focused tests
7. implement
8. verify
9. write/update `.omc/research` evidence
10. commit or prepare one coherent PR
11. continue to the next structural blocker

## Current baseline

Known completed structural slices:

- Milestone A: supervisor scope-drift guard
- Milestone B: verification ownership classifier
- Milestone C: shell mutation and diff supervisor
- Milestone D: command execution policy parity
- Milestone E: tool and prompt guidance parity
- Milestone F: provider, model, and reasoning effort robustness
- Milestone G: callable control-surface reporting and receipts
- Follow-up: shell/process command intent receipts
- Follow-up: process timeout receipts and timeout policy explanation
- Follow-up: bounded `process_wait`
- Follow-up: bounded `user_question_wait`
- Follow-up: refreshed control-surface gap wording after wait tools
- Follow-up: final-control pointer and post-PR213 continuity correction
- Follow-up: gitignored-file filtering for `code_symbols workspace_symbols`
- Follow-up: user-answer character bounds in receipts
- Follow-up: `ask_user_question` answer handoff commands and follow-up hint
- Follow-up: bounded `process_wait watch_text` marker waits
- Follow-up: registry-backed top-level CLI help and control-surface status refresh
- Follow-up: command-specific `--help` dispatch repair
- Follow-up: subcommand help coverage guard
- Follow-up: code-intelligence definitions, references, hover, and diagnostics
- Follow-up: todo active-form guard and active-work-state discipline
- Follow-up: runtime `/status` registry and control-surface coverage
- Local follow-up: pending user-question answer commands in pending-question views

Do not redo these unless new evidence shows a regression.

Recent merged PRs include #207 through #224. Always confirm branch, HEAD, origin/main, and dirty files first before trusting this note.

Expected implementation work should start from a clean branch or clean worktree. Preserve unrelated dirty files in `/Users/stello/elnath`.

Existing unrelated dirty files may be present. Preserve them and keep them out of commits unless intentionally adopted into the current milestone.

## Highest-priority documents

Read these first on resume:

1. `/Users/stello/elnath/.omc/research/elnath-control-loop-structural-correction-2026-05-14.md`
2. `/Users/stello/elnath/.omc/research/claude-code-vs-elnath-control-loop-diagnosis-2026-05-14.md`
3. `/Users/stello/elnath/.omc/research/supervisor-scope-drift-guard-milestone-a-2026-05-14.md`
4. `/Users/stello/elnath/.omc/research/supervisor-verification-ownership-milestone-b-2026-05-14.md`

Supporting references:

- `/Users/stello/elnath/CLAW_CODE_ANALYSIS.md`
- `/Users/stello/elnath/ADR-001-v01-architecture.md`
- `/Users/stello/elnath/docs/roadmap.md`
- `/Users/stello/elnath/AGENTS.md`
- `/Users/stello/claude-code-src/src`
- `/Users/stello/.hermes/hermes-agent`
- `/Users/stello/elnath/.omc/research`

## Reference boundary

Use Claude Code, Hermes, and claw-code as behavioral and architectural references.

Do not copy proprietary source, prompts, or error strings verbatim.

Reimplement flow in Go using Elnath-native names, file layout, prompt style, errors, policy language, receipts, and tests.

## How to choose the next milestone

Do not follow a stale checklist blindly.

At each loop, choose the highest-leverage structural blocker based on current code evidence.

Prefer blockers that reduce wasted autonomous loops:

- verifier ownership and unrelated failure handling
- shell/apply_patch/diff scope supervision
- command timeout/background/abort policy
- tool guidance and deferred discovery
- bounded retry and receipt-backed final claims
- provider/model/reasoning effort control
- plan/task/worktree/cron callable surfaces
- skill and slash-command compatibility
- LSP/code intelligence hooks where high ROI

## Immediate next likely milestone

The old Milestone C/G checklist is complete. Do not restart it.
The post-PR207-through-PR224 control-surface sequence is also complete unless
fresh code evidence proves a regression.

The next autonomous step should be chosen from fresh code evidence. Current best candidates:

1. final control-boundary refresh after PR #224 and the local pending-answer handoff milestone;
2. tiny current-only control smoke only if needed to validate receipt behavior in benchmark environment;
3. one more runtime-only boundary improvement if fresh code evidence shows a clear gap.

Do not widen into full v8, baseline, Codex comparison, or Claude comparison from this document alone.

## Remaining milestone candidates

These are product boundaries or future structural work, not proof that the earlier milestones failed:

- UI-level answer collection: runtime request/list/wait/answer receipts and pending-list answer commands exist, but desktop/app-level modal UX remains outside the current runtime.
- Streaming line-watch monitor: bounded `process_wait` and literal `watch_text` marker waits exist; richer async streaming notification remains deferred.
- Code intelligence: `code_symbols` supports document/workspace symbols, definitions, references, hover, and Go diagnostics; full multi-language LSP lifecycle remains deferred.
- Registry introspection polish: control-surface manifest, ToolSearch metadata, and runtime `/status` coverage exist; deeper registry diagnostics can still improve.
- Benchmark-readiness validation: use only small current-only control smoke first, not full v8 or comparison lanes.

## Hard prohibitions

- Do not run full v8.
- Do not run baseline.
- Do not run Codex CLI comparison.
- Do not run Claude Code comparison.
- Do not claim benchmark superiority.
- Do not treat benchmark failure as the main roadmap.
- Do not create PRs for tiny fragments.
- Do not mark active goal complete after one milestone.
- Do not merge unrelated dirty files.
- Do not copy proprietary code or prompts verbatim.

## Benchmark policy

Benchmark is a validation tool, not the driver.

Allowed only after a structural milestone is locally verified:

- one retained one-task current-only check if it directly validates the new guard
- small current-only smoke only when justified in artifact

Forbidden until a later explicit benchmark-readiness lane:

- full v8
- baseline
- Codex CLI comparison
- Claude Code comparison
- public superiority claim

## Implementation discipline

- Main agent owns implementation.
- Use subagents only for bounded mapping, research, or review support.
- Prefer focused tests before production code.
- Keep changes small but coherent.
- Use `apply_patch` for manual edits.
- Preserve user/unrelated dirty changes.
- Do not refactor adjacent code unless required.
- Batch local changes before PR.
- Commit coherent milestones.

## Verification expectations

Minimum per code milestone:

- focused tests for new behavior
- affected package tests
- `git diff --check`

Use broader checks when blast radius warrants:

- `go test ./cmd/elnath ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1`
- `go test ./internal/... -count=1`
- `go vet ./...`
- `make test` near PR-ready only when feasible

## Artifact requirements

Each milestone must write or update `.omc/research/...md` with:

- branch
- commit hash if committed
- changed files
- actual problem found
- reference code inspected
- chosen Elnath-native design
- implemented behavior
- exact verification commands and results
- benchmark run yes/no
- corpus/baseline mutation yes/no
- unrelated dirty files excluded
- remaining risk
- next autonomous action

## PR policy

- One PR per coherent milestone or milestone bundle.
- No tiny PR churn.
- Do not open PR before local verification passes.
- Do not merge until CI/review gates pass or artifact proves failures unrelated.
- After merge, sync main and continue to next structural milestone.

## Reporting

Report in Korean.

Use concise human-readable updates.

For substantial updates include:

- 요약
- 변경 사항
- 검증
- 영향 / 리스크
- 다음 단계

Every substantial update should include:

- branch
- PR URL if any
- commit hash if committed
- artifact path
- exact verification results
- remaining risk
- next autonomous action already chosen

## Success condition

The Elnath completion program is complete only when all are true:

- supervisor/control-loop no longer permits uncontrolled recovery loops
- verification ownership and unrelated failure handling are receipt-backed
- shell/diff mutation scope supervision exists
- command execution timeout/background/abort policy is explicit
- retry and final claims are receipt-backed
- provider/model/reasoning effort control is user-configurable and documented
- core Claude Code-like control surfaces have implementation or explicit documented exclusion
- local tests and proportional broader checks pass
- durable final closeout artifact exists
- clean claim boundary exists for future benchmark-readiness work

Until then, keep the goal active and continue to the next structural milestone.
