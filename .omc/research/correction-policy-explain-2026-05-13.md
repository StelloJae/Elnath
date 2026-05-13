# Correction Policy Explain Surface

Date: 2026-05-13
Branch: codex/correction-policy-explain
Lane: ccunpacked reference-parity control surface
Milestone estimate after local verification: 68%

## Objective

Make Elnath's bounded self-correction policy more inspectable without expanding automatic repair behavior.

## Finding

The runtime now supports a bounded completion correction budget up to `completion_retry_max=2`, with closed retry decisions:

- `retry_smaller_scope`
- `run_verification`

However, `elnath explain timeouts` only showed the configured retry max. The output did not disclose the supported maximum or closed decision set. README also still said only `0` or `1` were supported, while current config validation allows `0`, `1`, or `2`.

## Change

- `elnath explain timeouts --json` now includes:
  - `completion_retry_supported_max`
  - `completion_retry_decisions`
- Human-readable `elnath explain timeouts` now prints:
  - `supported_max=<n>`
  - `decisions=retry_smaller_scope,run_verification`
- README self-healing config example now documents `completion_retry_max` support for `0`, `1`, or `2`.

## Evidence

Red checks:

- `go test ./cmd/elnath -run TestCmdExplainTimeoutsJSON -count=1` failed because `completion_retry_supported_max` was absent.
- The text-output coverage was missing before this slice.

Green checks:

- `go test ./cmd/elnath -run 'TestCmdExplainTimeouts(JSON|TextShowsCorrectionPolicy)|TestExecutionRuntimeRunTaskSelfHealingCorrectionUsesSecondBoundedRetry|TestCompletionRetryPreservesPriorAttemptWhenVerificationSkipFollowsCorrection' -count=1` PASS
- `go test ./cmd/elnath ./internal/config -count=1` PASS
- `git diff --check` PASS

## Claim Boundary

Allowed:

- Self-correction retry policy is more inspectable in explain output.
- README now matches current config validation and retry executor support.

Not claimed:

- No new retry decision.
- No unbounded self-healing.
- No provider behavior change.
- No benchmark behavior change.
- No v8 benchmark, baseline, or comparison evidence.

## Next Action

Run `go vet ./...`, then decide whether this small slice should be batched with another nearby self-correction policy polish before opening a PR.
