# W2: Hermes Parity — #9 9-section structured compression summary

**Target**: `feat/hermes-parity-w2`
**Estimated LOC**: 250-400 (code) + ~200 (tests)
**Estimated time**: 5-7 hours (opencode, largest of the three workers)
**Depends on**: nothing
**Conflicts with**: nothing (owns `internal/conversation/` exclusively; W1 and W3 MUST NOT touch conversation package)

## Context (read first)

Current Elnath Stage 2 auto-compression uses a **2-line unstructured prompt** at `internal/conversation/context.go:26-29`:

```go
const summarizePrompt = `Summarize the following conversation history concisely.

Output only the summary, no preamble.`
```

The output is a free-form paragraph. After multiple compression rounds, the summary drifts — newer sessions' context gets summarized on top of an already-summarized blob, losing structure.

Hermes compression uses a **9-section structured template** that:
1. Produces consistent output shape (parseable),
2. Supports **iterative update** — on round N, the LLM receives the prior round's structured summary and appends/mutates sections rather than re-summarizing from scratch,
3. Preserves critical retention categories across rounds.

This is the largest Hermes gap by impact: long sessions in Elnath currently degrade compression quality monotonically.

## W2 scope

### Task A — Define the 9-section template

Add to `internal/conversation/context.go` (or new file `structured_summary.go`):

```go
const structuredSummaryTemplate = `# Session Summary

## 1. User goal
<primary objective the user is working toward, one paragraph>

## 2. Completed steps
<bullet list of concrete actions taken, in chronological order; keep every entry below 25 words>

## 3. Current focus
<what the agent is actively working on right now, one paragraph>

## 4. Files touched
<list "path — action (read/write/edit)" entries; do not include ephemeral tool_result markers>

## 5. Outstanding TODOs
<bullet list of work items not yet started; cross-reference user goal>

## 6. Blockers / unresolved
<list known blockers with symptoms; empty list allowed>

## 7. Key decisions
<bullet list of decisions with one-line rationale each>

## 8. Open questions
<questions the agent has for the user, or ambiguities requiring clarification>

## 9. Next action
<single most specific next step, expressed as an imperative sentence>
`
```

### Task B — Iterative-update compression prompt

Replace the current `summarizePrompt` with an iterative-update prompt that:
- Takes the **previous round's structured summary** (if any) as context
- Takes the **new messages since last compression**
- Returns a **new structured summary** by updating sections in-place

```go
const structuredSummaryPromptNewSession = `You are compressing a conversation. Produce a structured summary using this exact template:

` + structuredSummaryTemplate + `

Rules:
- Fill every section. Write "(none)" if a section has no content yet.
- Keep total output under 2000 tokens.
- Do not echo raw tool outputs or code blocks from the conversation.
- Section 9 must be an imperative sentence.

Conversation:
<INSERT CONVERSATION HERE>`

const structuredSummaryPromptIterative = `You are updating a structured conversation summary. Merge the new messages into the existing summary.

Existing summary:
<INSERT EXISTING SUMMARY HERE>

New messages since last compression:
<INSERT NEW MESSAGES HERE>

Rules:
- Use the same 9-section template as the existing summary.
- Preserve section 1 (user goal) unless the user has explicitly pivoted.
- Add to section 2 (completed steps) rather than rewriting prior entries.
- Move items from section 5 (TODOs) to section 2 when they finish.
- Keep total output under 2000 tokens.
- Do not echo raw tool outputs or code blocks.
- Section 9 must be an imperative sentence.

Output only the updated summary, no preamble.`
```

### Task C — Detection & parsing of prior summaries

Add `internal/conversation/structured_summary.go` with:

```go
// parseStructuredSummary returns the 9-section body if the content matches
// the structured summary shape, or empty string + false otherwise.
func parseStructuredSummary(content string) (body string, ok bool)

// isStructuredSummaryMessage returns true if the message is a previously
// emitted structured summary that should be fed back into the iterative
// compression prompt.
func isStructuredSummaryMessage(msg llm.Message) bool
```

Detection: look for `# Session Summary\n\n## 1. User goal` at the start of a text block. If found, strip back to `# Session Summary` and treat the whole message as the prior structured summary.

### Task D — Wire iterative prompt into `autoCompress` / `flatSummarize`

- In `autoCompress` (around `internal/conversation/context.go:286`), check if any message in `toCompress` or earlier history matches `isStructuredSummaryMessage`. If yes, route to `structuredSummaryPromptIterative` and pass the existing summary separately. If no, route to `structuredSummaryPromptNewSession`.
- The output of compression should be a single assistant message whose text is the structured summary (replacing the old compression artifact format).
- `flatSummarize` (around line 299) also needs updating — same routing logic.

