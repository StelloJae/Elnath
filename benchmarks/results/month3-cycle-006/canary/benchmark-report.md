# Benchmark Cycle Report

- Current: elnath-current
- Baseline: baseline-runner

## Runtime Policy

- Current: sandbox=workspace-write, approvals=bypass (benchmark wrapper default via ELNATH_BENCHMARK_PERMISSION_MODE); cli=--non-interactive
- Baseline: _unspecified_

## Overall Delta

- Success rate delta: 0.50
- Verification pass delta: 0.50
- Recovery success delta: 0.33

## Track Deltas

- brownfield_feature: success 0.50, verification 0.50, recovery 0.33

## Repo Class Summary

- cli_dev_tool: current 0.00 (0/1), baseline 0.00 (0/1)
- service_backend: current 0.67 (2/3), baseline 0.00 (0/3)

## Failure Families (Current)

- verification_failed: 2
