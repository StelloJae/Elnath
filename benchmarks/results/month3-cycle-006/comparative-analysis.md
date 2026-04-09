# Month 3 comparative analysis after benchmark permission fix

## Scope
This memo compares the relevant Month 3 evidence sets:
- `benchmarks/results/month3-cycle-003`
- `benchmarks/results/month3-cycle-004`
- `benchmarks/results/month3-cycle-005`
- `benchmarks/results/permission-rerun-001`
- `benchmarks/results/month3-cycle-006`

## Key conclusion
The benchmark permission fix restored the **real bugfix superiority signal** and **partially restored** the carry-forward canary signal, but it did **not fully restore** the strongest Month 3 cross-slice superiority evidence.

The reason is not an unresolved harness hang anymore: `month3-cycle-006` completed end-to-end. The limiting factor is that two current canary tasks still finished as `verification_failed` in the full post-fix cycle.

## Why cycle-004 and cycle-005 were not trustworthy
`month3-cycle-004` and `month3-cycle-005` showed all-zero current rates on both the bugfix slice and the canary, but those cycles lacked explicit `runtime_policy` disclosure and current collapsed to `no_changes` instead of landing real patches:

- `month3-cycle-004` bugfix current failure families: `no_changes=2`
- `month3-cycle-004` canary current failure families: `no_changes=4`
- `month3-cycle-005` bugfix current failure families: `no_changes=2`
- `month3-cycle-005` canary current failure families: effectively the same collapse pattern, with current still at 0/4 success and baseline also at 0/4

Those runs were therefore evidence of harness/policy distortion, not evidence that current genuinely lost all Month 3 capability.

## What permission-rerun-001 proved
`permission-rerun-001` was a focused post-fix rerun under explicit current runtime policy:

- Current runtime policy: `sandbox=workspace-write, approvals=bypass (benchmark wrapper default via ELNATH_BENCHMARK_PERMISSION_MODE); cli=--non-interactive`
- `GO-BUG-001` flipped from cycle-005 `no_changes` failure to success + verification pass
- `GO-BF-001` flipped from cycle-005 `no_changes` failure to success + verification pass

This proved the permission fix mattered, but it was only subset evidence, not a full Month 3 recapture.

## What cycle-006 established
`month3-cycle-006` is the first full post-fix recapture with explicit fairness disclosure in the checked artifacts.

### Bugfix slice
- Current: `2/2` success, `2/2` verification pass
- Baseline: `0/2` success, `0/2` verification pass
- Delta from `summary.md`: success `+1.00`, verification `+1.00`, recovery `+1.00`
- Current failure families: `none`
- Baseline failure families: `execution_timeout=2`

Interpretation: the permission fix restored the bugfix slice strongly enough to say the prior month3-cycle-004/005 collapse was not the true bugfix capability signal.

### Carry-forward canary
- Current: `2/4` success, `2/4` verification pass
- Baseline: `0/4` success, `0/4` verification pass
- Delta from `summary.md`: success `+0.50`, verification `+0.50`, recovery `+0.25`
- Current failure families: `verification_failed=2`
- Baseline failure families: `execution_timeout=4`

Current per-task outcomes in `month3-cycle-006/canary/current-scorecard.json`:
- `GO-BF-001`: success, verification passed
- `TS-BF-001`: `verification_failed`
- `GO-BF-002`: `verification_failed`
- `TS-BF-002`: success, verification passed

Interpretation: the canary was restored enough to beat baseline again, but not enough to match the strongest earlier Month 3 evidence.

## Comparison against the strongest earlier signal
The best pre-collapse full Month 3 signal remains `month3-cycle-003`:

- Cycle 003 bugfix delta: success `+0.75`, verification `+0.75`
- Cycle 003 canary delta: success `+0.75`, verification `+0.75`

Compared with cycle 003, cycle 006 is:
- **stronger on the bugfix wedge** (`+1.00` vs `+0.75`)
- **weaker on the canary** (`+0.50` vs `+0.75`)

So the permission fix restored the true signal enough to invalidate the all-zero cycle-004/005 conclusion, but the strongest overall Month 3 superiority story is only **partially** restored because the canary remains degraded versus cycle 003.

## Final answer
- **Did the permission fix restore the true Month 3 signal?**
  - **Yes for bugfix.** The full recapture now shows a clean 2/2 current bugfix win over a 0/2 baseline under explicit runtime-policy disclosure.
  - **Partially for canary.** The full recapture restored a real current-over-baseline canary lead (2/4 vs 0/4), but not a fully healthy carry-forward canary.

- **Is restoration only partial because GO-BF-002 remains blocked?**
  - **No in the literal “stuck” sense.** `month3-cycle-006` completed; `GO-BF-002` is no longer a live blocker.
  - **Yes in the evidence sense.** The restoration is partial because `GO-BF-002` and `TS-BF-001` both ended as `verification_failed` in the completed full recapture, leaving the canary at 50% instead of the 75% seen in cycle 003.

## Exact next follow-up
Do **not** overstate cycle 006 as a full restoration of the strongest Month 3 evidence. The next exact step should be:

1. Run a **targeted canary repair lane** for the two failed current canary tasks under the same explicit runtime policy used in cycle 006:
   - `TS-BF-001`
   - `GO-BF-002`
2. For `TS-BF-001`, keep the worker/runtime retry-telemetry scope but ensure verification is limited to the intended narrow worker-only regression path, not unrelated broad CLI/watch coverage.
3. For `GO-BF-002`, fix the shutdown-progress accounting/test mismatch first, then rerun with the same `go test ./...` standard so the task can clear without relying on an ambiguous partial pass.
4. After those two targeted repairs, run **one canary-only recapture** before making any stronger roadmap claim than “bugfix restored, canary partially restored.”

## Bottom line
The permission fix corrected a false-negative harness condition and restored the real bugfix lead. But the complete post-fix evidence still supports only a **partial** restoration of Month 3’s broader superiority signal, because the carry-forward canary remains mixed rather than decisively healthy.
