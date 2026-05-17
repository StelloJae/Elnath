# Telegram User Question Operator UX

Date: 2026-05-17 KST

Branch:

- `codex/user-input-operator-ux`

Upstream context:

- PR #254 draft separated the prior approval-continuation milestone:
  `https://github.com/StelloJae/Elnath/pull/254`
- This milestone starts from `origin/main`, not from PR #254, to avoid stacking
  unrelated runtime changes.

## Goal

Improve Elnath's user-input/operator UX without widening benchmark scope.

The specific gap:

- Elnath already supports pending user questions, `/questions`, `/answer`, plain
  text capture for a single bound pending question, cancellation, and receipts.
- For multiple-choice Telegram questions with `allow_free_text=false`, the
  operator previously had to answer with the exact option text.
- Hermes clarify gateway supports rich buttons and a numbered/text fallback.
- Elnath's Telegram shell needed the same operator ergonomics without changing
  the underlying receipt/queue contract.

## References Inspected

Elnath:

- `internal/agent/user_question_tool.go`
- `internal/learning/user_question_tools.go`
- `internal/learning/pending_questions.go`
- `internal/daemon/task_tools.go`
- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `internal/telegram/http_client.go`
- `internal/telegram/http_client_test.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tools/clarify_gateway.py`
- `/Users/stello/.hermes/hermes-agent/tests/gateway/test_telegram_clarify_buttons.py`

Claude Code:

- `/Users/stello/claude-code-src/src/tools/AskUserQuestionTool/AskUserQuestionTool.tsx`
- `/Users/stello/claude-code-src/src/tools/AskUserQuestionTool/prompt.ts`

Use boundary:

- Flow reference only.
- No proprietary source, prompts, or error strings copied.

## Change

Telegram pending-question rendering now supports native inline buttons when the
bot client supports them:

- `/questions` sends one inline button per structured choice.
- Button callback data uses `uq:<request_id>:<choice_number>`.
- Callback handling resolves the pending request, normalizes the selected number
  to the option text, enqueues the answer-resume task, acknowledges the callback,
  and sends the same operator receipt message as `/answer`.
- Plain text fallback still exists for clients or surfaces without buttons.

Telegram HTTP client support now covers:

- `sendMessage` with `reply_markup.inline_keyboard`;
- `answerCallbackQuery`;
- `getUpdates` parsing for `callback_query`.

Telegram pending-question rendering now shows numbered choices:

- `1. main`
- `2. new`

Telegram answer handling now normalizes numeric choices:

- `/answer sess-1 req-1 2` becomes answer `new`.
- Plain text `2` in a bound chat with exactly one pending question becomes
  answer `new`.

Existing behavior preserved:

- exact option text still works;
- free-text questions still accept arbitrary text;
- invalid choices still do not enqueue resume tasks;
- ambiguous multiple pending questions still require explicit `/answer`.

Daemon `user_question_answer` now also normalizes numeric choices:

- any caller using the validator-backed tool can send answer `2`;
- the queued resume prompt and answer receipt use the normalized option text;
- invalid numbers or non-option text remain rejected when free text is not
  allowed.

CLI pending-question explanation now advertises the numeric path:

- `options: 1. "main", 2. "new branch"`
- `choose 1: elnath task answer ... --answer '1'`
- `choose 2: elnath task answer ... --answer '2'`

## Changed Files

- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `internal/telegram/http_client.go`
- `internal/telegram/http_client_test.go`
- `internal/daemon/task_tools.go`
- `internal/daemon/task_tools_test.go`
- `cmd/elnath/cmd_explain.go`
- `cmd/elnath/cmd_explain_test.go`

## Verification

Run:

- `go test ./internal/telegram -run 'TestShell(QuestionsCommandShowsPendingUserChoices|AnswerCommandAcceptsNumericChoice|PlainTextAnswersSingleBoundPendingQuestionByNumber)' -count=1` failed before implementation as expected.
- `go test ./internal/telegram -run 'TestShell(QuestionsCommandShowsPendingUserChoices|AnswerCommandAcceptsNumericChoice|PlainTextAnswersSingleBoundPendingQuestion|PlainTextQuestionAnswerRejectsUnexpectedChoice)' -count=1` passed after implementation.
- `go test ./internal/telegram -run 'TestShellQuestionsCommandSendsChoiceButtons|TestShellQuestionChoiceCallbackEnqueuesAnswer' -count=1` passed.
- `go test ./internal/telegram -run 'TestHTTPClient(SendMessageWithButtons|AnswerCallback|GetUpdatesParsesCallbackQuery)' -count=1` passed.
- `go test ./internal/daemon -run 'TestUserQuestionAnswerTool(NormalizesNumericStructuredChoice|RejectsAnswerOutsideStructuredChoices|ValidatesPendingRequest)' -count=1` failed before daemon implementation as expected, then passed after implementation.
- `go test ./cmd/elnath -run TestExplainPendingQuestionsTextShowsAnswerCommand -count=1` failed before CLI renderer implementation as expected, then passed after implementation.
- `go test ./internal/telegram -count=1` passed.
- `go test ./internal/telegram ./internal/daemon ./internal/learning -count=1` passed.
- `go test ./cmd/elnath ./internal/telegram ./internal/daemon ./internal/learning -count=1` passed.
- `git diff --check -- internal/telegram/shell.go internal/telegram/shell_test.go internal/telegram/http_client.go internal/telegram/http_client_test.go internal/daemon/task_tools.go internal/daemon/task_tools_test.go cmd/elnath/cmd_explain.go cmd/elnath/cmd_explain_test.go .omc/research/telegram-user-question-numeric-choice-ux-2026-05-17.md` passed.

## Benchmark / Corpus Boundary

- No benchmark run.
- No baseline run.
- No corpus mutation.
- No public superiority claim.

## Claim Boundary

Allowed:

- Telegram pending user-question choices now support numeric fallback on the
  command path and bound-chat plain text path.
- Telegram `/questions` can expose structured choices as native inline buttons
  and callback selections enqueue receipt-backed answer-resume tasks.
- Validator-backed `user_question_answer` supports numeric fallback independent
  of Telegram.
- `elnath explain pending-questions` explains numeric choices to terminal
  operators.
- This improves operator UX toward Hermes-style clarify flow while preserving
  Elnath's queue/receipt contract.

Forbidden:

- Do not claim full Claude Code AskUserQuestion UI parity.
- Do not claim Elnath product completion from this small slice alone.

## Remaining Risk

- Callback data currently carries request ID and choice number only; if future
  multi-select or long-lived signed callback contracts are needed, add a
  bounded token/expiry layer.
- Multi-select questions remain outside this slice.

## Next Recommendation

Finish this small UX slice with proportional verification, then decide whether
to batch it with a second user-input UX improvement or keep it as a small local
milestone until PR #254 clears.
