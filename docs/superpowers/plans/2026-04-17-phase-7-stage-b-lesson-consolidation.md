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

### B.1 — Gate + Lock (foundation)

- [ ] Create `internal/learning/consolidator_gate.go`
- [ ] `Gate.ShouldRun(now time.Time) (bool, reason string)` — combines time/session/lock checks
- [ ] `Gate.Acquire() (release func(), err error)` — mtime-CAS lock with PID + stale reclaim
- [ ] Test: concurrent `Acquire` — exactly one succeeds; stale PID (non-existent process) reclaims
- [ ] Test: stuck-lock recovery (lock file mtime > stuckAfter → reclaim + log warning)

**Why first**: Everything else runs under the gate. Land this standalone to catch lock bugs early.

### B.2 — Lesson Clustering

- [ ] Add clustering logic to `consolidator.go`: given `[]Lesson`, return `[]Cluster` where each cluster shares a topic signature
- [ ] Signature: normalize `lesson.topic` (lowercase, strip punctuation, token sort) then exact-match group
- [ ] Threshold: cluster qualifies for consolidation when `len(lessons) >= minPrevalence (3)` AND `distinct(session_ids) >= minSessionSpread (2)`
- [ ] Test: 6 lessons across 4 sessions with 2 topics → 2 clusters, one qualifies
- [ ] Test: 10 lessons same topic same session → no qualifying cluster (session spread fails)

**Note**: The existing `lesson.topic` field is the full user input prefix (see actual data: `"Create /tmp/elnath-f5-v2/hello.txt..."`). This is too specific for clustering. The prompt-builder (B.3) asks the LLM to normalize topics semantically; clustering alone won't produce meaningful groups. Document this: B.2 groups only *literal-duplicate* topics as a cheap pre-filter; B.3 does the real semantic merge.

### B.3 — Consolidation Prompt + LLM Call

- [ ] `consolidator_prompt.go`: build 4-phase prompt (orient/gather/consolidate/prune)
- [ ] Inject: recent raw lessons, prior synthesis pages, session context (when consolidations happened before)
- [ ] Expected LLM output format: JSON array of `{synthesis_text, topic_tags, superseded_lesson_ids, confidence}`
- [ ] Validator: parse LLM output, fail closed on malformed JSON (do not write garbage synthesis)
- [ ] Test: golden-prompt test with fixed input → assert prompt structure contains each phase header

### B.4 — Synthesis Persistence

- [ ] Extend `internal/learning/store.go`: add `SupersededBy string` JSON field on `Lesson`
- [ ] `MarkSuperseded(ids []string, synthesisID string) error` — atomic UPSERT (temp-file + rename pattern, match existing outcome_store)
- [ ] `Rotate` keeps `SupersededBy != ""` lessons when their synthesis page is still in wiki
- [ ] Write synthesis to wiki: `wiki/synthesis/<slug>/<date>.md` with `wiki.SourceConsolidation`
- [ ] Test: consolidation run → 3 raw lessons marked superseded + 1 synthesis page exists with expected content
- [ ] Test: Rotate after consolidation preserves superseded-but-referenced lessons

### B.5 — Consolidator Orchestration

- [ ] `consolidator.go`: `Run(ctx)` orchestrates gate → cluster → prompt → persist → state update
- [ ] Writes `~/.elnath/data/consolidation_state.json` on each run (success or failure)
- [ ] On failure: release lock, log error, do NOT advance `lastConsolidatedAt` (so next run retries)
- [ ] Integration test: seeded lessons.jsonl + stub LLM → full run → assert synthesis page + state + superseded marks

### B.6 — Scheduler Wiring

- [ ] Create `wiki/boot/lesson-consolidation.md` ambient boot task (cron: daily 04:00)
- [ ] Verify Phase 5.2 ambient scheduler picks it up
- [ ] Manual trigger via `elnath debug consolidation run` (for testing without waiting for schedule)

### B.7 — Debug + Transparency

- [ ] `cmd_debug.go`: `elnath debug consolidation` subcommand
  - `show` (default): last run time, gate status (pass/block reason), cluster candidate count
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

- `feat(learning): add consolidation gate with mtime-CAS lock` (B.1)
- `feat(learning): cluster lessons by topic signature` (B.2)
- `feat(learning): build 4-phase consolidation prompt` (B.3)
- `feat(learning,wiki): persist synthesis pages with consolidation provenance` (B.4)
- `feat(learning): orchestrate full consolidation run with state tracking` (B.5)
- `feat(boot): schedule daily lesson consolidation via ambient autonomy` (B.6)
- `feat(cli): add elnath debug consolidation commands` (B.7)

7 commits. B.8 is the gate, not a commit.

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
