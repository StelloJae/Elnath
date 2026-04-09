# Permission rerun summary

Focused reruns after commit `6644b17` using the benchmark wrapper's non-interactive bypass policy.

## Runtime policy

- Current rerun policy: **sandbox=workspace-write, approvals=bypass (benchmark wrapper default via ELNATH_BENCHMARK_PERMISSION_MODE); cli=--non-interactive**
- Before-fix month3-cycle-005 current scorecards omitted this field and collapsed to `no_changes`.

## GO-BUG-001 (bugfix subset)

- Before: `benchmarks/results/month3-cycle-005/bugfix-current-scorecard.json` → success=False failure_family=`no_changes` notes=`task completed without creating a working-tree diff`
- After: `benchmarks/results/permission-rerun-001/bugfix-go-bug-001.current-scorecard.json` → success=True verification_passed=True notes=`verification passed on first attempt`
- Baseline reference: `benchmarks/results/permission-rerun-001/bugfix-go-bug-001.baseline-scorecard.json` → failure_family=`execution_timeout`
- Markdown report: `benchmarks/results/permission-rerun-001/bugfix-go-bug-001.report.md`

## GO-BF-001 (canary subset)

- Before: `benchmarks/results/month3-cycle-005/canary/current-scorecard.json` → success=False failure_family=`no_changes` notes=`task completed without creating a working-tree diff`
- After: `benchmarks/results/permission-rerun-001/canary-go-bf-001.current-scorecard.json` → success=True verification_passed=True recovery_succeeded=True notes=`verification passed after one recovery attempt`
- Baseline reference: `benchmarks/results/permission-rerun-001/canary-go-bf-001.baseline-scorecard.json` → failure_family=`execution_timeout`
- Markdown report: `benchmarks/results/permission-rerun-001/canary-go-bf-001.report.md`

## Notes

- These artifacts are intentionally subset reruns, not a replacement for the full month3 cycle.
- They demonstrate that the wrapper no longer collapses immediately to interactive-approval `no_changes` on the targeted current tasks.
