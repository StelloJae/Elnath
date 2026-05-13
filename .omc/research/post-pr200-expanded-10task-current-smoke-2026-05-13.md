# Post-PR200 expanded 10-task current-only smoke

Date: 2026-05-13
Branch: `main`
HEAD: `6f1b4ab87f79bd1c55a78d8d9240669f5e3cd620`
Goal source: `.omc/research/elnath-final-autonomous-runtime-goal-2026-05-13.md`
Result directory: `benchmarks/results/post-pr200-expanded-10task-current-smoke-20260513`
Scorecard: `benchmarks/results/post-pr200-expanded-10task-current-smoke-20260513/current-scorecard.json`
Debug directory: `benchmarks/results/post-pr200-expanded-10task-current-smoke-20260513/current-scorecard.debug`

## Command

```bash
CURRENT_BIN=/Users/stello/elnath/scripts/run_current_benchmark_wrapper.sh \
ELNATH_BIN=/Users/stello/elnath/elnath \
ELNATH_BENCHMARK_KEEP_TMP=1 \
ELNATH_BENCHMARK_PERMISSION_MODE=bypass \
ELNATH_REASONING_EFFORT_MODE=auto \
ELNATH_REASONING_EFFORT=medium \
ELNATH_TIMEOUT=300 \
ELNATH_VERIFY_TIMEOUT=300 \
/Users/stello/elnath/elnath eval run-current \
  .omc/research/post-pr200-expanded-10task-current-smoke-20260513/current-plan.json
```

## Runtime policy

- current-only
- no baseline
- no full v8 benchmark
- no Codex CLI comparison
- no Claude Code comparison
- `ELNATH_BENCHMARK_KEEP_TMP=1`
- absolute `CURRENT_BIN`
- absolute `ELNATH_BIN`
- permission mode: `bypass`

## Pre-run gates

- `HEAD == origin/main`: PASS
- tracked working tree clean: PASS
- `./elnath eval validate benchmarks/public-corpus-v8-25.v1.json`: PASS
- `bash -n scripts/run_current_benchmark_wrapper.sh scripts/run_baseline_benchmark_wrapper.sh`: PASS

Untracked files present and not part of this lane:

- `.claude/`
- `docs/superpowers/plans/2026-04-30-elnath-local-managed-runtime.md`

## Aggregate

- total tasks: 10
- success + verified: 7
- failures: 3
- `verification_unavailable`: 0
- recovery attempted: 4
- patch quality:
  - `strong`: 7
  - unset due failure: 3

Failure family counts:

- `incomplete_patch`: 1
- `verification_failed`: 1
- `no_change_planning_failure`: 1

Verdict: FAIL for widening purposes.

Reason: selected current-only setup remains healthy, but `7/10` is below the widening threshold and includes one no-change completion failure. This is not a setup-contract failure and not `verification_unavailable`; it is a runtime/control-loop quality finding.

## Per-task table

