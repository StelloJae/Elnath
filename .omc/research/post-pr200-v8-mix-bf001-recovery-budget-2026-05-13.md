# post-PR200 V8-MIX-BF-001 recovery budget retry

Date: 2026-05-13 / completed 2026-05-14 KST
Branch: codex/v8-self-correction-regression-recovery
Commit at run start: 602b3c2618a99cb9e24d50c85a38c6970fde78af

## Scope

This was a one-task current-only retry for V8-MIX-BF-001 after a narrow benchmark-wrapper policy patch.

This was not:

- full v8 benchmark
- baseline run
- Codex CLI comparison
- Claude Code comparison
- public superiority evidence

## Patch Under Test

Files:

- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/test_benchmark_wrapper_v8_task_guidance.sh`

Behavior:

- V8-MIX-BF-001 is treated as a long mixed recovery task for `task_recovery_timeout`.
- V8-MIX-BF-001 gets bounded `ELNATH_MAX_ITERATIONS=30` instead of the generic 20.
- Regression coverage checks that v8 task guidance still includes the relevant task-specific routing and budget text.

Reason:

The prior 10-task smoke produced 9/10 success+verified with one remaining `incomplete_patch` on V8-MIX-BF-001. Retained logs showed the model identified the correct missing regression seam but hit `budget_exceeded` before applying the test edit.

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
  .omc/research/post-pr200-v8-mix-bf001-recovery-budget-20260513/current-plan.json
```

Result directory:

```text
benchmarks/results/post-pr200-v8-mix-bf001-recovery-budget-20260513
```

Scorecard:

```text
benchmarks/results/post-pr200-v8-mix-bf001-recovery-budget-20260513/current-scorecard.json
```

Retained root:

```text
/var/folders/m_/fnd4jmdn6fv03bdwkqrwk1fh0000gn/T//elnath-current-benchmark.ihPlpb
```

## Result

- Total: 1
- Success + verified: 1
- Failure family counts: none
- verification_unavailable: 0
- recovery_attempted: 0
- Patch quality: strong
- Duration: 323 seconds

Task result:

| Task | Result | Failure family | Verification | Changed files | Patch quality |
|---|---:|---|---|---|---|
| V8-MIX-BF-001 | PASS |  | `cd kyaml && go test ./...` | api/internal/accumulator/namereferencetransformer.go, api/internal/accumulator/namereferencetransformer_test.go, go.work.sum | strong |

## Local Verification For Wrapper Patch

```text
bash -n scripts/run_current_benchmark_wrapper.sh scripts/run_baseline_benchmark_wrapper.sh
PASS

scripts/test_benchmark_wrapper_v8_task_guidance.sh
PASS

scripts/test_current_benchmark_wrapper_completion_guards.sh
PASS

git diff --check
PASS

./elnath eval validate .omc/research/post-pr200-v8-mix-bf001-recovery-budget-20260513/one-task-corpus.json
PASS
```

## Claim Boundary

Allowed:

- V8-MIX-BF-001 one-task current-only retry passed after bounded recovery budget policy.
- The prior selected 10-task smoke remaining miss was reproduced as a budget/patch-quality issue, not setup failure.
- The wrapper patch has focused local regression coverage.

Forbidden:

- v8 benchmark passed.
- full v8 current-only passed.
- baseline completed.
- Elnath beats Codex CLI.
- Elnath beats Claude Code.
- broad public benchmark superiority.

## Next Action

Rerun the same 10-task selected current-only smoke after the V8-MIX-BF-001 recovery budget patch.

If it reaches 10/10 with `verification_unavailable=0`, recommend full v8 current-only planning. Do not run baseline or comparison yet.
