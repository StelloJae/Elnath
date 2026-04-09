# Canary targeted repair review

- Date: 2026-04-09
- Scope: targeted repair review + documentation handoff for `TS-BF-001` and `GO-BF-002`
- Runtime policy reference: `sandbox=workspace-write, approvals=bypass (benchmark wrapper default via ELNATH_BENCHMARK_PERMISSION_MODE); cli=--non-interactive`

## Reviewed changes and artifacts

- `scripts/run_current_benchmark_wrapper.sh`
- `scripts/run_baseline_benchmark_wrapper.sh`
- `scripts/test_benchmark_wrapper_targeted_verification.sh`
- `benchmarks/results/canary-targeted-repair/ts-bf-001.current-scorecard.json`
- `benchmarks/results/go-bf-002-targeted-rerun-20260409/`

## Review findings

### Blocking issues

- None remain after fixing the missing executable bit on `scripts/test_benchmark_wrapper_targeted_verification.sh`.

### Quality notes

- The current and baseline benchmark wrappers now use the same narrow Vitest targeted verification path for retry-telemetry tasks, which keeps current-vs-baseline verification behavior aligned.
- `scripts/test_benchmark_wrapper_targeted_verification.sh` covers that targeted-command selection directly, reducing the risk of drifting back to a broader test invocation; this review also restored its executable bit so the check runs directly from the repo.
- The `TS-BF-001` artifact still records `failure_family: "no_changes"`, so that lane remains unresolved and should not be described as repaired yet.
- The `GO-BF-002` lane now has a fresh passing rerun artifact under the same runtime policy, so it is safe to treat that task as cleared for the canary recapture handoff.

## Durable handoff status

- `TS-BF-001`: still unresolved in the checked-in targeted artifact (`no_changes` / no working-tree diff).
- `GO-BF-002`: fresh targeted rerun passed; see `../go-bf-002-targeted-rerun-20260409/summary.md` and `../go-bf-002-targeted-rerun-20260409/result.json`.
- Canary-only recapture: still pending follow-up after the targeted repair evidence is integrated.

## Recommendation

Use the checked-in targeted artifacts as the source of truth for the next canary-only recapture:

1. Keep the runtime-policy disclosure verbatim in any recapture summary.
2. Treat `GO-BF-002` as repaired based on the fresh rerun evidence.
3. Treat `TS-BF-001` as still requiring a real code-producing fix unless newer evidence supersedes `ts-bf-001.current-scorecard.json`.
