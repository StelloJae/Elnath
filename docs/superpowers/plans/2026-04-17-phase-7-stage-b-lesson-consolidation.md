# Phase 7 Stage B — Lesson Consolidation (autoDream for Elnath)

> **For agentic workers:** Use superpowers:executing-plans or superpowers:subagent-driven-development to implement task-by-task.

**Date**: 2026-04-17
**Reference source**: Claude Code `/Users/stello/claude-code-src/src/services/autoDream/` (sole reference — Hermes has no analog, confirmed by explore-agent survey).
**Goal**: Complete the self-improvement flywheel by adding semantic consolidation of accumulated lessons, so knowledge compounds over time instead of just growing in count.

---

## Why this matters

Stage A activated outcome recording (outcomes.jsonl now populates). But the **lessons store** (`~/.elnath/data/lessons.jsonl` — 11 entries at Stage A landing) has only size-based rotation, no semantic merging. Without consolidation:

- Two lessons expressing the same insight compete rather than reinforce
- Prompt context bloats with near-duplicates
- "시간이 갈수록 더 똑똑해지는" claim rests only on routing preference learning, not on knowledge synthesis

Phase 7.2 Maturity Scorecard (next Stage) would measure a codebase that can *adapt routing* but cannot *compound knowledge*. Stage B closes that gap.

---

## Design decisions (grounded in Claude Code autoDream)

### Trigger (borrowed from `autoDream.ts:95-172`)

- **Time gate**: `now - lastConsolidatedAt >= minInterval` (default 24h)
- **Session gate**: `count(sessions touched since lastConsolidatedAt) >= minSessions` (default 5)
- **Lock**: mtime-based CAS on `.consolidate-lock` file with PID holder + stale-PID reclaim
- **Scan throttle**: when session-gate blocks, don't re-check for 10min

Elnath-specific entry point: the existing `wiki/boot/` ambient autonomy scheduler (Phase 5.2) already runs cron-style tasks. Wire the consolidation trigger there rather than building new scheduling.

### Work (adapted from Claude Code's 4-phase prompt + Elnath's Consolidator prevalence gate)

1. **Orient**: load last N lessons + prior synthesis pages; build consolidator's view of world
2. **Gather**: group lessons by topic signature (already present in `lesson.topic` field — see `lessons.jsonl`)
3. **Consolidate**: for each cluster with prevalence ≥M across ≥K sessions, emit one synthesis lesson that supersedes the cluster
4. **Prune**: mark superseded raw lessons as `status: consolidated` (not delete — preserve audit trail); update lesson index

Uses a forked agent (single workflow, read-only tools except `wiki_write` for synthesis pages). LLM is the existing lesson-extraction provider (Codex OAuth in production).

### Output (Elnath adaptation)

Claude Code writes markdown to memory dir. Elnath writes:

- `wiki/synthesis/<topic-slug>/<YYYY-MM-DD>.md` — synthesis pages with `wiki.SourceConsolidation` provenance (new source constant)
- `~/.elnath/data/lessons.jsonl` — raw lessons get `SupersededBy: <synthesis-id>` field appended; `Rotate` no longer drops them while referenced
- `~/.elnath/data/consolidation_state.json` — tracks `lastConsolidatedAt`, `sessionsTouched[]`, consolidation history

### What NOT to port

- Claude Code's autoDream runs in-process as a TypeScript agent. Elnath ports this to Go but **does not port the read-only-bash tool restriction** — lesson consolidation doesn't need bash at all (pure wiki + lesson store).
- Markdown-index manipulation (`FileTableOfContents`). Elnath's wiki already has FTS5 + frontmatter; no separate index needed.

---

## File Structure

### New Files

| File | Responsibility |
|------|---------------|
| `internal/learning/consolidator.go` | `LessonConsolidator` struct, `Run(ctx)` entry point |
| `internal/learning/consolidator_gate.go` | Time/session gate + lock management |
| `internal/learning/consolidator_prompt.go` | 4-phase prompt builder |
| `internal/learning/consolidator_test.go` | Table-driven tests for gate + clustering |
| `wiki/boot/lesson-consolidation.md` | Ambient boot task definition (scheduled trigger) |

