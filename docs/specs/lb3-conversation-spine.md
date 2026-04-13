# LB3 Conversation Spine — Implementation Spec

**Status**: READY FOR IMPLEMENTATION
**Phase**: A Foundation-2
**Branch**: `feat/telegram-redesign`
**Prerequisite**: LB4 Safe Parallel Executor (already done in SF3)
**Estimated scope**: ~8 files, 1 session

## Acceptance Criteria (from Superiority Design v2.2)

1. Telegram answer is visible from CLI `--continue`
2. Session auto-saved to wiki on completion

## Current State

### What Already Works
- JSONL sessions are canonical, written to `{dataDir}/sessions/{id}.jsonl`
- `Spine` forwards daemon task completions to wiki ingest (`conversation/spine.go`)
- CLI `--continue` calls `LoadLatestSession()` which finds latest JSONL by file mtime
- Telegram `ChatSessionBinder` tracks `chatID|userID → sessionID` for follow-ups
- Daemon `TaskPayload` carries `SessionID` for session binding

### What's Broken / Missing

**Gap 1: LoadLatestSession() is surface-blind**
- `conversation/manager.go:130-158` sorts JSONL files by mtime without Principal matching
- Result: CLI `--continue` might resume a different user's session or a session from a different project
- For single-user (current), mtime ordering works. For multi-user daemon, it's unsafe.

**Gap 2: No unified session index**
- CLI uses JSONL file scan (O(n) directory walk)
- Telegram uses `ChatSessionBinder` JSON file (separate tracking)
- `DBHistoryStore` exists but is optional and not used for resume (JSONL is canonical)
- No way to query "latest session for (UserID, ProjectID) across all surfaces"

**Gap 3: CLI --continue doesn't validate Principal**
- `LoadLatestSession()` returns the newest file regardless of who created it
- Should filter by current user's Principal to prevent cross-user session leaks

**Gap 4: Session header doesn't record surface transitions**
- When CLI resumes a Telegram session, no record of the surface switch
- Wiki ingest shows original Principal only, not resume chain

## Implementation Plan

### P1: Session Index in Manager (conversation/manager.go)

Add a `SessionIndex` method that reads JSONL headers efficiently:

```go
// SessionIndex returns metadata for all sessions, sorted by UpdatedAt DESC.
// Reads only the first line (header) of each JSONL file + file stat for mtime.
func (m *Manager) SessionIndex() ([]SessionMeta, error)

type SessionMeta struct {
    ID        string
    CreatedAt time.Time
    UpdatedAt time.Time  // from file mtime
    Principal identity.Principal
    MsgCount  int       // from file line count - 1 (cheap: wc -l equivalent)
}
```

This replaces the current `ListSessionFiles()` in `agent/session.go:283-331` which already does most of this work but doesn't expose Principal from the JSONL header.

**Changes**:
- `agent/session.go`: Add `ReadSessionHeader(path string) (*sessionHeader, error)` — reads first line only
- `conversation/manager.go`: Add `SessionIndex()` using `ReadSessionHeader` + file stat
- `conversation/manager.go`: Modify `LoadLatestSession()` to accept optional `identity.Principal` filter

### P2: Principal-Aware Resume (conversation/manager.go)

Change `LoadLatestSession()` signature:

```go
// LoadLatestSession loads the most recently updated session.
// If principal is non-zero, only sessions matching (UserID, ProjectID) are considered.
// Surface is NOT filtered — this enables cross-surface resume.
func (m *Manager) LoadLatestSession(principal ...identity.Principal) (*agent.Session, error)
```

Implementation:
1. Call `SessionIndex()`
2. If principal provided, filter by `UserID == p.UserID && ProjectID == p.ProjectID`
3. Return first match (already sorted by UpdatedAt DESC)
4. Surface is intentionally NOT part of the filter — this is what enables Telegram → CLI resume

**Caller changes**:
- `cmd/elnath/cmd_run.go:154-160`: Pass CLI principal to `LoadLatestSession(principal)`

### P3: Session Header Principal Persistence (agent/session.go)

Currently `sessionHeader` stores Principal. Verify:
1. `NewSession()` writes Principal to header ✅ (already done at line 70-87)
2. Telegram sessions also write Principal ✅ (daemon task runner sets Principal in payload)
3. `ReadSessionHeader()` can extract Principal from existing sessions

**No code change needed if header already stores Principal** — just need the new `ReadSessionHeader` reader from P1.

### P4: Surface Transition Tracking (agent/session.go)

Add optional resume event to session JSONL:

```go
type sessionResumeEvent struct {
    Type      string             `json:"type"`      // "resume"
    Surface   string             `json:"surface"`
    Principal identity.Principal `json:"principal"`
    At        time.Time          `json:"at"`
}
```

When a session is loaded for resume (not fresh creation):
- Append a resume event line to the JSONL: `{"type":"resume","surface":"cli","principal":{...},"at":"..."}`
- This is a metadata-only line, not a message — existing `LoadSession()` should skip non-message lines

