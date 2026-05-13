# post-PR200 expanded selected current-only smoke after self-correction

Date: 2026-05-13 / completed 2026-05-14 KST
Branch: codex/v8-self-correction-regression-recovery
Commit at run start: 602b3c2618a99cb9e24d50c85a38c6970fde78af

## Scope

This was a selected 10-task current-only smoke after the post-PR200 self-correction and task-specific recovery changes.

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
  .omc/research/post-pr200-expanded-10task-after-self-correction-20260513/current-plan.json
```

Result directory:

```text
benchmarks/results/post-pr200-expanded-10task-after-self-correction-20260513
```

Scorecard:

```text
benchmarks/results/post-pr200-expanded-10task-after-self-correction-20260513/current-scorecard.json
```

## Aggregate

- Total: 10
- Success + verified: 9
- Failure family counts:
  - incomplete_patch: 1
- verification_unavailable: 0
- recovery_attempted: 3
- Strong patch quality: 9

Verdict: PASS WITH FINDINGS.

## Per-task result

| Task | Result | Failure family | Recovery | Changed files | Patch quality |
|---|---:|---|---:|---|---|
| V8-GO-BF-004 | PASS |  | false | callbacks/query.go, logger/slog_test.go, tests/query_test.go | strong |
| V8-MIX-BF-001 | FAIL | incomplete_patch | true | api/internal/accumulator/namereferencetransformer.go, go.work.sum |  |
| V8-JS-BUG-001 | PASS |  | false | lib/application.js, test/app.use.js | strong |
| V8-PY-BUG-001 | PASS |  | true | src/requests/sessions.py, tests/test_requests.py | strong |
| V8-GO-BUG-003 | PASS |  | false | command.go, command_test.go | strong |
| V8-GO-BUG-004 | PASS |  | false | backend_inotify.go, backend_inotify_test.go | strong |
| V8-TS-BUG-003 | PASS |  | false | lib/helpers/composeSignals.js, tests/unit/composeSignals.test.js | strong |
| V8-TS-BUG-004 | PASS |  | false | lib/api/api-request.js, test/client-request.js | strong |
| V8-PY-BUG-002 | PASS |  | false | src/click/parser.py, tests/test_parser.py | strong |
| V8-PY-BF-001 | PASS |  | true | src/flask/app.py, tests/test_basic.py | strong |

## Finding: V8-MIX-BF-001

The remaining miss is not a setup contract failure.

Evidence:

- Repo-native verification passed: `cd kyaml && go test ./...`
- Changed production file: `api/internal/accumulator/namereferencetransformer.go`
- Missing focused regression file: `api/internal/accumulator/namereferencetransformer_test.go`
- Guard classified the result as `incomplete_patch`
- Retained root: `/var/folders/m_/fnd4jmdn6fv03bdwkqrwk1fh0000gn/T//elnath-current-benchmark.vOdeLR`
- Debug pointer: `benchmarks/results/post-pr200-expanded-10task-after-self-correction-20260513/current-scorecard.debug/V8-MIX-BF-001-run-1.debug-evidence.json`

Retained logs show both the initial attempt and task-specific recovery identified the correct missing regression seam, but hit `budget_exceeded` before applying the test edit.

Operational root cause:

- `scripts/run_current_benchmark_wrapper.sh` assigns most tasks `ELNATH_MAX_ITERATIONS=20`.
- V8-MIX-BF-001 is a mixed Go/KRM brownfield task that already had production diff and verification, then needed a focused regression.
- The wrapper has task-specific guidance, but the task still needs a larger bounded recovery budget to avoid spending the entire recovery pass on targeted inspection.

## Claim boundary

Allowed:

- Post-PR200 expanded selected current-only smoke produced 9/10 success+verified.
- verification_unavailable was 0.
- V8-GO-BUG-004 passed with production + regression evidence after the self-correction wrapper change.
- Remaining miss is V8-MIX-BF-001 incomplete patch quality, not runner setup failure.

Forbidden:

- v8 benchmark passed.
- full v8 current-only passed.
- baseline completed.
- Elnath beats Codex CLI.
- Elnath beats Claude Code.
- broad public benchmark superiority.

## Next action

Patch the wrapper policy narrowly:

- classify V8-MIX-BF-001 as a long mixed recovery task for `task_recovery_timeout`
- raise only its `task_max_iterations` budget from the generic 20 to a bounded higher value
- rerun one-task V8-MIX-BF-001 current-only with retained logs

If the one-task retry passes, rerun the same 10-task selected smoke before any full v8 current-only planning.
