# Approval Live Wait Design - 2026-05-17

## Purpose

Continue the approval-consumption milestone without turning benchmark failures
into the roadmap.

The next product/runtime gap is:

- `approval_required` can now create an approval;
- `elnath agentic approve` can decide it;
- gateway/store can consume an approved matching request once;
- but a running gateway task still does not naturally continue after the
  operator approves it.

## Reference Pattern

Elnath files inspected:

- `cmd/elnath/runtime.go`
- `cmd/elnath/cmd_daemon.go`
- `cmd/elnath/cmd_agentic.go`
- `cmd/elnath/runtime_agentic_enforcement_test.go`
- `internal/agentic/tools/gateway.go`
- `internal/agentic/approvals/bridge.go`
- `internal/daemon/approval_store.go`
- `internal/agentic/runtime/envelope.go`
- `internal/agentic/enqueue/enqueue.go`

Claude Code source inspected:

- `/Users/stello/claude-code-src/src/bridge/bridgePermissionCallbacks.ts`
- `/Users/stello/claude-code-src/src/utils/permissions/PermissionResult.ts`

Hermes source inspected:

- `/Users/stello/.hermes/hermes-agent/tools/approval.py`
- `/Users/stello/.hermes/hermes-agent/tests/tools/test_approval.py`

Observed pattern:

- approval is an operator/user-owned decision;
- approval response is bounded to a specific request;
- session identity matters;
- approvals must not become unlimited permission;
- gateway/async contexts need a way for a blocked request to receive a decision.

## Decision

Implement bounded live wait as an explicit opt-in:

- add `agentic.approval.wait_timeout_seconds`;
- default remains `0`, meaning no live wait and current behavior is preserved;
- when timeout is positive, gateway:
  1. creates/reuses an approval request;
  2. records the current receipt as `approval_required`;
  3. waits until approved/denied/timeout;
  4. on approval, consumes the approved request once and executes the exact
     matching tool call;
  5. on denial, completes the receipt as denied;
  6. on timeout, leaves the approval pending and returns approval-required.

Why not `approve --resume` first:

- current Elnath agentic tasks are one-to-one linked to daemon queue tasks;
- re-enqueueing the same agentic task would require changing task/queue lineage
  semantics or adding a new resume-envelope model;
- a new follow-up task would not naturally share the same `task_id`, so it
  would not consume the approved request without broader schema work;
- live wait directly solves the same-session continuation gap with less
  lineage churn.

## Boundaries

Allowed:

- bounded wait for one approval request;
- single-use approved execution;
- pending timeout remains pending;
- denied approval does not execute.

Forbidden:

- no default indefinite blocking;
- no direct generic tool replay CLI;
- no benchmark run;
- no broad self-healing claim;
- no Codex/Claude/Hermes superiority claim.

## Tests Required

- approved live wait executes once and marks approval consumed;
- denied live wait does not execute and marks receipt denied;
- timeout live wait does not execute and leaves approval pending;
- default gateway behavior remains no-wait;
- config default is disabled and rejects negative timeout.

## Implementation Evidence

Branch: `codex/approval-consumption`

Changed files:

- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_agentic_enforcement_test.go`
- `internal/agentic/approvals/bridge.go`
- `internal/agentic/tools/gateway.go`
- `internal/agentic/tools/gateway_test.go`
- `internal/config/config.go`
- `internal/config/defaults.go`
- `internal/config/agentic_enforcement_test.go`

Behavior implemented:

- `agentic.approval.wait_timeout_seconds` config was added.
- Default is `0`, so live wait is disabled unless explicitly configured.
- Negative timeout is rejected by config validation.
- Runtime gateway wiring enables wait only when the timeout is positive.
- Gateway writes an `approval_required` receipt before waiting.
- Approved responses consume the approval once and execute the exact tool call.
- Denied responses complete the receipt as denied and do not execute.
- Timeout returns `approval_required`, keeps the approval pending, and does not execute.

Verification results:

- `go test ./internal/agentic/tools -run 'TestToolGateway_MutatingActionLiveWait|TestToolGateway_MutatingActionConsumesApprovedApprovalOnce' -count=1`
  - PASS
- `go test ./internal/config -run 'TestAgentic(EnforcementConfig_DefaultsToObservePassThrough|EnforcementConfig_LoadGatewayMode|ApprovalConfig_RejectsNegativeWaitTimeout)' -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestGatewayOptIn_LiveWaitConsumesApprovalAndContinues|TestGatewayOptIn_MutatingToolRequiresApprovalAndDoesNotExecute|TestGatewayOptIn_DoesNotGateQueueMarkDone' -count=1`
  - PASS
- `go test ./internal/agentic/tools ./internal/agentic/approvals ./internal/config -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestGatewayOptIn|TestAgenticCommand' -count=1`
  - PASS
- `go test ./cmd/elnath -count=1`
  - PASS
- `go test ./internal/agentic/... ./internal/daemon ./internal/config -count=1`
  - PASS
- `go vet ./...`
  - PASS
- `git diff --check -- cmd/elnath/runtime.go cmd/elnath/runtime_agentic_enforcement_test.go internal/agentic/approvals/bridge.go internal/agentic/tools/gateway.go internal/agentic/tools/gateway_test.go internal/config/config.go internal/config/defaults.go internal/config/agentic_enforcement_test.go`
  - PASS

Benchmark run:

- not run

Corpus or baseline mutation:

- no

## Claim Boundary

Allowed:

- Elnath now supports opt-in bounded live wait for agentic tool approvals.
- Default behavior remains no-wait.
- Approved live wait consumes approval once and continues the tool flow.
- Denied and timeout paths do not execute the tool.

Not allowed:

- approval UX is fully complete across every surface;
- benchmark readiness was proven;
- Elnath is better than Codex, Claude Code, or Hermes.

## Next Milestone

Next likely structural blocker:

- surface live-wait config and approval continuation state more clearly in
  operator docs/status, then decide whether `approve --resume` is still needed
  as a separate offline continuation path.
