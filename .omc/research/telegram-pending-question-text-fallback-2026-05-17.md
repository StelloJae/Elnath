# Telegram Pending Question Text Fallback - 2026-05-17

## Summary

Branch: `codex/session-handoff-recap`

This milestone adds a small Elnath-native user input UX improvement: when a Telegram chat is bound to a session and that session has exactly one pending user question, a plain non-command Telegram message can now be captured as the answer instead of being misrouted as a new task.

This is a product/runtime completion slice, not benchmark work.

## Reference Files Inspected

Elnath:

- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `internal/telegram/binding.go`
- `internal/learning/pending_questions.go`
- `internal/agent/user_question_tool.go`
- `internal/daemon/task_tools.go`

Hermes:

- `/Users/stello/.hermes/hermes-agent/tools/clarify_gateway.py`
- `/Users/stello/.hermes/hermes-agent/gateway/platforms/telegram.py`

Reference pattern used:

- Hermes has a clarify gateway with a text fallback path.
- Rich UI/button callbacks are broader than this Elnath slice.
- Elnath now takes the narrower compatible behavior: a bound session with one pending question may consume plain text as the answer.

## Behavior Added

`internal/telegram/shell.go` now checks non-command messages for pending user-question answers before intent classification and task enqueue.

Capture rules:

- Requires `OutcomeStore`.
- Requires `ChatSessionBinder`.
- Requires a valid chat/user binding to a session.
- Requires exactly one pending question for the bound session.
- For structured-choice questions, the text must match one pending option exactly after trimming.
- If free text is allowed, any non-empty text is accepted.
- Multiple pending questions do not enqueue a task; Telegram replies with guidance to use `/questions` and `/answer`.
- Invalid structured choices do not enqueue a task; Telegram replies with mismatch guidance.

The existing `/answer <session_id> <request_id> <answer>` command still works and now shares the same enqueue helper.

`/questions` now also shows a direct-reply hint when the requesting Telegram user is bound to a session with exactly one pending question. This keeps the new fallback discoverable without adding Telegram inline callback support in this slice.

## Changed Files

- `internal/telegram/shell.go`
- `internal/telegram/shell_test.go`
- `.omc/research/telegram-pending-question-text-fallback-2026-05-17.md`

## Verification

Focused TDD failure before implementation:

```text
go test ./internal/telegram -run 'TestShellPlainTextQuestionAnswer|TestShellPlainTextAnswersSingleBoundPendingQuestion' -count=1
FAIL
```

Expected failures showed plain text was being enqueued as a normal task.

Post-implementation checks:

```text
go test ./internal/telegram -run 'TestShellPlainTextQuestionAnswer|TestShellPlainTextAnswersSingleBoundPendingQuestion|TestShellAnswerCommandEnqueuesPendingQuestionAnswer' -count=1
PASS
ok github.com/stello/elnath/internal/telegram 0.670s
```

```text
go test ./internal/telegram -run 'TestShellQuestionsCommandShowsDirectReplyHintForSingleBoundQuestion|TestShellQuestionsCommandShowsPendingUserChoices|TestShellQuestionsCommandShowsFreeTextTimeoutAndCancel|TestShellQuestionsCommandDoesNotRenderAnswerForUnboundQuestion' -count=1
PASS
ok github.com/stello/elnath/internal/telegram 0.663s
```

```text
go test ./internal/telegram -count=1
PASS
ok github.com/stello/elnath/internal/telegram 15.331s
```

```text
go test ./internal/telegram ./internal/daemon ./internal/learning -count=1
PASS
ok github.com/stello/elnath/internal/telegram 15.654s
ok github.com/stello/elnath/internal/daemon 40.618s
ok github.com/stello/elnath/internal/learning 1.175s
```

```text
go test ./internal/... ./cmd/elnath -count=1
PASS
ok github.com/stello/elnath/internal/agent 11.242s
ok github.com/stello/elnath/internal/daemon 43.932s
ok github.com/stello/elnath/internal/eval 21.863s
ok github.com/stello/elnath/internal/telegram 18.300s
ok github.com/stello/elnath/internal/tools 44.970s
ok github.com/stello/elnath/cmd/elnath 20.909s
```

```text
go vet ./...
PASS
```

```text
git diff --check
PASS
```

## Benchmark / Corpus Boundary

- Full v8 benchmark: not run.
- Baseline: not run.
- Codex comparison: not run.
- Claude comparison: not run.
- Benchmark corpus changed: no.
- Baseline artifacts changed: no.
- Benchmark superiority claim: no.

## Claim Boundary

Allowed:

- Telegram can now capture a plain text answer for a single bound pending user question.
- Invalid or ambiguous pending-question text is no longer silently enqueued as a normal task in the covered paths.

Not claimed:

- Full Telegram inline button support.
- General natural-language disambiguation for all pending questions.
- Blocking Hermes-style clarify thread behavior.
- Benchmark readiness improvement.
- Elnath superiority over Codex, Claude Code, or Hermes.

## Remaining Risk

- Telegram `Update` / `BotClient` still do not support callback queries or inline keyboards.
- This fallback is session-bound and single-pending-question only by design.
- Multi-question UX still requires `/questions` and explicit `/answer`.

## Next Recommended Milestone

Continue product/runtime completion, not benchmark loops.

Suggested next structural blocker:

- Add a small command/user-input discoverability improvement, or
- move to a code-intelligence slice if the goal needs more runtime substance before opening one coherent PR.
