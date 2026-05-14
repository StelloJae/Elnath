# User Question Wait Tool

Date: 2026-05-14
Branch: `codex/user-question-wait`
Status: implemented locally

## Goal

Add a bounded `user_question_wait` tool for pending clarification questions.

This closes part of the `user_input` partial gap by letting the model observe
whether a specific `ask_user_question` request has been answered without
unbounded polling.

## Reference Check

- Claude Code remote/session flows keep pending permission requests keyed by
  request id and resolve or cancel them explicitly.
- Hermes MCP bridge exposes event wait/list/respond surfaces for pending
  approvals.
- Elnath already has `ask_user_question`, `user_question_list`, and
  `user_question_answer`, but only list/answer surfaces are model-callable for
  follow-up observation.

Design choice: add an Elnath-native read-only wait tool backed by outcome
receipts. Do not copy reference source, prompts, or errors.

## Intended Behavior

- Require `session_id` and `request_id`.
- Return immediately as `answered` if an answer receipt already exists.
- Wait up to bounded `wait_ms` for an answer receipt to appear.
- Return `pending` with `wait_timed_out=true` when still unanswered.
- Return `not_found` when no matching pending/answered question exists.
- Preserve wait metadata in completion-control receipts.

## Changed Files

- `internal/learning/user_question_tools.go`
- `internal/learning/user_question_tools_test.go`
- `cmd/elnath/runtime.go`
- `cmd/elnath/runtime_command_tool_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`
- `cmd/elnath/runtime_completion_observability.go`
- `cmd/elnath/runtime_completion_observability_test.go`
- `internal/tools/tool_search.go`
- `internal/tools/tool_search_test.go`
- `internal/agent/permission.go`
- `internal/agent/permission_test.go`

## Behavior Added

- Added read-only deferred `user_question_wait`.
- Added bounded wait policy:
  - default `30000ms`
  - max `300000ms`
  - poll interval `25ms`
- `user_question_wait` returns:
  - `answered` when matching `user_question_answer` receipt exists
  - `pending` with `wait_timed_out=true` when unanswered after wait
  - `not_found` when no matching request exists
- Registered the tool in runtime, ToolSearch routing, plan-mode read-only
  permissions, control-surface manifest, and completion receipt collection.

## Verification

- Initial TDD proof failed as expected:
  - `go test ./internal/learning -run 'TestUserQuestion(Wait|List)' -count=1`
    - failed: `NewUserQuestionWaitTool`, `UserQuestionWaitToolName`, and wait
      output type undefined
  - `go test ./cmd/elnath -run 'TestExecutionRuntimeRegistersAskUserQuestionTool|TestExplainControlSurfacesJSON|TestCompletionContractSummaryRecordsUserQuestionWaitReceipt' -count=1`
    - failed before implementation; test was then corrected to use
      `orchestrator.WorkflowResult`
  - `go test ./internal/tools ./internal/agent -run 'TestToolSearchReportsRoutingMetadata|TestPermissionModes|TestAcceptEditsAutoApprovesSafeTools' -count=1`
    - failed: `user_question_wait` routing metadata was `other/registry`
- Focused verification after implementation:
  - `go test ./internal/learning -run 'TestUserQuestion(Wait|List)' -count=1`
    - PASS: `ok github.com/stello/elnath/internal/learning 0.649s`
  - `go test ./cmd/elnath -run 'TestExecutionRuntimeRegistersAskUserQuestionTool|TestExplainControlSurfacesJSON|TestCompletionContractSummaryRecordsUserQuestionWaitReceipt' -count=1`
    - PASS: `ok github.com/stello/elnath/cmd/elnath 0.985s`
  - `go test ./internal/tools ./internal/agent -run 'TestToolSearchReportsRoutingMetadata|TestPermissionModes|TestAcceptEditsAutoApprovesSafeTools' -count=1`
    - PASS: `internal/tools 0.934s`, `internal/agent 0.463s`
- Proportional broader verification:
  - `go test ./internal/learning ./cmd/elnath ./internal/tools ./internal/agent -count=1`
    - PASS: `internal/learning 0.683s`, `cmd/elnath 20.183s`,
      `internal/tools 39.184s`, `internal/agent 11.774s`
  - `go vet ./internal/learning ./cmd/elnath ./internal/tools ./internal/agent`
    - PASS
  - `git diff --check`
    - PASS

## Boundary

- Full v8 benchmark: not run.
- Baseline: not run.
- Codex/Claude comparison: not run.
- Benchmark corpus mutation: none.
- Baseline artifact mutation: none.

## Claim Boundary

Allowed:

- Elnath now has a bounded read-only `user_question_wait` surface.
- The user-input surface now supports request, list, wait, and answer enqueue.
- Wait receipts are captured by completion observability.

Forbidden:

- Full UI-level answer collection is complete.
- Elnath benchmark success.
- Elnath is better than Claude Code or Codex.
- Full autonomous completion program is done.

## Remaining Risk

- `user_question_wait` observes outcome receipts; it does not itself deliver UI
  notifications.
- Answer text remains in the queued follow-up task payload, not in the wait
  receipt.

## Next Recommendation

Commit this coherent user-input milestone, open one PR, wait for CI, merge if
green, then continue to the next structural blocker.
