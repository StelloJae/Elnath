# Month 3 Evidence Snapshot

## Bugfix primary slice
Source: `benchmarks/results/month3-cycle-002/bugfix-report.md`

- Current vs baseline success delta: **+1.00**
- Current vs baseline verification delta: **+1.00**
- Repo classes:
  - `cli_dev_tool`: current 1/1 vs baseline 0/1
  - `service_backend`: current 1/1 vs baseline 0/1

## Carry-forward canary
Source: `benchmarks/results/month3-cycle-002/canary/benchmark-report.md`

- Current vs baseline success delta: **+0.50**
- Current vs baseline verification delta: **+0.50**
- Current vs baseline recovery delta: **+1.00**
- Canary remains green; no regression versus baseline.

## Holdout probe
Source: `benchmarks/results/month3-holdout-001/benchmark-report.md`

- Current vs baseline success delta: **+1.00**
- Current vs baseline verification delta: **+1.00**
- Current vs baseline recovery delta: **+1.00**
- Scope is thin (single-task holdout) but direction is favorable.

## Interpretation
Month 3 started with a real bugfix wedge, not just a plan. Primary bugfix evidence favors current strongly, the Month 2 carry-forward canary still holds, and the first holdout probe also favors current. Remaining work is to increase repeat count / corpus thickness so the signal becomes harder to dismiss as thin smoke evidence.
