# Phase 7 Stage A — Flywheel Activation + Critical Fixes

> **For agentic workers:** Use superpowers:executing-plans or superpowers:subagent-driven-development to implement this plan task-by-task.

**Date**: 2026-04-17
**Goal**: Activate the Phase 5.3 self-improvement flywheel in production (outcomes.jsonl has been empty for 14 days despite dog-food traffic) and land all pre-Phase-7 HIGH/MEDIUM fixes before Maturity Scorecard measurement.

**Context**: Phase 5 + Phase 6 are code-complete. Three parallel audits (architect / explore / code-reviewer) found:
- ❌ `~/.elnath/data/outcomes.jsonl` does not exist. daemon.log shows 0 "outcome store" and 0 "routing advisor" log entries over 14 days.
- ✅ A.1 root cause fixed: `cmd/elnath/runtime.go:607` used `rt.principal.ProjectID` (daemon workspace fallback) instead of the session's actual principal. Session principal preferred when real (not legacy `"unknown"` sentinel).
- ❌ Workflow errors skip outcome recording (survivorship bias in learning data).
- ❌ `SaveUserWorkflowPreference` accumulates duplicate entries in `AvoidWorkflows`.
- ❌ 5 wiki Upsert paths skip `SetSource` (missing provenance).
- ❌ LLM cost table has no entries for `gpt-5.4/5.4-mini/5.2` (fallback overestimates 2x).
- ❌ Phase 6.7 slash commands (`/remember`, `/forget`, `/override`, `/undo`) have zero unit tests.
- ❌ F-5.2 Lesson Consolidation is completely absent (deferred to Stage B).

**Load-bearing principle**: Measuring the scorecard (Phase 7.2) or running benchmark v2 (Phase 7.3) without these fixes would produce data that cannot support the "시간이 갈수록 더 똑똑해지는" claim because the flywheel has never actually fed back into routing.

---

## Status

- [x] **A.1** — Critical runtime.go:607 fix applied and verified (landed in working tree, uncommitted).
- [ ] **A.2** — Record outcomes on workflow error path (runtime.go:699-704).
- [ ] **A.3** — Dedup `AvoidWorkflows` in `SaveUserWorkflowPreference`.
- [ ] **A.4** — Filter `PreferenceUsed==true` outcomes in `RoutingAdvisor.Advise`.
- [ ] **A.5** — Add `SetSource` to 5 wiki Upsert paths.
- [ ] **A.6** — Add GPT-5.4 / 5.4-mini / 5.2 cost entries.
- [ ] **A.7** — Extract `LessonsNode` magic-number threshold.
- [ ] **A.8** — Table-driven tests for Phase 6.7 slash commands.
- [ ] **A.9** — Telegram principal ProjectID verification (ensure dog-food tasks actually get a real ProjectID).
- [ ] **A.10** — Archive stale plan artifact; cleanup.
- [ ] **A.11** — Full verification + manual dog-food probe.

---

## A.2 — Outcome on Error Path

**File**: `cmd/elnath/runtime.go:699-732`

**Current**:
```go
wfStart := time.Now()
result, err := wf.Run(ctx, input)
elapsed := time.Since(wfStart)
if err != nil {
    return nil, "", fmt.Errorf("workflow %s: %w", wf.Name(), err)
}
if rt.outcomeStore != nil && routeCtx.ProjectID != "" && learning.ShouldRecord(result.FinishReason) {
    record := learning.OutcomeRecord{ ... }
    rt.outcomeStore.Append(record)
    ...
}
```

**Change**: Record an error outcome before the early return. Extract the recording block into a helper so both success and error paths share it.

```go
wfStart := time.Now()
result, err := wf.Run(ctx, input)
elapsed := time.Since(wfStart)

if err != nil {
    rt.recordOutcome(routeCtx, intent, wf.Name(), "error", false, elapsed, 0, userInput, pref != nil)
    return nil, "", fmt.Errorf("workflow %s: %w", wf.Name(), err)
}

if rt.outcomeStore != nil && routeCtx.ProjectID != "" && learning.ShouldRecord(result.FinishReason) {
    rt.recordOutcome(routeCtx, intent, result.Workflow, result.FinishReason, learning.IsSuccessful(result.FinishReason), elapsed, result.Iterations, userInput, pref != nil)
    // advisor + wiki preference update stays here
    ...
}
```