| task id | result | failure family | verification command | changed files | sidecar path | notes |
| --- | --- | --- | --- | --- | --- | --- |
| `V8-GO-BF-004` | PASS |  | `go test ./...` | `callbacks/query.go`, `logger/slog_test.go`, `tests/query_test.go` | `current-scorecard.debug/V8-GO-BF-004-run-1.debug-evidence.json` | verified first attempt; patch quality `strong` |
| `V8-MIX-BF-001` | FAIL | `incomplete_patch` | `cd kyaml && go test ./...` | `api/internal/accumulator/namereferencetransformer.go`, `go.work.sum` | `current-scorecard.debug/V8-MIX-BF-001-run-1.debug-evidence.json` | verification passed after recovery, but patch-quality guard rejected missing focused test/fixture coverage |
| `V8-JS-BUG-001` | PASS |  | `npm test` | `lib/application.js`, `test/app.use.js` | `current-scorecard.debug/V8-JS-BUG-001-run-1.debug-evidence.json` | verified first attempt; patch quality `strong` |
| `V8-PY-BUG-001` | PASS |  | `python3 -m pytest tests/test_requests.py -q` | `src/requests/sessions.py`, `tests/test_requests.py` | `current-scorecard.debug/V8-PY-BUG-001-run-1.debug-evidence.json` | verified first attempt; patch quality `strong` |
| `V8-GO-BUG-003` | FAIL | `verification_failed` | `go test ./...` | `command.go`, `command_test.go` | `current-scorecard.debug/V8-GO-BUG-003-run-1.debug-evidence.json` | retry attempted; final `go test ./...` still failed |
| `V8-GO-BUG-004` | FAIL | `no_change_planning_failure` | `go test ./...` |  | `current-scorecard.debug/V8-GO-BUG-004-run-1.debug-evidence.json` | edit intent detected, but final working-tree diff empty |
| `V8-TS-BUG-003` | PASS |  | `npm exec -- vitest run --project unit tests/unit/composeSignals.test.js` | `lib/helpers/composeSignals.js`, `tests/unit/composeSignals.test.js` | `current-scorecard.debug/V8-TS-BUG-003-run-1.debug-evidence.json` | verified first attempt; patch quality `strong` |
| `V8-TS-BUG-004` | PASS |  | `node --test test/client-request.js` | `lib/api/api-request.js`, `test/client-request.js` | `current-scorecard.debug/V8-TS-BUG-004-run-1.debug-evidence.json` | verified first attempt; patch quality `strong` |
| `V8-PY-BUG-002` | PASS |  | `python3 -m pytest tests/test_parser.py -q` | `src/click/parser.py`, `tests/test_parser.py` | `current-scorecard.debug/V8-PY-BUG-002-run-1.debug-evidence.json` | verified after one recovery attempt; patch quality `strong` |
| `V8-PY-BF-001` | PASS |  | `python3 -m pytest tests/test_basic.py -q` | `src/flask/app.py`, `tests/test_basic.py` | `current-scorecard.debug/V8-PY-BF-001-run-1.debug-evidence.json` | verified first attempt; patch quality `strong` |

## Failure inspection

### V8-MIX-BF-001

Retained root:

`/var/folders/m_/fnd4jmdn6fv03bdwkqrwk1fh0000gn/T//elnath-current-benchmark.RBLCjn`

Observed:

- first attempt ended with provider `INTERNAL_ERROR`
- recovery produced `api/internal/accumulator/namereferencetransformer.go` and `go.work.sum`
- verification and retry verification both passed
- scorecard rejected result as `incomplete_patch` because the task requires behavior diff plus focused test/fixture coverage

Interpretation:

This is not a setup failure. It is a patch-quality/recovery-quality failure under the stricter gate.

### V8-GO-BUG-003

Retained root:

`/var/folders/m_/fnd4jmdn6fv03bdwkqrwk1fh0000gn/T//elnath-current-benchmark.c5XeMn`

Observed:

- first attempt made `command.go` and `command_test.go` changes
- `go test ./...` failed
- recovery attempted more edits
- final retry verification still failed in `github.com/spf13/cobra`
- visible failure output remained around existing flag-group behavior and usage output

Interpretation:

This is a task-quality failure. The agent made a plausible targeted patch but did not converge before budget/attempt boundary.

### V8-GO-BUG-004

Retained root:

`/var/folders/m_/fnd4jmdn6fv03bdwkqrwk1fh0000gn/T//elnath-current-benchmark.qRIBnE`

Observed:

- first attempt inspected inotify code/tests
- provider timeout warning appeared while reading body
- recovery inspected the same area
- final `diff.patch` length: 0 lines
- final working-tree status empty

Interpretation:

This is the most important control-loop finding from this lane. Elnath had edit intent and enough local context discovery, but bounded retry still exited without a diff. This validates a no-change/timeout self-correction repair before any full v8 run.

## Claim boundary

Allowed:

- post-PR200 expanded selected current-only smoke completed
- result was `7/10 success+verified`
- `verification_unavailable=0`
- setup contract remained healthy in this selected lane
- patch-quality and control-loop failures were exposed and retained

Forbidden:

- v8 benchmark passed
- full v8 current-only passed
- baseline completed
- Elnath beats Claude Code
- Elnath beats Codex
- broad public benchmark superiority

## Next autonomous action

Do not widen to full v8.

Next slice:

1. inspect Elnath completion/self-correction path for edit-intent plus timeout/no-diff cases
2. compare against existing receipt fields and closed-enum retry strategy
3. patch the smallest runtime/control-loop gap that caused `V8-GO-BUG-004` to exit with no diff after edit intent
4. add focused tests
5. run focused tests and `git diff --check`
6. rerun one-task `V8-GO-BUG-004` current-only retained smoke
