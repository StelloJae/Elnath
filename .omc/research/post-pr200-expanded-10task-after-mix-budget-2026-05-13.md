# post-PR200 expanded selected current-only smoke after mixed recovery budget

Date: 2026-05-13 / completed 2026-05-14 KST
Branch: codex/v8-self-correction-regression-recovery
Base HEAD: 6f1b4ab87f79bd1c55a78d8d9240669f5e3cd620
Local milestone commit before this rerun: 602b3c2618a99cb9e24d50c85a38c6970fde78af

## Scope

This was the same selected 10-task current-only smoke after:

- V8-GO-BUG-004 no-change / missing-regression self-correction repair
- V8-MIX-BF-001 bounded recovery budget policy

This was not:

- full v8 benchmark
- baseline run
- Codex CLI comparison
- Claude Code comparison
- public superiority evidence

## Command

```bash
CURRENT_BIN=/Users/stello/elnath/scripts/run_current_benchmark_wrapper.sh \
ELNATH_BIN=/Users/stello/elnath/elnath \
ELNATH_BENCHMARK_KEEP_TMP=1 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
ELNATH_BENCHMARK_SELF_HEALING_OBSERVE_ONLY=false \
ELNATH_BENCHMARK_SELF_HEALING_COMPLETION_RETRY_MAX=1 \
ELNATH_REASONING_EFFORT_MODE=auto \
ELNATH_REASONING_EFFORT=medium \
ELNATH_TIMEOUT=300 \
ELNATH_VERIFY_TIMEOUT=300 \
/Users/stello/elnath/elnath eval run-current \
  .omc/research/post-pr200-expanded-10task-after-mix-budget-20260513/current-plan.json
```

Result directory:

```text
benchmarks/results/post-pr200-expanded-10task-after-mix-budget-20260513
```

Scorecard:

```text
benchmarks/results/post-pr200-expanded-10task-after-mix-budget-20260513/current-scorecard.json
```

## Aggregate

- Total: 10
- Success + verified: 10
- Failure family counts:
  - none: 10
- verification_unavailable: 0
- recovery_attempted: 1
- Patch quality:
  - strong: 10

Verdict: PASS.

## Per-task result

| Task | Result | Failure family | Recovery | Changed files | Patch quality | Notes |
|---|---:|---|---:|---|---|---|
| V8-GO-BF-004 | PASS |  | false | callbacks/query.go, logger/slog_test.go, tests/query_test.go | strong | verification passed on first attempt |
| V8-MIX-BF-001 | PASS |  | false | api/internal/accumulator/namereferencetransformer.go, api/internal/accumulator/namereferencetransformer_test.go, go.work.sum | strong | verification passed on first attempt |
| V8-JS-BUG-001 | PASS |  | false | lib/application.js, test/app.use.js | strong | verification passed on first attempt |
| V8-PY-BUG-001 | PASS |  | true | src/requests/sessions.py, tests/test_requests.py | strong | verification passed after one recovery attempt |
| V8-GO-BUG-003 | PASS |  | false | command.go, command_test.go | strong | verification passed on first attempt |
| V8-GO-BUG-004 | PASS |  | false | backend_inotify.go, backend_inotify_test.go | strong | verification passed on first attempt |
| V8-TS-BUG-003 | PASS |  | false | lib/helpers/composeSignals.js, tests/unit/composeSignals.test.js | strong | verification passed on first attempt |
| V8-TS-BUG-004 | PASS |  | false | lib/api/api-request.js, test/client-request.js | strong | verification passed on first attempt |
| V8-PY-BUG-002 | PASS |  | false | src/click/parser.py, tests/test_parser.py | strong | verification passed on first attempt |
| V8-PY-BF-001 | PASS |  | false | src/flask/app.py, tests/test_basic.py | strong | verification passed on first attempt |

## Interpretation

This closes the selected-smoke patch-quality gate for the current milestone.

Compared with the previous selected smoke:

- `V8-GO-BUG-004` moved from no-change / missing-regression failure to strong PASS.
- `V8-MIX-BF-001` moved from incomplete patch to strong PASS.
- `V8-GO-BUG-003` remained stable as PASS.
- `verification_unavailable` remained 0.
- Every selected task has production + test/fixture evidence or task-appropriate focused coverage.

This supports planning the next lane: full v8 current-only run.

It does not support baseline, Codex comparison, Claude comparison, or public superiority claims yet.

## Claim Boundary

Allowed:

- Post-PR200 selected 10-task current-only smoke passed 10/10.
- All 10 selected tasks were success+verified.
- All 10 selected tasks had patch_quality `strong`.
- `verification_unavailable=0`.
- The selected-smoke control-loop evidence gate is clean enough to plan full v8 current-only.

Forbidden:

- v8 benchmark passed.
- full v8 current-only passed.
- baseline completed.
- Elnath beats Codex CLI.
- Elnath beats Claude Code.
- broad public benchmark superiority.

## Next Action

Make the local milestone reviewable:

1. run final local verification for touched runtime/wrapper areas
2. amend or create a coherent local milestone commit including the evidence artifacts
3. open one PR for the milestone, not multiple small PRs
4. after merge, plan full v8 current-only run

Do not run baseline or comparisons before full v8 current-only evidence exists.

## Final Local Verification

```text
go test ./cmd/elnath ./internal/config -count=1
PASS

bash -n scripts/run_current_benchmark_wrapper.sh scripts/run_baseline_benchmark_wrapper.sh
PASS

scripts/test_benchmark_wrapper_v8_task_guidance.sh
PASS

scripts/test_current_benchmark_wrapper_completion_guards.sh
PASS

git diff --check
PASS

make build
PASS
```
