# Elnath reference-driven completion goal (2026-05-14)

## Supersession note

For future `$goal` runs, use this newer control document instead:

- `/Users/stello/elnath/.omc/research/elnath-completion-program-control-2026-05-14.md`

Reason: this earlier document names Milestone B as the likely next milestone and can be misread as a one-milestone goal. The newer document defines Elnath completion as a multi-milestone program and explicitly forbids marking the active goal complete after a single milestone.

## Purpose

Use this document as the long-form instruction source for future `$goal` runs.

Goal text should stay short. The active goal should reference this file and follow it as the control document.

## Primary objective

Complete Elnath by fixing real runtime/control-loop gaps, not by wasting time on repeated benchmark symptom loops.

Work adaptively:

1. Discover the next real blocker.
2. Compare Elnath against Claude Code source, Hermes, claw-code analysis, and repo evidence.
3. Identify the structural gap.
4. Implement the smallest durable Elnath-native fix.
5. Verify with focused tests.
6. Write artifact evidence.
7. Continue to the next structural milestone.

## Current known state

- Milestone A scope-drift guard is complete.
- Milestone A commit: `03f153b10fabf24ff6c41350366ccc56827f7482`
- Expected branch may be `codex/supervisor-scope-drift-guard`, but always confirm.
- Do not assume working tree is clean.
- Prior benchmark-wrapper dirty files may exist. Do not mix unrelated dirty changes into new commits unless intentionally adopted into the current milestone.

## Highest-priority documents

Read these first every time this goal resumes:

1. `/Users/stello/elnath/.omc/research/elnath-control-loop-structural-correction-2026-05-14.md`
2. `/Users/stello/elnath/.omc/research/claude-code-vs-elnath-control-loop-diagnosis-2026-05-14.md`
3. `/Users/stello/elnath/.omc/research/supervisor-scope-drift-guard-milestone-a-2026-05-14.md`

This file is the lane-level goal wrapper around those control documents.

## Reference sources

- Elnath repo: `/Users/stello/elnath`
- Claude Code source: `/Users/stello/claude-code-src/src`
- Hermes source: `/Users/stello/.hermes/hermes-agent`
- claw-code / Claude analysis:
  - `/Users/stello/elnath/CLAW_CODE_ANALYSIS.md`
  - `/Users/stello/elnath/ADR-001-v01-architecture.md`
  - `/Users/stello/elnath/docs/roadmap.md`
- Elnath evidence:
  - `/Users/stello/elnath/.omc/research`
  - `/Users/stello/elnath/benchmarks/results` only when needed for diagnosis, not as the main loop

## Strategic rule

Do not execute a fixed checklist blindly.

At each milestone:

1. Inspect current Elnath code.
2. Inspect Claude Code source for the matching subsystem.
3. Inspect Hermes/claw-code references when relevant.
4. Identify what Elnath structurally lacks.
5. Write or update a short `.omc/research/...md` evidence/design note.
6. Add focused tests.
7. Implement the smallest durable fix.
8. Verify.
9. Update artifact.
10. Decide next milestone from evidence.

Benchmark is a verification tool. It is not the roadmap.

## Hard prohibitions

- Do not run full v8.
- Do not run baseline.
- Do not run Codex CLI comparison.
- Do not run Claude Code comparison.
- Do not claim benchmark superiority.
- Do not keep rerunning benchmarks to discover runtime bugs.
- Do not create PRs for tiny fragments.
- Do not merge unrelated dirty files.
- Do not copy proprietary source, prompts, or error text verbatim. Reimplement flow in Go/Elnath style.

## Allowed actions

- Read all local reference source.
- Modify Elnath production code.
- Modify tests.
- Modify docs/artifacts.
- Commit coherent milestones.
- Open one PR per coherent milestone after local verification.
- Use subagents only for bounded code mapping, reference comparison, or review. Main agent owns implementation.

## Adaptive milestone priorities

Choose the next milestone based on evidence. Current likely next milestone is Milestone B.

### Milestone B: verification ownership classifier

Goal: prevent broad unrelated verification failures from becoming model edit permission.

Implement:

- classify verification commands/results as focused, broad, harness-owned, diagnostic, or unknown
- distinguish target verification failure from unrelated broad failure
- record classification in receipt/outcome/gate context
- ensure retry only auto-edits target/focused failures
- classify broad unrelated failure and stop instead of editing unrelated files
- wire classifier into scope-drift guard where possible

