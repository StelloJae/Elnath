# Product/runtime Milestone 5: provider operator hardening

Date: 2026-05-15
Branch: `codex/product-runtime-provider-hardening`
Base HEAD: `f9eed00`
Status: locally verified implementation slice

## Purpose

Make provider/model/effort selection safer and easier to debug without reading
code. This follows the product/runtime control document: provider health,
effort compatibility, auth-required, timeout-invalid, and no-silent-fallback
behavior should be visible through operator-facing JSON.

This is product/runtime work, not benchmark work.

## Reference files inspected

- `/Users/stello/elnath/.omc/research/elnath-product-runtime-100-control-2026-05-15.md`
- `/Users/stello/claw-code/USAGE.md`
- `/Users/stello/claw-code/rust/crates/rusty-claude-cli/src/main.rs`
- `/Users/stello/claw-code/rust/crates/runtime/src/config.rs`
- `/Users/stello/elnath/cmd/elnath/cmd_provider.go`
- `/Users/stello/elnath/cmd/elnath/runtime_provider.go`
- `/Users/stello/elnath/cmd/elnath/cmd_doctor.go`
- `/Users/stello/elnath/internal/llm/provider.go`
- `/Users/stello/elnath/internal/llm/provider_error.go`
- `/Users/stello/elnath/internal/agent/errorclass/classify.go`

## Behavior added

- `elnath provider status --json` now reports:
  - `ready`;
  - `failure_family`;
  - `issues`;
  - `remediation`;
  - `effort_compatibility`;
  - active provider timeout and switch boundaries.

- `elnath provider status --json` now emits structured JSON even when no
  usable provider can be built, then returns a command error. This keeps the
  CLI exit status honest while still giving operators machine-readable
  debugging evidence.

- `elnath provider check <provider> --json` now reports not-ready requested
  providers in JSON instead of only returning free-text errors. It classifies:
  - `auth_required`;
  - `provider_timeout_invalid`.

- Configured provider candidates now include readiness diagnostics:
  - `ready`;
  - `auth_configured`;
  - `timeout_status`;
  - `failure_family`;
  - `issues`;
  - `remediation`;
  - `effort_compatibility`;
  - `effort_note`.

- Runtime provider switch receipts now include the same readiness/effort
  compatibility fields on successful switch.

## Verification

TDD red check:

- `go test ./cmd/elnath -run 'TestProviderCommandStatusJSON|TestProviderCommandCheckJSON|TestProviderSelectionCheckRejectsUnlistedProviderCandidate' -count=1`
  - initially failed because `providerStatusView` and
    `providerSelectionCheckView` did not expose readiness, failure family,
    issue, remediation, or effort compatibility fields.

Passing checks:

- `go test ./cmd/elnath -run 'TestProviderCommandStatusJSON|TestProviderCommandCheckJSON|TestProviderSelectionCheckRejectsUnlistedProviderCandidate' -count=1`
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

- This slice classifies local configuration readiness, not live upstream API
  health. A future doctor/provider live-check can probe upstream responses with
  a bounded request.
- `auth_permanent` still belongs to runtime/provider HTTP error classification,
  not local config inspection. Existing `errorclass` paths already classify
  HTTP 403/permanent auth failures.
- OpenAI Responses-compatible endpoints remain first-class, but classic
  Chat Completions transformation is still outside this slice.

## Next recommended milestone

Milestone 6: install/update/doctor hardening.

Recommended next slice:

- add secret/env file permission diagnostics;
- add optional integration health checks;
- make missing optional backends produce remediation instead of ambiguous
  failures.