**Changes**:
- `agent/session.go`: Add `RecordResume(principal)` method
- `agent/session.go`: `LoadSession()` should tolerate and skip `{"type":"resume",...}` lines (or any line with a `"type"` field that isn't a message)
- `cmd/elnath/cmd_run.go`: Call `sess.RecordResume(principal)` after successful `LoadSession()` or `LoadLatestSession()` 
- `internal/telegram/shell.go`: Call `sess.RecordResume(principal)` on follow-up resume

### P5: Wiki Ingest Enhancement (wiki/ingest.go)

Extend `IngestEvent` to carry resume chain:

```go
type IngestEvent struct {
    // ... existing fields ...
    Resumes []ResumeRecord `json:"resumes,omitempty"`
}

type ResumeRecord struct {
    Surface   string    `json:"surface"`
    Principal string    `json:"principal"` // SurfaceIdentity()
    At        time.Time `json:"at"`
}
```

Wiki page template adds resume history if present:
```markdown
## Session Metadata
- **Session ID**: {id}
- **Created by**: telegram:12345
- **Resumed by**: cli:stello@host (2026-04-13T10:30:00)
```

**Changes**:
- `wiki/ingest.go`: Read resume events from session JSONL when building IngestEvent
- `wiki/ingest.go`: Render resume history in page template

### P6: Deprecate ChatSessionBinder (telegram/binding.go)

After P1-P2, the unified `SessionIndex` + Principal-aware `LoadLatestSession` makes `ChatSessionBinder` redundant. But this is a **follow-up cleanup**, not part of the core LB3 implementation. Keep binder working for now.

**No change in this phase** — just a note for future cleanup.

## File Change Summary

| File | Change | LOC Est |
|------|--------|---------|
| `internal/agent/session.go` | `ReadSessionHeader()`, `RecordResume()`, skip resume lines in `LoadSession()` | ~60 |
| `internal/conversation/manager.go` | `SessionIndex()`, Principal-aware `LoadLatestSession()` | ~50 |
| `cmd/elnath/cmd_run.go` | Pass principal to LoadLatestSession, call RecordResume | ~10 |
| `internal/telegram/shell.go` | Call RecordResume on follow-up | ~5 |
| `internal/wiki/ingest.go` | ResumeRecord, render resume history | ~30 |
| **Tests** | | |
| `internal/agent/session_test.go` | ReadSessionHeader, RecordResume, LoadSession skips resume lines | ~60 |
| `internal/conversation/manager_test.go` | SessionIndex, Principal-aware LoadLatestSession | ~80 |
| `internal/wiki/ingest_test.go` | Resume history rendering | ~30 |

**Total**: ~325 LOC across 8 files

## Test Plan

### Unit Tests
1. `TestReadSessionHeader` — reads header from valid JSONL, returns Principal
2. `TestReadSessionHeader_EmptyFile` — returns error for empty/malformed file
3. `TestRecordResume` — appends resume event line to JSONL
4. `TestLoadSession_SkipsResumeLines` — LoadSession ignores resume events, only loads messages
5. `TestSessionIndex` — returns sorted metadata for multiple sessions
6. `TestLoadLatestSession_PrincipalFilter` — CLI principal matches Telegram session's (UserID, ProjectID)
7. `TestLoadLatestSession_CrossSurface` — Telegram session resumable from CLI (different Surface, same UserID+ProjectID)
8. `TestLoadLatestSession_DifferentUser` — doesn't return other user's session

### Integration Test (manual or script)
1. Start daemon with Telegram
2. Send task via Telegram → session created
3. Run `elnath run --continue` → should resume the Telegram session
4. Verify wiki page shows both surfaces in resume history

## Key Design Decisions

1. **Surface is NOT a filter for resume** — intentional. Cross-surface resume is the whole point of LB3. Only UserID + ProjectID must match.

2. **JSONL remains canonical** — no new SQLite tables for session index. `SessionIndex()` reads JSONL headers directly. O(n) directory scan is fine for hundreds of sessions; optimize with SQLite cache only if it becomes a bottleneck.

3. **Resume events are JSONL metadata lines** — not messages. They don't pollute the message array. LoadSession skips them. This is extensible for future metadata (compression markers, context snapshots, etc).

4. **ChatSessionBinder preserved** — deprecation is a follow-up. LB3 adds a better mechanism alongside, doesn't break existing Telegram flow.

## OpenCode Delegation Prompt

```
Implement LB3 Conversation Spine for the Elnath project (/Users/stello/elnath/).

Read the spec at docs/specs/lb3-conversation-spine.md FIRST. It contains the full
implementation plan with file paths, function signatures, and test requirements.

Implementation order:
1. P1: ReadSessionHeader + SessionIndex (agent/session.go, conversation/manager.go)
2. P2: Principal-aware LoadLatestSession (conversation/manager.go, cmd/elnath/cmd_run.go)
3. P4: RecordResume + skip resume lines in LoadSession (agent/session.go)
4. P5: Wiki ingest resume history (wiki/ingest.go)
5. Tests for each step

Key constraints:
- JSONL is the canonical session store. No new SQLite tables.
- Surface is NOT a filter for resume. Only UserID + ProjectID match.
- Resume events are metadata lines in JSONL, not messages.
- Run `go test -race ./internal/agent/ ./internal/conversation/ ./internal/wiki/` after each step.
- Run `go test -race ./...` at the end. All 17 packages must pass.
```
