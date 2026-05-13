# Elnath final autonomous runtime goal

Date: 2026-05-13
Branch: `main`
HEAD: `8d61c831fd1e28b200011a38a80dbff500129d46`
Status: active operating contract

Latest continuation update:

- working branch: `codex/budget-edit-intent-completion-warning`
- base HEAD after PR #198: `3aca1cdc7a9ad4144dcd8d5a43794940a4ff2c4f`
- PR #196 shipped patch-quality evidence fields
- PR #197 shipped a fail-closed `V8-MIX-BF-001` patch-quality guard
- PR #198 shipped narrower `V8-MIX-BF-001` recovery guidance
- post-PR198 expanded smoke was intentionally aborted after repeated
  `V8-MIX-BF-001` `incomplete_patch`
- current implementation lane: runtime completion guard for
  `budget_exceeded` after edit intent

## Core clarification

Elnath's final goal is not "raise benchmark score".

Elnath's final goal is:

> Build Elnath into a reliable, claim-safe, Claude-Code/Codex-class autonomous
> coding runtime with durable control surfaces, provider/model flexibility,
> bounded self-correction, verified execution receipts, and reproducible
> evidence.

Benchmarks are evidence gates.
Benchmarks are not the product.

## Why recent benchmark work mattered

Recent v8 current-only smoke work tested whether Elnath's runtime foundation
can produce claim-safe evidence:

- benchmark wrappers execute current Elnath reliably
- verification commands run instead of being skipped
- `verification_unavailable` does not return
- no-op coding attempts are detected
- retained logs and debug sidecars survive
- setup surfaces for JS, TS/Node, Python, and mixed Go work
- claim boundary prevents false "v8 benchmark passed" statements

This is useful only because it validates runtime behavior under realistic
coding pressure.

## Current state

Source of truth:

- `/Users/stello/llm_memory/Claude Valut/wiki/entities/elnath-progress-2026-05-13.md`
- `.omc/research/ccunpacked-reference-parity-closeout-boundary-2026-05-13.md`
- `.omc/research/post-pr195-control-loop-smoke-2026-05-13.md`
- `.omc/research/post-pr195-v8-js-bug-001-retained-retry-2026-05-13.md`
- `.omc/research/post-pr195-control-smoke-4task-2026-05-13.md`

Reference-parity control surface:

- complete for this lane after PR #195
- ToolSearch, skills, commands, task, schedule, plan, worktree, process,
  provider/model, reasoning effort, timeout explain, bounded self-correction
  all exist as core surfaces
- UI answer collection, full LSP, NotebookEdit, PowerShell are explicit
  exclusions/deferred work

Latest benchmark-readiness evidence:

- `V8-JS-BUG-001` retained retry: PASS
- 4-task current-only control smoke: `4/4 success+verified`
- `verification_unavailable=0`
- no baseline run
- no full v8 run
- no Codex/Claude comparison

Important caveat:

- 4-task smoke exposed patch-quality weakness:
  - `V8-MIX-BF-001` passed with only `go.work.sum` changed
  - `V8-PY-BUG-001` passed with production-only diff despite regression-test
    acceptance criteria

This means Elnath can produce executable/verified patches, but benchmark score
alone is not enough. Patch-quality evidence must be tracked.

## Product completion target

Elnath is complete enough for this macro-goal only when these are true:

1. Control surfaces are implemented and dogfooded.
2. Provider/model/reasoning-effort routing works across OpenAI Responses-style
   providers, not only Anthropic defaults.
3. Bounded self-correction catches no-op, incomplete, failed verification, and
   unsupported success claims without promising silent universal repair.
4. Verification receipts and patch-quality receipts are durable.
5. Daemon/task/skill/tool workflows can run unattended within explicit policy.
6. Benchmark readiness proves the runtime under realistic coding pressure.
7. Full v8 current-only evidence exists.
8. Baseline/comparison evidence exists only if current-only evidence is clean
   enough to justify it.
9. Claim boundaries are written and reviewed.
10. Main branch stays clean and reproducible.

## Stage gates

### Stage 1: post-PR195 benchmark-readiness control

Status: mostly complete.

Evidence:

- small current-only smoke completed
- retained `V8-JS-BUG-001` retry passed
- 4-task current-only smoke passed
- `verification_unavailable=0`

Remaining:

- patch-quality gate needs first-class tracking

Exit criteria:

- scorecards/artifacts distinguish executable verification success from weak
  patch quality

### Stage 2: patch-quality evidence gate

Goal:

- do not treat all `success+verified` results as equal
- flag checksum-only, lock-only, docs-only, production-only without test when
  acceptance criteria require regression coverage
