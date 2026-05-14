# V8-JS-BUG-001 Regression Evidence Gate - 2026-05-14

## Summary

Follow-up to PR #203.

PR #203 added a generic guard for prompts that explicitly mention focused
test/regression evidence. During post-merge validation planning, a gap appeared:
the actual `V8-JS-BUG-001` benchmark prompt does not carry the regression-test
requirement in the prompt string passed to the wrapper. The requirement exists
in the task-specific guidance and acceptance criteria.

Therefore, a production-only `lib/application.js` patch for `V8-JS-BUG-001`
could still be classified as clean success if verification passed.

## Branch

`codex/v8-js-regression-gate`

## Changed Files

- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`

## Behavior Added

Added a task-specific completion guard for `V8-JS-BUG-001`:

- required production behavior diff: `lib/application.js`
- required regression evidence: a changed test or fixture file
- missing either side is classified as `incomplete_patch` even when verification
  passes

This is intentionally narrow. It does not generalize a benchmark success claim.
It fixes the exact weak-pass shape observed in the post-PR202 smoke.

## Verification

Red check:

```text
bash scripts/test_current_benchmark_wrapper_completion_guards.sh
```

Result before implementation:

```text
FAIL as expected: actual V8-JS-BUG-001 prompt shape with production-only
lib/application.js diff returned success=true.
```

Green checks:

```text
bash scripts/test_current_benchmark_wrapper_completion_guards.sh
bash scripts/test_benchmark_wrapper_v8_task_guidance.sh
bash -n scripts/run_current_benchmark_wrapper.sh scripts/test_current_benchmark_wrapper_completion_guards.sh
git diff --check
```

Results:

```text
PASS: current benchmark wrapper completion guards classify no-change/incomplete runs
PASS: v8 task guidance and corpus prompts stay focused
PASS: shell syntax
PASS: git diff --check
```

## Benchmark Scope

No current-only benchmark was run for this patch.
No full v8 benchmark was run.
No baseline was run.
No Codex CLI comparison was run.
No Claude Code comparison was run.
No corpus or baseline artifact was mutated.

## Claim Boundary

Allowed:

- `V8-JS-BUG-001` production-only patches are no longer accepted as clean
  success when regression evidence is missing.
- Local wrapper regression tests cover both the failing production-only case and
  the passing production-plus-test case.

Not allowed:

- v8 benchmark passed.
- Elnath beats Claude Code.
- Elnath beats Codex.
- Broad benchmark superiority is proven.

## Remaining Risk

This is a task-specific benchmark wrapper quality gate. It improves evidence
classification for `V8-JS-BUG-001`; it does not prove that all benchmark tasks
have complete semantic patch-quality gates.

## Next Recommendation

After merge, run a one-task retained current-only retry for `V8-JS-BUG-001` from
a clean `origin/main` worktree. Accept either:

- success+verified with `lib/application.js` and a test/fixture diff, or
- `incomplete_patch` if the model again produces a production-only weak patch.

Do not widen to full v8, baseline, or comparisons yet.
