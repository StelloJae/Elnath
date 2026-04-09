# Month 3 Bugfix Governance

## Primary bugfix slice
- `benchmarks/bugfix-primary.v1.json` is the first Month 3 bugfix evaluation slice.
- Initial scope is intentionally narrow: `GO-BUG-001` + `TS-BUG-001`.
- Goal: prove diagnosis + regression-fix quality before broadening corpus size.

## Holdout bugfix slice
- `benchmarks/bugfix-holdout.v1.json` starts with `GO-BUG-002`.
- Holdout stays out of day-to-day tuning and is used to check local overfitting.

## Carry-forward canary
- `benchmarks/month3-canary-corpus.v1.json` carries the Month 2 4-task smoke set forward.
- This canary must stay runnable through Month 3.
- If current drops below baseline on canary success or verification, Month 3 bugfix claims are blocked until the canary is repaired.

## Month 3 rule of motion
1. Strengthen bugfix success and recovery.
2. Keep the canary green enough to preserve the brownfield lead.
3. Do not trade away canary health for a narrow bugfix win.