Create `recordOutcome` helper that owns the `rt.outcomeStore != nil && routeCtx.ProjectID != ""` guard so the error path gets the same safety check. Include a warn log on append failure.

**Test**: Add `TestExecutionRuntimeRunTaskRecordsOutcomeOnWorkflowError` in `cmd/elnath/runtime_test.go` — stub workflow returns error, assert outcome file has exactly 1 record with `Success=false`, `FinishReason="error"`.

**Acceptance**: Error outcomes appear in outcomes.jsonl; RoutingAdvisor sees the full distribution.

---

## A.3 — Dedup `AvoidWorkflows`

**File**: `internal/wiki/routing_write.go:95`

**Bug**: `merged.AvoidWorkflows = append(merged.AvoidWorkflows, pref.AvoidWorkflows...)` grows unboundedly with duplicates on repeated `/override` calls.

**Change**: Deduplicate before write using a string set.

```go
seen := make(map[string]struct{}, len(merged.AvoidWorkflows)+len(pref.AvoidWorkflows))
out := merged.AvoidWorkflows[:0]
for _, w := range merged.AvoidWorkflows {
    if _, ok := seen[w]; ok { continue }
    seen[w] = struct{}{}
    out = append(out, w)
}
for _, w := range pref.AvoidWorkflows {
    if _, ok := seen[w]; ok { continue }
    seen[w] = struct{}{}
    out = append(out, w)
}
merged.AvoidWorkflows = out
```

**Test**: Table-driven `TestSaveUserWorkflowPreferenceDedupAvoidWorkflows` — call twice with overlapping avoid lists, assert final page has no duplicates.

---

## A.4 — Advisor Filters Override Outcomes

**File**: `internal/learning/routing_advisor.go` (`Advise` function)

**Issue**: Outcomes with `PreferenceUsed=true` reflect user-pinned routing, not natural discovery. Including them in advisor statistics means the advisor can re-derive the user's own override as an "insight," creating a circular learning loop.

**Change**: In `Advise`, when iterating outcomes, skip records where `PreferenceUsed=true`. If doing so drops sample count below the advisor's minimum threshold, return `nil, nil` (no recommendation).

**Test**: `TestRoutingAdvisorIgnoresPreferenceUsedOutcomes` — 10 outcomes with `PreferenceUsed=true, Success=true`, advisor returns no preference (insufficient natural data).

---

## A.5 — Add `SetSource` to 5 Wiki Upsert Paths

**Files**:
- `internal/research/loop.go:264` → `SourceResearch`
- `internal/wiki/tool.go:310` → `SourceAgent`
- `internal/wiki/ingest.go:73` → `SourceIngest`
- `internal/wiki/ingest.go:171` → `SourceIngest`
- `internal/wiki/ingest.go:292` → `SourceIngest`

**First**: Verify the constants exist in `internal/wiki/provenance.go`. The audit says 5 sources are defined; confirm `SourceResearch`, `SourceAgent`, `SourceIngest` are among them. Add any that are missing.

**Pattern** (per call site):
```go
page.SetSource(wiki.SourceResearch, "research-loop", time.Now())  // or equivalent
if err := store.Upsert(ctx, page); err != nil { ... }
```

**Test**: For each file, add (or extend) a test that calls the path and asserts `page.Source()` returns the expected `wiki.Source` constant.

---

## A.6 — GPT-5.4 / 5.4-mini / 5.2 Cost Table

**File**: `internal/llm/usage.go:133`

**Change**: Add pricing entries. Verify actual rates from vendor docs before committing; do not guess.

Placeholder structure:
```go
"gpt-5.4":      {in: 3.0, out: 12.0},   // TODO: confirm from OpenAI pricing
"gpt-5.4-mini": {in: 0.25, out: 1.0},   // TODO: confirm
"gpt-5.2":      {in: 2.5, out: 10.0},   // TODO: confirm
```

Executor must replace TODO values with published rates (fetch via WebFetch if needed). Keep `gpt-4o/4o-mini` for backward compat.

**Test**: Extend `TestUsageEstimateCost` with cases for each new model.

---

## A.7 — LessonsNode Magic Number

**File**: `internal/prompt/lessons_node.go:61`

**Change**:
```go
const minTopicLessons = 3 // below this, fall back to global recent lessons for breadth

...
if len(lessons) < minTopicLessons {
    lessons = l.store.Recent(10)
}
```

Add a one-line comment on the constant explaining why 3 (threshold where topic-specific recall becomes statistically meaningful).