### Modified Files

| File | Change |
|------|--------|
| `internal/learning/store.go` | Add `SupersededBy` field to `Lesson` struct; `Rotate` honors it (keep superseded lessons referenced by active synthesis) |
| `internal/learning/store.go` | New `MarkSuperseded(ids, synthesisID)` method |
| `internal/wiki/provenance.go` | Add `SourceConsolidation = "consolidation"` constant |
| `internal/daemon/scheduler.go` (or wiki/boot wiring) | Register `LessonConsolidator.Run` as a scheduled task |
| `cmd/elnath/cmd_debug.go` | `elnath debug consolidation` subcommand: show last run, gate status, pending cluster count |

---

## Tasks

### B.1 — Gate + Lock (foundation) ✅

- [x] Create `internal/learning/consolidator_gate.go`
- [x] `Gate.ShouldRun(now time.Time) (bool, reason string)` — combines time/session/lock checks
- [x] `Gate.Acquire() (release func(), err error)` — mtime-CAS lock with PID + stale reclaim
- [x] Test: concurrent `Acquire` — exactly one succeeds; stale PID (non-existent process) reclaims
- [x] Test: stuck-lock recovery (lock file mtime > stuckAfter → reclaim + log warning)

**Why first**: Everything else runs under the gate. Land this standalone to catch lock bugs early.

**Landed**: commit `c80d629 feat(learning): add consolidation gate with mtime-CAS lock`. O_EXCL atomic create gives single-winner semantics; empty/unparseable lock bodies with fresh mtime are treated as alive (conservative) to avoid reclaiming a file another process just created mid-acquire.

### B.2 — Lesson Clustering ⏭ Skipped

Reason (decided 2026-04-17 after B.1):
- Plan's own note flagged B.2 as a "cheap literal-duplicate pre-filter" with B.3 doing the real semantic merge.
- Real data shows `lesson.topic` is the full user-input prefix (`"Create /tmp/elnath-f5-v2/hello.txt…"`) — unique per session, so exact-match clustering produces mostly 1-element groups.
- `Lesson` struct has no `SessionID` field, so the "distinct(session_ids) ≥ 2" threshold cannot be evaluated without a schema change that only B.2 would use.
- Claude Code autoDream (the reference) does not pre-cluster — it passes a session list to the LLM and lets the model do semantic grouping.

**Absorbed into B.5**: orchestration selects a recent-N window of lessons and passes them to the consolidation prompt. No separate clustering module.

### B.3 — Consolidation Prompt + Parser ✅

- [x] `consolidator_prompt.go`: 4-phase prompt builder (orient/gather/consolidate/prune)
- [x] Inject: recent raw lessons, prior synthesis pages, session context
- [x] Output format: JSON `{ "syntheses": [{ synthesis_text, topic_tags, superseded_lesson_ids, confidence }] }`
- [x] `ParseConsolidationResponse` fails closed on malformed JSON; silently drops items with missing fields, invalid confidence, fewer than 2 superseded IDs, or hallucinated lesson IDs
- [x] Tests: prompt contains each phase header + lesson/synthesis IDs; parser table-drives 10+ validation cases

**Scope change**: B.3 no longer makes the LLM call directly — that is the orchestration layer's job (B.5). B.3 owns prompt construction and response parsing only, which keeps the unit tests LLM-free.

### B.4 — Synthesis Persistence ✅

- [x] Extend `Lesson` with `SupersededBy string` JSON field (omitempty) — commit `fd77cd2`
- [x] `Store.MarkSuperseded(ids, synthesisID) (int, error)` — atomic rewrite, first-write-wins, idempotent
- [x] `RotateOpts.KeepFn` + `KeepSuperseded` predicate — caller composes with wiki check in B.5
- [x] `wiki.SourceConsolidation` constant + `BuildSynthesisPage` + `SynthesisID` / `SynthesisSlug` helpers
- [x] Unit tests: MarkSuperseded behaviour (update/ignore/idempotent/first-link-wins); Rotate preservation; synthesis slug & path; page round-trip through `Store.Create` → `Store.Read`

