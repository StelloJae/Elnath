# Approval Consumption Milestone - 2026-05-17

## Summary

Branch: `codex/approval-consumption`

This milestone closes the immediate post-PR253 approval continuation gap:

- a pending approval still blocks and can be reused while pending;
- an approved matching approval can be consumed exactly once;
- the matching approved tool call executes once after consumption;
- the execution receipt is linked to the consumed approval id;
- consumed approvals are not reused for later matching calls;
- denied approvals are not consumed;
- approvals for a different actor are not consumed.

This is product/runtime work, not benchmark work.

## Reference Inputs

Control references:

- `.omc/research/elnath-ultimate-goal-codex-claude-hermes-convergence-2026-05-17.md`
- `.omc/research/elnath-convergence-gap-map-2026-05-17.md`
- `.omc/research/approved-request-continuation-design-2026-05-17.md`

Elnath files inspected:

- `internal/agentic/tools/gateway.go`
- `internal/agentic/tools/gateway_test.go`
- `internal/agentic/store.go`
- `internal/daemon/approval_store.go`
- `internal/daemon/approval_store_test.go`
- `cmd/elnath/cmd_agentic.go`

Reference pattern:

- operator approval is a bounded permission, not an unlimited policy change;
- approval should be task/actor/tool/input scoped;
- approval consumption must leave durable receipt evidence.

## Changes

Changed files:

- `cmd/elnath/cmd_agentic.go`
- `cmd/elnath/cmd_agentic_test.go`
- `internal/daemon/approval_store.go`
- `internal/daemon/approval_store_test.go`
- `internal/agentic/store.go`
- `internal/agentic/tools/gateway.go`
- `internal/agentic/tools/gateway_test.go`

Behavior added:

1. `approval_requests` now records single-use consumption metadata:
   - `consumed_at`
   - `consumed_by_receipt_id`
2. Legacy approval tables migrate those columns with default unconsumed values.
3. `agentic.Store.ConsumeApprovedApprovalRequestID` atomically consumes one approved matching approval.
4. The gateway checks for a matching approved unconsumed approval before creating/reusing a pending approval.
5. When consumed, the gateway executes the exact matching tool call once and links the resulting receipt to the consumed approval id.
6. Finalized tool-result receipts preserve the approval id.
7. `elnath agentic task`, `elnath agentic lineage`, and `elnath agentic evidence` now render approval consumption evidence.
8. Approval JSON structures now include `consumed_at` and `consumed_by_receipt_id` when present.

## Tests Added

New gateway coverage:

- `TestToolGateway_MutatingActionConsumesApprovedApprovalOnce`
- `TestToolGateway_MutatingActionDoesNotConsumeDeniedApproval`
- `TestToolGateway_MutatingActionDoesNotConsumeApprovedApprovalForDifferentActor`

Updated daemon migration coverage:

- `TestApprovalStore_MigratesProvenanceColumns`

New CLI/evidence coverage:

- `TestAgenticCommand_EvidenceShowsConsumedApproval`
- `TestAgenticCLI_ApprovalsHandleLegacyTableWithoutConsumptionColumns`

## Verification

Commands and results:

- `go test ./internal/agentic/tools -run 'TestToolGateway_MutatingAction(ConsumesApprovedApprovalOnce|DoesNotConsumeDeniedApproval|DoesNotConsumeApprovedApprovalForDifferentActor|ReusesPendingApprovalForSameAction)$' -count=1`
  - PASS
- `go test ./internal/daemon -run 'TestApprovalStore' -count=1`
  - PASS
- `go test ./internal/agentic ./internal/agentic/tools ./internal/daemon ./internal/agentic/approvals -count=1`
  - PASS
- `go test ./internal/agentic/... ./internal/daemon -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestCmdAgentic|TestRuntimeAgentic' -count=1`
  - PASS, no tests matched
- `go test ./cmd/elnath -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestAgenticCommand_(EvidenceShowsConsumedApproval|EvidenceShowsCompactTaskEvidenceChain|LineageShowsGoalSignalTaskActorPolicyApprovalReceiptVerificationMemoryFollowup|TaskShowsCoreTaskLinks|ApprovalsListsPendingRequests|ApproveDecidesPendingRequest|DenyDecidesPendingRequestJSON)$' -count=1`
  - PASS
- `go test ./cmd/elnath -run 'TestAgentic(Command_ApprovalsListsPendingRequests|CLI_ApprovalsHandleLegacyTableWithoutConsumptionColumns|Command_EvidenceShowsConsumedApproval|Command_EvidenceShowsCompactTaskEvidenceChain|Command_ApproveDecidesPendingRequest|Command_DenyDecidesPendingRequestJSON)' -count=1`
  - PASS
- `go test ./internal/agentic/... ./internal/daemon -count=1`
  - PASS
- `git diff --check -- internal/agentic/store.go internal/agentic/tools/gateway.go internal/agentic/tools/gateway_test.go internal/daemon/approval_store.go internal/daemon/approval_store_test.go`
  - PASS
- `git diff --check -- cmd/elnath/cmd_agentic.go cmd/elnath/cmd_agentic_test.go internal/agentic/store.go internal/agentic/tools/gateway.go internal/agentic/tools/gateway_test.go internal/daemon/approval_store.go internal/daemon/approval_store_test.go`
  - PASS
- `go vet ./...`
  - PASS

Benchmark run:

- not run

Corpus or baseline mutation:

- no

## Claim Boundary

Allowed:

- Elnath can now consume a matching approved approval once in the gateway/store layer.
- The approved execution is receipt-linked.
- Pending approval reuse remains unchanged.
- Denied and actor-mismatched approvals do not execute.
- Consumed approval evidence is visible in agentic task/lineage/evidence JSON/rendering.

Not allowed:

- Elnath has full live approval wait UX.
- `elnath agentic approve` automatically resumes a blocked daemon task end-to-end.
- benchmark readiness improved or passed.
- Elnath is better than Codex, Claude Code, or Hermes.

## Remaining Risks

- There is no explicit `approve --resume` or live wait UX yet.
- This slice proves the gateway/store continuation primitive, not full operator workflow completion.

## Next Milestone Recommendation

Next structural milestone:

- decide and implement the next bounded approval continuation UX:
  - explicit `approve --resume`, or
  - bounded live-wait approval execution.

Keep benchmark lanes paused until the approval/control-loop UX is stronger.
