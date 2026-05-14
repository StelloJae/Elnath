# JS Weak Patch Gate - 2026-05-14

## Summary

Milestone: post-PR202 benchmark-readiness validation follow-up.

The post-PR202 2-task current-only smoke passed 2/2 success+verified, but
`V8-JS-BUG-001` produced a weak patch: the production diff changed
`lib/application.js`, verification passed, and no test or fixture file changed.
That is useful benchmark evidence, but it should not be classified as clean
success when the task prompt explicitly requires focused test or regression
evidence.

This patch adds a generic completion guard:

- If the task prompt asks for test/regression/fixture/coverage evidence, and
- the run has a diff, and
- no changed file is a test or fixture path,
- then passed verification is still classified as `incomplete_patch`.

## Branch

`codex/js-weak-patch-gate`

## Changed Files

- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_current_benchmark_wrapper_completion_guards.sh`

## Behavior Added

Added wrapper helpers:

- `task_prompt_requires_test_or_fixture_evidence`
- `generic_missing_test_or_fixture_evidence`

The guard is used in both:

- `write_passed_verification_task_specific_failure`
- `task_specific_completion_failure_reason`

The new focused regression uses a fake Elnath run that modifies only
`lib/application.js` while the task prompt asks for focused regression test
coverage. Before the wrapper change, this case incorrectly returned:

- `success=true`
- `verification_passed=true`
- `failure_family=""`

After the wrapper change, it returns:

- `success=false`
- `verification_passed=true`
- `failure_family="incomplete_patch"`

## Verification

Red check:

```text
bash scripts/test_current_benchmark_wrapper_completion_guards.sh
```

Result before implementation:

```text
FAIL as expected: V8-JS-BUG-001 fake production-only patch returned success=true.
```

Green checks:

```text
bash scripts/test_current_benchmark_wrapper_completion_guards.sh
bash -n scripts/run_current_benchmark_wrapper.sh scripts/test_current_benchmark_wrapper_completion_guards.sh
git diff --check
```

Results:

```text
PASS: current benchmark wrapper completion guards classify no-change/incomplete runs
PASS: shell syntax
PASS: git diff --check
```

## Benchmark Scope

No full v8 benchmark was run.
No baseline was run.
No Codex CLI comparison was run.
No Claude Code comparison was run.
No corpus or baseline artifact was mutated.

## Claim Boundary

Allowed:

- The wrapper now rejects production-only patches when the task prompt requires
  focused test/regression evidence.
- The focused wrapper completion-guard regression passed locally.

Not allowed:

- v8 benchmark passed.
- Elnath beats Claude Code.
- Elnath beats Codex.
- Broad benchmark superiority is proven.

## Remaining Risk

The prompt detector is intentionally conservative and text-pattern based. It is
appropriate as a benchmark wrapper completion guard, but it is not a complete
semantic proof that every task has adequate regression coverage.

## Next Recommendation

Open one coherent PR for this benchmark-wrapper evidence-quality fix after
local status is clean. After merge, rerun a tiny current-only smoke that includes
`V8-JS-BUG-001` and confirm the previous weak-pass shape is no longer treated as
clean success.
