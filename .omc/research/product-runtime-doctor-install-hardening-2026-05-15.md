# Product/runtime Milestone 6: doctor/install hardening

Date: 2026-05-15
Branch: `codex/product-runtime-doctor-hardening`
Base HEAD: `8b55963`
Status: locally verified implementation slice

## Purpose

Make setup and optional-integration failures visible through `elnath doctor`
without logging secrets. This continues the product/runtime completion lane and
does not use benchmark execution as the roadmap.

## Reference files inspected

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`
- `/Users/stello/.hermes/hermes-agent/tests/test_project_metadata.py`
- `/Users/stello/claw-code/rust/crates/commands/src/lib.rs`
- `/Users/stello/claw-code/rust/crates/runtime/src/config.rs`
- `/Users/stello/claude-code-src/src/upstreamproxy/upstreamproxy.ts`
- `/Users/stello/elnath/cmd/elnath/cmd_doctor.go`
- `/Users/stello/elnath/cmd/elnath/cmd_doctor_test.go`
- `/Users/stello/elnath/internal/config/config.go`

## Behavior added

- `doctorCheck` now supports `remediation` entries in JSON and text output.
- Added `config_file_permissions` doctor check:
  - passes when config contains no configured secrets;
  - passes when a secret-bearing config file is owner-only;
  - warns when secret-bearing config is readable by group/other;
  - suggests `chmod 600` and private config/env usage;
  - does not print API key/token values.
- Added `telegram_integration` doctor check:
  - passes when Telegram is disabled;
  - passes when enabled and required fields are configured;
  - leaves missing required fields to config validation, which already fails
    closed before runtime starts.

## Verification

TDD red check:

- `go test ./cmd/elnath -run 'TestCmdDoctorWarnsOnWorldReadableSecretConfig|TestCmdDoctorPassesPrivateSecretConfig|TestCmdDoctorWarnsWhenEnabledTelegramIncomplete' -count=1`
  - initially failed because `doctorCheck` had no `remediation` field and the
    new doctor checks did not exist.

Passing checks:

- `go test ./cmd/elnath -run 'TestCmdDoctorWarnsOnWorldReadableSecretConfig|TestCmdDoctorPassesPrivateSecretConfig|TestCmdDoctorReportsEnabledTelegramConfigured|TestCmdDoctorJSONReportsLocalReadiness' -count=1`
  - PASS
- `go test ./cmd/elnath -count=1`
  - PASS
- `go vet ./...`
  - PASS
- `git diff --check`
  - PASS

## Benchmark boundary

- Full v8 benchmark: not run.
- Baseline: not run.
- Codex comparison: not run.
- Claude Code comparison: not run.
- Benchmark corpus mutation: none.
- Baseline mutation: none.
- Benchmark superiority claim: none.

## Remaining risks

- This slice does not implement an installer/updater command. It hardens the
  doctor/readiness path first.
- Live Telegram API probing is not performed; this check verifies local config
  shape and secret hygiene only.
- Additional optional backends can be added to doctor as their product boundary
  becomes explicit.

## Next recommended milestone

Milestone 7: bounded self-correction to product-grade.

Recommended next slice:

- use diagnostic deltas and completion-guard receipts to allow narrow automatic
  correction attempts;
- enforce max attempts and explicit stop reasons;
- keep broad unrelated failures from widening edit scope.