### Task E — Compatibility with existing compression flow

- `microCompress` (Stage 1) and `snip` (Stage 3) are unchanged.
- Stage 2 output format changes — the assistant message that replaces the old messages now contains a structured summary instead of a free-form paragraph. The session JSONL will contain these messages going forward.
- **Important**: legacy sessions with old free-form summaries must still load. Don't assume every compression marker is structured.

### Task F — Token budget handling

- If the LLM returns a structured summary > 2000 tokens despite the rule, the orchestrator must still function. Apply Stage 3 snip as fallback.
- If the LLM returns malformed output (missing sections), fall back to the old one-shot summarizer (keep the old prompt as `legacyUnstructuredSummaryPrompt` reachable for fallback). Log a warning.

## Files touched

- `internal/conversation/context.go` — replace summarizePrompt constant region; add routing logic in autoCompress/flatSummarize
- `internal/conversation/structured_summary.go` — NEW file with constants + parsing helpers
- `internal/conversation/context_test.go` — extend existing tests; add new ones per below
- `internal/conversation/structured_summary_test.go` — NEW file for parser tests

**DO NOT TOUCH**:
- `internal/agent/*` (W1)
- `internal/tools/*` (W3)
- `cmd/elnath/*`

## Required tests

### structured_summary_test.go
1. `TestParseStructuredSummary_Valid` — 9-section input parses, body returned, ok=true
2. `TestParseStructuredSummary_MissingSection` — 8 sections only → ok=false
3. `TestParseStructuredSummary_WrongHeader` — missing `# Session Summary` → ok=false
4. `TestIsStructuredSummaryMessage_Assistant` — assistant message with valid body → true
5. `TestIsStructuredSummaryMessage_User` — user message is never a summary → false

### context_test.go extensions
6. `TestCompressMessages_UsesNewSessionPromptWhenNoPriorSummary` — mockProvider captures prompt; verify it matches new-session template
7. `TestCompressMessages_UsesIterativePromptWhenPriorSummaryPresent` — inject a prior structured summary in messages; verify iterative prompt is used with that summary substituted
8. `TestCompressMessages_OutputIsStructuredSummary` — LLM returns structured text; compression result contains exactly one assistant message whose text parses as structured summary
9. `TestCompressMessages_MalformedOutputFallsBackToLegacy` — LLM returns one-line output; fallback to legacy prompt triggered, no error bubbled
10. `TestCompressMessages_PreservesOnAutoCompressCallback` — regression check that W2 changes don't break the W1 OnAutoCompress hook wiring

## Behavior invariants

- Structured template sections must be stable across compression rounds (parser depends on exact `## 1.`, `## 2.`, …, `## 9.` numbering)
- Compression output replaces `toCompress` messages with exactly **one** assistant message (not multiple)
- Fallback to legacy prompt never bubbles errors — log warning + use the fallback output
- Token budget soft-capped at 2000 tokens in the prompt rule, hard-enforced by Stage 3 snip

## Verification

```bash
go test -race ./internal/conversation/...
go vet ./internal/conversation/...
go build ./...
```

## PR body template

```
## Summary

- 9-section structured summary template (Session Summary with sections 1-9)
- Iterative-update prompt: updates existing summary in place instead of re-summarizing
- Structured summary detection + parser, legacy unstructured prompt kept as fallback
- Tests: 5 parser tests + 5 compression-integration tests

Hermes parity item #9 complete. Long sessions no longer drift through repeated one-shot summarization.

## Test plan

- [ ] `go test -race ./internal/conversation/...` PASS
- [ ] `go test -race ./...` full suite PASS (regression check)
- [ ] Manual: create a 30-message mock session, trigger compression twice, verify second round receives the first round's structured output
```

## Notes for the worker

- Do NOT change `ContextWindow` struct fields touched by W1 (`onAutoCompress`). The W1 callback must still fire after a successful Stage 2 compression run, regardless of which prompt variant was used.
- `feedback_no_stubs.md`: if the parser is too strict and rejects valid LLM outputs, **fix the parser** (regex, tolerant whitespace), do not stub/comment out the check.
- `feedback_research_before_spec.md`: the exact 9 sections in this spec are an informed guess — Hermes source was not accessible. If dog-food evidence later shows 1-2 sections are useless, treat it as a section revision PR, not a rewrite.
- Keep `legacyUnstructuredSummaryPrompt` available. If a future Elnath session depends on the legacy format for some reason (eval suite?), we need the option to run it.