---

## A.8 — Slash Command Tests

**File**: `internal/telegram/shell_test.go` (extend existing)

**Coverage**: For each of `/remember`, `/forget`, `/override`, `/undo`:
- Happy path (well-formed input)
- Empty arguments
- Nonexistent target (forget unknown ID, undo completed task)
- Nil store safety (where applicable)
- State persistence verification (lesson actually saved, queue task actually cancelled)

Use table-driven tests with a fake store/queue. No live Telegram API calls.

**Acceptance**: `go test ./internal/telegram/` covers the new command paths (`go test -coverprofile` shows >0% for lines in `shell.go:362-441`).

---

## A.9 — Telegram Principal ProjectID

**Investigation first** (do before coding):

Find the Telegram task ingestion path that creates a `TaskPayload.Principal`. Likely in `cmd/elnath/cmd_daemon.go` or `internal/telegram/`. Confirm:
1. Does the Telegram-created principal have a real `ProjectID`? Or does it default to `"unknown"` / workspace hash?
2. If `"unknown"`, the A.1 fix alone will not activate outcome recording for Telegram traffic — we would still land on the daemon fallback principal's ProjectID.

**If broken**: Set `TaskPayload.Principal.ProjectID` to a stable identifier. Options:
- Hash of the Telegram chat_id + bot_token (stable per user)
- Explicit `project_id` field in the Telegram config
- Inherit from the last active CLI session for that user

**Test**: Simulate a Telegram-ingested payload and assert `sess.Principal.ProjectID` is real (not empty, not "unknown") after task execution.

---

## A.10 — Archive Stale Plan Artifact

**File**: `docs/superpowers/plans/2026-04-16-typed-event-bus.md` (untracked)

**Action**: Move to `docs/archive/2026-04-16-typed-event-bus-plan.md` (create `docs/archive/` if it doesn't exist). Commit the archive so the plan history is preserved.

Also: scan `docs/superpowers/plans/` for any other untracked artifacts from completed phases and archive likewise.

---

## A.11 — Verification

Run in order:

1. `cd /Users/stello/elnath && go build ./...` — no errors
2. `go test -race ./...` — all pass, no race
3. `make lint` — clean
4. **Manual dog-food probe**:
   - Ensure the daemon is stopped (or about to be restarted) so the new binary is used.
   - `make build && sudo launchctl stop com.elnath.daemon && sudo launchctl start com.elnath.daemon` (or `elnath run` directly).
   - Send a task (CLI or Telegram).
   - After completion: `ls -la ~/.elnath/data/outcomes.jsonl` → **file exists with ≥1 record**.
   - `elnath explain last` → shows the recorded outcome with non-empty ProjectID and a known intent/workflow.
   - `tail -5 ~/.elnath/daemon.log | grep -E "outcome|routing advisor"` → shows invocation logs.

**Acceptance criteria**: outcomes.jsonl has at least one entry; `elnath explain last` renders something meaningful; no test regressions; lint clean.

---

## Commit Strategy

One commit per task, conventional-commits style:
- `fix(runtime): record outcomes under session principal, not daemon fallback`  ← A.1 (already applied)
- `fix(runtime): record outcome on workflow error path`                         ← A.2
- `fix(wiki): dedup AvoidWorkflows in SaveUserWorkflowPreference`               ← A.3
- `fix(learning): advisor ignores PreferenceUsed=true outcomes`                 ← A.4
- `feat(wiki): annotate all upsert paths with provenance source`                ← A.5
- `feat(llm): add gpt-5.4/5.4-mini/5.2 cost table entries`                      ← A.6
- `refactor(prompt): extract LessonsNode topic threshold constant`              ← A.7
- `test(telegram): table-driven tests for Phase 6.7 slash commands`             ← A.8
- `fix(telegram): propagate real ProjectID on task principal`                   ← A.9 (if needed)
- `chore(docs): archive completed Phase 5.0 plan artifact`                      ← A.10

A.11 is not a commit — it's the gate that releases Stage A.

---

## Out of Scope for Stage A

- **F-5.2 Lesson Consolidation** → Stage B (separate plan).
- **Provenance enforcement** (reject upsert without source) → defer to Stage B or later; additive fixes in A.5 are sufficient for scorecard measurement.
- **Observability for flywheel health** (dashboard showing outcomes/hour, advisor activations) → Stage C or later.
