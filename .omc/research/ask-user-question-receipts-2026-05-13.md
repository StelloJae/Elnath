# AskUserQuestion receipt boundary

Date: 2026-05-13
Branch: `codex/ask-user-question-receipts`

## Claim

`ask_user_question` now returns a structured receipt for user-input requests.

This improves observability without implementing blocking wait/resume.

## Scope

Changed:

- Added `receipt` to successful `ask_user_question` output.
- Receipt includes:
  - `tool=ask_user_question`
  - `action=request`
  - `read_only=true`
  - `execution_policy=user_input_request`
  - `question_chars`
  - `option_count`
  - `allow_free_text`
  - `timeout_seconds`
- Question text is not duplicated in the receipt.
- Completion summaries now capture `ask_user_question` as a control-tool receipt.
- Learning and agentic receipt structs preserve the new bounded metadata.

Not changed:

- no blocking user-input wait
- no daemon/TUI resume flow
- no persisted user-question state machine
- no benchmark corpus, baseline, or v8 evidence changes

## Evidence

TDD red before implementation:

- `go test ./internal/agent -run TestAskUserQuestionToolReturnsStructuredRequest -count=1`
- Result: FAIL as expected; output lacked `Receipt`.

Focused verification after implementation:

- `go test ./internal/agent -run TestAskUserQuestionTool -count=1`
- Result: PASS

- `go test ./cmd/elnath -run TestCompletionContractSummaryRecordsAskUserQuestionReceipt -count=1`
- Result: PASS

- `go test ./internal/learning ./internal/agentic/completion -count=1`
- Result: PASS; verifies JSON outcome/gate preservation for the new receipt fields.

Broader verification:

- `go test ./internal/agent ./cmd/elnath ./internal/agentic/completion ./internal/learning -count=1`
- Result: PASS

- `go vet ./...`
- Result: PASS

- `git diff --check`
- Result: PASS

## Claim boundary

Allowed:

- `ask_user_question` requests are now receipt-backed.
- Elnath can record user-question request metadata without storing duplicate question text in receipts.

Not allowed:

- no claim that Elnath can pause and resume on user input.
- no claim that daemon/TUI approval wait is implemented.
- no v8 benchmark claim.