Reference compare:

- Claude Code query/tool loop and BashTool handling
- Elnath `runtime_completion_observability.go`
- Elnath `runtime_completion_retry.go`
- Elnath agent/tool executor
- Hermes long-running/verification patterns when applicable

### Milestone C: shell mutation and diff supervisor

Goal: scope guard should not only detect `edit_file` and `write_file`. Shell/apply_patch/gofmt/sed mutations need diff-based scope classification.

Implement:

- safe changed-file collection around correction attempts where possible
- classify shell mutation scope drift
- avoid expensive scans unless needed
- keep receipt-backed evidence

### Milestone D: command execution policy parity

Goal: make command execution more Claude Code-like: timeout/background/foreground/abort policy explicit.

Implement:

- command class metadata
- long-running command guidance
- `process_start` / `process_monitor` preference
- timeout receipt
- sibling cancellation policy review

### Milestone E: prompt/tool guidance parity

Goal: model-facing tool docs should steer behavior like Claude Code without copying text.

Implement:

- richer Elnath-native guidance for read/search/edit/bash/process tools
- clear "broad verification failure is not edit permission"
- clear "focused verification first"
- clear "scope lock must be obeyed"

### Milestone F: provider/reasoning effort robustness

Goal: any OpenAI Responses-compatible provider can use effort levels when supported. Auto effort should remain simple but observable.

Implement only if current code evidence shows gaps.

## Required first action on resume

1. `git status --short --branch`
2. Confirm HEAD/branch and dirty tracked files.
3. Read this file and the three highest-priority artifacts.
4. Inspect current relevant Elnath code before editing.
5. Inspect matching Claude Code source before designing.
6. Write/update a short `.omc/research/...md` milestone note stating:
   - actual problem
   - reference pattern
   - chosen Elnath-native fix
   - files planned
   - verification plan

## Implementation discipline

- Prefer tests before production changes.
- Keep each milestone coherent, not tiny.
- Avoid broad refactor unless necessary.
- Do not patch benchmark-wrapper symptoms unless it directly wires a structural guard.
- Preserve user/unrelated dirty changes.
- Use `apply_patch` for manual edits.
- Run focused tests first.
- Run broader tests only proportional to changed packages.
- Always run `git diff --check`.

## Verification expectations

Minimum for code milestone:

- focused tests for new behavior
- affected package tests
- `git diff --check`

Use broader checks when touched surface requires:

- `go test ./cmd/elnath ./internal/orchestrator ./internal/agentic/completion ./internal/learning -count=1`
- `go test ./internal/... -count=1` only if blast radius warrants
- `go vet ./...` only near PR-ready if feasible

## Benchmark policy

Benchmark is not the driver.

Only after a structural guard is implemented and locally verified:

- one retained one-task current-only check may run if it directly validates the new guard
- no selected smoke unless needed and justified in artifact
- no full v8/baseline/comparison until explicit later lane

## Artifact requirements

After each milestone write/update `.omc/research/...md` with:

- branch
- commit hash if committed
- changed files
- problem found
- reference code inspected
- implemented behavior
- exact verification commands/results
- benchmark run: yes/no
- corpus/baseline mutation: yes/no
- remaining risk
- next autonomous action

## PR policy

- Batch local changes into one coherent PR per milestone.
- Do not open PR before local tests pass.
- Do not merge until CI/review gates pass or artifact clearly proves unrelated failure.
- Keep prior unrelated dirty files out of commit.

## Success condition

This lane is complete only when Elnath has a reference-backed supervisor/control-loop upgrade across:

- scope drift guard
- verification ownership
- shell/diff mutation scope detection
- command execution policy
- retry/receipt-backed claim boundary
- documentation/artifact trail

Completion is not benchmark score.

Completion is runtime becoming structurally capable enough that benchmark/readiness can proceed without wasting time on repeated uncontrolled recovery loops.

## Reporting

Report in Korean, `caveman hangul-full`.

Keep reports short and human-readable.

For substantial updates:

- 요약
- 변경 사항
- 검증
- 영향 / 리스크
- 다음 단계

Every substantial report must include:

- branch
- PR URL if any
- commit hash if committed
- artifact path
- exact verification results
- remaining risk
- next autonomous action already chosen