- preserve benchmark result but add a claim-safe quality tier

Expected implementation shape:

- inspect `RunResult.changed_files`
- add quality classification or artifact-side classifier first
- prefer narrow eval/scorecard helper before production runtime changes
- tests cover:
  - lock/checksum-only diff
  - production + test diff
  - production-only diff with acceptance requiring tests
  - no changed files with edit intent

Status:

- shipped through PR #196 and PR #197
- `success+verified` can now be separated from weak patch quality
- `V8-MIX-BF-001` production-only plus `go.work.sum` now fails closed as
  `incomplete_patch`

Exit criteria:

- focused tests pass
- artifact documents exact behavior
- larger selected smoke can include patch-quality columns

### Stage 3: larger current-only selected smoke

Goal:

- run 8-10 selected v8 current-only tasks
- no baseline
- no comparison
- include patch-quality review columns

Status:

- started after PR #197 and PR #198
- intentionally aborted after repeated `V8-MIX-BF-001` finding
- latest blocker is not setup or verification availability
- latest blocker is runtime self-correction/completion-contract weakness:
  the agent can state a missing test edit intent, spend budget, and exit
  without the promised concrete diff

Exit criteria:

- `verification_unavailable=0`
- setup failures absent
- failure families understood
- weak patch-quality results documented, not hidden

### Stage 3a: budget/edit-intent completion guard

Goal:

- detect `budget_exceeded` after edit intent as a completion warning
- route it to bounded `retry_smaller_scope`
- preserve stronger existing warnings:
  - `final_response_reports_incomplete`
  - `verification_command_failed`
  - `unsupported_verification_success_claim`
  - `edit_intent_without_mutation`

Exit criteria:

- focused runtime completion tests pass
- `.omc/research` artifact records the post-PR198 root cause and fix
- larger benchmark lanes stay paused until this runtime fix is reviewed

### Stage 4: full v8 current-only run

Allowed only after Stage 3 is clean enough.

Goal:

- run full 25-task v8 current-only benchmark
- no baseline yet
- capture full evidence and patch-quality summary

Exit criteria:

- full current-only scorecard exists
- invalid runs excluded
- failure families and patch-quality findings documented

### Stage 5: baseline and comparison

Allowed only after full current-only evidence is stable enough.

Goal:

- run baseline/comparison under documented protocol
- compare only exact protocol, exact corpus, exact runner state

Exit criteria:

- baseline scorecard exists
- comparison artifact exists
- claim boundary written

### Stage 6: public claim readiness

Goal:

- produce claim-safe external story

Allowed claims only when evidence supports them:

- exact benchmark protocol result
- exact comparison result
- limitations and invalid-run exclusions

Forbidden unless directly proven:

- broad public superiority
- "Elnath beats Claude Code" outside exact benchmark
- "Elnath beats Codex" outside exact benchmark
- hiding failed or weak-quality patches

## Operating rules

- Use `hangul-full` caveman reporting unless user turns it off.
- Do not optimize benchmark score at the expense of runtime truth.
- Do not run full v8 before selected smoke and patch-quality gate.
- Do not run baseline before full current-only evidence.
- Do not run Codex/Claude comparison before baseline protocol is written.
- Do not mutate corpus/baselines without an explicit artifact.
- Keep changes local first.
- Batch PRs by coherent milestone, not every tiny slice.
- Write `.omc/research` artifact after each lane.
- Use exact commands, result directories, and scorecard paths.
- Separate:
  - setup readiness
  - verification success
  - patch quality
  - benchmark score
  - public claim maturity

## Stop conditions

Stop only for:

- missing credential/account/payment
- destructive action outside repo/project
- legal/security action outside repo scope
- repeated same failure with no new evidence path
- contradiction in this goal contract

Do not stop for:

- routine test failure
- benchmark miss
- CI failure
- needing next evidence lane
- needing narrow implementation repair

## Immediate next action

Do not run larger smoke first.

First implement or document a patch-quality evidence gate, because the latest
4-task smoke proved that `success+verified` can still hide weak patch quality.

Recommended first slice:

1. inspect existing eval `RunResult` / scorecard summary code
2. add a narrow patch-quality classifier if local structure supports it
3. add focused tests
4. write closure artifact
5. then run larger selected current-only smoke with patch-quality columns

## Completion definition

This final goal is complete only when Elnath has:

- reference-backed control surfaces
- durable receipt and verification evidence
- bounded self-correction with no-op/incomplete detection
- provider/model/effort configurability
- patch-quality evidence gates
- selected and full v8 current-only evidence
- baseline/comparison evidence if run
- documented limitations and claim boundary
- clean `main`
- durable `.omc/research` closure artifact