The "consolidation run ⇒ synthesis page + superseded marks" integration test lives in B.5 where the orchestrator wires these primitives together with the Gate + prompt/parser from B.1 + B.3.

Split into two commits: `fd77cd2` (learning side) and the follow-up wiki commit.

### B.5 — Consolidator Orchestration

- [ ] `consolidator.go`: `Run(ctx)` orchestrates gate → select recent lessons → prompt → LLM call → parse → persist → state update
- [ ] Recent-lesson window replaces B.2's abandoned clustering (N most recent, capped by prompt budget)
- [ ] Writes `~/.elnath/data/consolidation_state.json` on each run (success or failure)
- [ ] On failure: release lock, log error, do NOT advance `lastConsolidatedAt` (so next run retries)
- [ ] Integration test: seeded lessons.jsonl + stub LLM → full run → assert synthesis page + state + superseded marks

### B.6 — Scheduler Wiring

- [ ] Create `wiki/boot/lesson-consolidation.md` ambient boot task (cron: daily 04:00)
- [ ] Verify Phase 5.2 ambient scheduler picks it up
- [ ] Manual trigger via `elnath debug consolidation run` (for testing without waiting for schedule)

### B.7 — Debug + Transparency

- [ ] `cmd_debug.go`: `elnath debug consolidation` subcommand
  - `show` (default): last run time, gate status (pass/block reason), recent-lesson window size
  - `run`: force-run ignoring gates (with `--force` confirmation)
  - `history [n]`: last N consolidation runs with stats (superseded count, synthesis count)
- [ ] Golden-output tests

### B.8 — Verification

- [ ] `go build ./...`
- [ ] `go test -race ./...` — all packages pass
- [ ] `make lint` — clean
- [ ] **Live probe**: `elnath debug consolidation run --force` on current lessons.jsonl (11 entries) → assert at least one synthesis page created, raw lessons get `superseded_by` field, state.json updated
- [ ] **Follow-up probe** 24h later (ambient cycle): assert natural schedule fires without manual intervention

---

## Commit Strategy (Stage A precedent)

- `feat(learning): add consolidation gate with mtime-CAS lock` (B.1) — **landed c80d629**
- ~~`feat(learning): cluster lessons by topic signature` (B.2)~~ — skipped, absorbed into B.5
- `feat(learning): build 4-phase consolidation prompt` (B.3)
- `feat(learning,wiki): persist synthesis pages with consolidation provenance` (B.4)
- `feat(learning): orchestrate full consolidation run with state tracking` (B.5)
- `feat(boot): schedule daily lesson consolidation via ambient autonomy` (B.6)
- `feat(cli): add elnath debug consolidation commands` (B.7)

6 commits remaining after B.1. B.8 is the gate, not a commit.

---

## Out of Scope

- **Lesson deduplication across projects**: Stage B consolidates within one project; cross-project synthesis is future work (may never be needed).
- **Reinforcement loop into persona**: existing `persona_delta` handling stays as is. Consolidation affects only lesson surface, not persona parameters.
- **Multi-model orchestration**: uses the single configured lesson-extraction provider. Future: maybe run consolidation on Opus while routine work uses Sonnet/Codex.

---

## Acceptance (Stage B complete when all true)

1. `outcomes.jsonl` + `lessons.jsonl` both populate (Stage A covers outcomes; Stage B doesn't regress)
2. At least one consolidation run produces a valid synthesis page in wiki
3. Superseded lessons are marked (not deleted); `Rotate` preserves referenced ones
4. Ambient scheduler fires consolidation without manual kick
5. `elnath debug consolidation show` renders meaningful state
6. All tests pass with `-race`
7. Phase 7.2 Scorecard (next stage) has substantive evidence for the "knowledge compounds over time" axis — not just routing adaptation
