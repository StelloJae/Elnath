# Elnath final autonomous runtime goal

Date: 2026-05-13
Branch: `codex/v8-self-correction-regression-recovery`
Base HEAD: `6f1b4ab87f79bd1c55a78d8d9240669f5e3cd620`
Status: active operating contract

Latest continuation update:

- working branch: `codex/v8-self-correction-regression-recovery`
- base HEAD after PR #200: `6f1b4ab87f79bd1c55a78d8d9240669f5e3cd620`
- local milestone commit: `602b3c2618a99cb9e24d50c85a38c6970fde78af`
- PR #196 shipped patch-quality evidence fields
- PR #197 shipped a fail-closed `V8-MIX-BF-001` patch-quality guard
- PR #198 shipped narrower `V8-MIX-BF-001` recovery guidance
- PR #199 shipped a runtime completion warning for `budget_exceeded` after
  edit intent
- PR #200 shipped a narrower `V8-MIX-BF-001` recovery insertion guard
- post-PR200 4-task control smoke passed `4/4`
- post-PR200 expanded 10-task current-only smoke after mixed recovery budget
  completed `10/10`
- current implementation lane: no-change/timeout self-correction repair after
  `V8-GO-BUG-004` exited with edit intent but no diff
- latest focused result: `V8-GO-BUG-004` one-task current-only PASS after
  special missing-regression recovery path
- latest focused result: `V8-MIX-BF-001` one-task current-only PASS after
  bounded recovery budget policy
- latest selected result: 10-task current-only smoke PASS with
  `10/10 success+verified`, `verification_unavailable=0`, and
  `patch_quality=strong` for all tasks

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
- post-PR200 expanded 10-task smoke after mixed recovery budget:
  `10/10 success+verified`
- post-PR200 `V8-GO-BUG-004` focused rerun: PASS after no-diff and
  missing-regression recovery repairs
- post-PR200 `V8-MIX-BF-001` focused rerun: PASS after bounded recovery budget
  policy
- post-PR200 expanded selected rerun:
  `.omc/research/post-pr200-expanded-10task-after-mix-budget-2026-05-13.md`
- selected smoke result: PASS, 10/10, all patch_quality `strong`
- `verification_unavailable=0`
- no baseline run
- no full v8 run
- no Codex/Claude comparison

Important caveat:

- earlier 4-task smoke exposed patch-quality weakness:
  - `V8-MIX-BF-001` passed with only `go.work.sum` changed
  - `V8-PY-BUG-001` passed with production-only diff despite regression-test
    acceptance criteria
- PR #196 and PR #197 made those weak patches visible/fail-closed where
  appropriate
- post-PR200 expanded smoke exposed two runtime/control-loop weaknesses:
  - `V8-GO-BUG-004` reached edit intent, hit timeout/recovery, and exited
    with an empty diff
  - `V8-MIX-BF-001` produced a valid production diff but hit budget before
    adding required focused regression coverage

This means Elnath can produce executable/verified patches across multiple
languages, but benchmark score alone is not enough. Patch-quality and no-diff
completion evidence must remain first-class gates.

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

- completed after post-PR200 self-correction and mixed recovery budget lane
- latest selected result: `10/10 success+verified`
- `verification_unavailable=0`
- setup contract remained healthy
- focused follow-up for that miss passed after bounded recovery budget policy
- selected-smoke confirmation passed after the V8-MIX-BF-001 focused repair
- current blocker is no longer selected-smoke readiness
- next blocker is making the local milestone reviewable before full v8
  current-only planning

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

Status:

- shipped through PR #199

### Stage 3b: no-change/timeout self-correction guard

Goal:

- detect edit-intent plus no diff after timeout or budget pressure as a stronger
  retry/repair condition
- keep retry bounded and receipt-backed
- avoid silent self-healing promises

Status:

- focused repair implemented after post-PR200 expanded smoke
- primary evidence: `V8-GO-BUG-004` `no_change_planning_failure`
- result artifact:
  `.omc/research/post-pr200-expanded-10task-current-smoke-2026-05-13.md`
- focused closure artifact:
  `.omc/research/post-pr200-v8-go-bug004-special-regression-recovery-2026-05-13.md`
- focused result: `V8-GO-BUG-004` one-task current-only PASS, changed
  `backend_inotify.go` and `backend_inotify_test.go`, patch quality `strong`

Exit criteria:

- focused runtime/self-correction tests pass
- no-diff completion produces an explicit repair signal or narrower retry path
- one-task `V8-GO-BUG-004` current-only retained retry is run and documented

### Stage 3c: mixed-task recovery budget guard

Goal:

- prevent mixed brownfield tasks from exhausting the generic recovery budget
  after identifying the correct missing regression seam
- keep the higher budget task-specific and bounded
- preserve patch-quality fail-closed behavior

Status:

- focused wrapper policy patch implemented
- primary evidence: post-PR200 expanded selected smoke had one remaining miss,
  `V8-MIX-BF-001` `incomplete_patch`
- root cause: generic `ELNATH_MAX_ITERATIONS=20` was too low for this mixed
  task after production diff plus targeted regression insertion
- focused closure artifact:
  `.omc/research/post-pr200-v8-mix-bf001-recovery-budget-2026-05-13.md`
- focused result: `V8-MIX-BF-001` one-task current-only PASS, changed
  `api/internal/accumulator/namereferencetransformer.go`,
  `api/internal/accumulator/namereferencetransformer_test.go`, and
  `go.work.sum`, patch quality `strong`

Exit criteria:

- wrapper syntax and guidance tests pass
- one-task `V8-MIX-BF-001` current-only retry passes
- same 10-task selected current-only smoke is rerun and documented

Status:

- exit criteria met
- selected rerun artifact:
  `.omc/research/post-pr200-expanded-10task-after-mix-budget-2026-05-13.md`

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

Do not run baseline or comparison.

Make the local milestone reviewable before full v8:

Recommended next slice:

1. keep no baseline and no comparison
2. run final local verification for changed runtime/wrapper/doc artifacts
3. amend or create one coherent milestone commit
4. open one PR for the milestone, not multiple tiny PRs
5. after merge, plan full v8 current-only run

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
