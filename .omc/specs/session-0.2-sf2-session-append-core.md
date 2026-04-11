# Session 0.2 — SF2 Session Append Core

**Branch**: `feat/telegram-redesign` (현재 브랜치 그대로)
**Phase**: 0.2 of Elnath Superiority Design v2.2 (43-session roadmap)
**Predecessor**: Session 0.1 SF1 Principal Key (DONE — `internal/identity` 패키지 + 8-layer Principal plumbing)
**Predicted complexity**: 2 sessions

---

## 1. Goal

같은 메시지가 두 번 enqueue 되거나 (Telegram 재전송, daemon stale recovery, IPC 재시도) worker가 같은 task 를 두 번 실행하더라도, 사용자 관점에서 **중복 메시지가 보이지 않는다**. 구체적으로:

1. **Enqueue dedup**: 동일 principal + 동일 prompt 가 최근 30s 이내에 pending/running 이면 새 row 를 만들지 않고 기존 `task_id` 를 반환.
2. **Session append idempotency**: `agent.Session.AppendMessage` / `AppendMessages` 가 동일 (session_id, content_hash) 메시지로 두 번 호출되어도 JSONL 과 SQLite 양쪽에 한 번만 기록.
3. **SQLite UPSERT**: `DBHistoryStore.Save` 가 DELETE+INSERT 를 stop 하고 message-level UPSERT 로 전환 (race 시 partial overwrite 방지).
4. **Delivery dedup**: `DeliveryRouter.Deliver` 가 동일 (sink, task_id) 조합에 대해 한 번만 sink 를 호출.

목표는 SF1 이 만든 `identity.Principal` plumbing 위에 **(principal, content) 기반 dedup 키**를 일관되게 흘려, Phase 1 의 LBB3 (Safe Parallel Executor) 와 LBB2 (Conversation Spine) 가 안정적인 substrate 위에서 작동하도록 하는 것.

---

## 2. Fixed Decisions (검증 대상 — line 단위로 spec 어김 여부 판정)

| # | Decision | Rationale |
|---|---|---|
| FD1 | **Idempotency key**: `sha256(principal.user_id \| principal.project_id \| prompt)[:16]` (16 hex chars). Surface 는 키에서 제외. | Surface 포함하면 같은 사람이 두 entry 점에서 동일 명령 보낼 때 dedup 안 됨. "한 번만 실행" 이 default. |
| FD2 | **Window**: 30 second sliding window. 30s 지난 동일 키는 새 task 로 enqueue. | 사용자가 의도적으로 반복 명령 보내는 경우 영원히 차단되면 안 됨. 30s 는 Telegram 재전송 / stale recovery 모두 커버. Follow-up review fix RF2 clarifies that the partial unique index remains authoritative while a task is pending/running; the 30s window is a fast-path lookup, not a live-task key release mechanism. |
| FD3 | **Enqueue 시그니처 변경**: `Enqueue(ctx, payload string) (int64, error)` → `Enqueue(ctx, payload string, idemKey string) (int64, bool, error)`. 두번째 반환값 `existed bool`. **모든** 호출처가 새 시그니처로 마이그레이션. payload 를 daemon 안에서 parse 해 키를 만들지 않음 — 호출처 (telegram/shell, daemon/handleSubmit, cmd_daemon) 가 명시적으로 키를 생성해 전달. | Daemon 안에서 parsing 하면 plain string payload 와 structured payload 가 분기되어 dedup semantics 모호해짐. 키 책임을 호출처로 밀어 명시화. |
| FD4 | **AppendMessage 메시지-레벨 idempotency**: `agent.Session` 에 `appliedHashes map[string]struct{}` in-memory set 추가. `LoadSession` 시 기존 메시지를 모두 hash 하여 set 채움. `AppendMessage` 는 hash 가 set 에 있으면 no-op + warn log. JSONL schema 는 변경 안 함. | LoadSession 의 raw message 에서 hash 계산 → backward compatible. |
| FD5 | **`DBHistoryStore.Save` UPSERT**: DELETE + INSERT 제거. `conversation_messages` 에 `content_hash TEXT NOT NULL DEFAULT ''` 컬럼 + `(session_id, content_hash) WHERE content_hash != ''` UNIQUE index. INSERT 는 `ON CONFLICT(session_id, content_hash) DO NOTHING`. 기존 row 는 backfill 함수로 채움. | DELETE+INSERT 는 race 시 partial overwrite + FTS rebuild 비용. 메시지는 append-only 가 본질. |
| FD6 | **Delivery dedup 테이블**: 신규 SQLite 테이블 `task_completion_deliveries(task_id INTEGER, sink_name TEXT, delivered_at INTEGER, PRIMARY KEY(task_id, sink_name))`. `DeliveryRouter.Deliver` 가 sink 별로 `INSERT OR IGNORE` 후 `RowsAffected == 0` 이면 skip + debug log. Sink 이름은 `String() string` interface assertion. | sink 별 dedup 이 필요한 이유: log sink 는 dedup 불필요 가능, telegram sink 는 사용자에게 보이므로 반드시 dedup. 명시적 sink 이름으로 분리. |
| FD7 | **Idempotency key 의 task_queue 영속화**: `task_queue` 에 `idempotency_key TEXT NOT NULL DEFAULT ''` 컬럼 + partial unique index `CREATE UNIQUE INDEX task_queue_idem_active ON task_queue(idempotency_key) WHERE status IN ('pending','running') AND idempotency_key != ''`. Enqueue 는 INSERT 시도 → UNIQUE 위반이면 SELECT 로 기존 row id 가져와 `(id, true, nil)` 반환. 30s window 는 SELECT 시 `created_at` 필터링. | Partial unique index 는 modernc.org/sqlite (SQLite 3.45+) 지원. status 가 done/failed 가 되면 자동으로 index 에서 빠져 "완료된 작업은 다시 enqueue 가능". |

---

## 3. Scope

### In
- `internal/identity/idempotency.go` 신설 — `func KeyFor(principal Principal, prompt string) string` (FD1).
- `internal/daemon/queue.go` — `Enqueue` 시그니처 변경 (FD3, FD7), schema 마이그레이션, `ensureColumns` 에 `idempotency_key` 추가, partial unique index 생성, time window dedup 로직, `Task` struct 에 `IdempotencyKey string` 필드, `Next/List/Get` SELECT 컬럼 보강.
- `internal/daemon/daemon.go:216-239` — `handleSubmit` 가 payload parse 후 `identity.KeyFor` 로 키 계산, `IPCResponse.Data` 에 `existed bool` 추가.
- `internal/daemon/delivery.go` — `Deliver` 가 dedup 테이블 사용 (FD6). `NewDeliveryRouter` 시그니처에 `*sql.DB` 추가, `task_completion_deliveries` 자동 생성.
- `internal/agent/session.go` — `appliedHashes` 필드 + `messageHash` helper + `AppendMessage` dedup 분기 (FD4). `LoadSession` 이 기존 메시지 hash 로 set 채움. `NewSession` 도 빈 set 초기화.
- `internal/conversation/history.go:54-146` — `Save` 를 UPSERT 로 재작성 (FD5), `InitSchema` 에 `content_hash` 컬럼 + UNIQUE index 추가, `backfillContentHash` 함수.
- `internal/telegram/shell.go:404, 433` — `EncodeTaskPayload` 후 `identity.KeyFor` 계산해 `queue.Enqueue` 에 전달. `existed == true` 면 사용자에게 "이미 처리 중입니다 (#%d)" 응답 + 신규 reaction/active task 등록 skip.
- `cmd/elnath/cmd_daemon.go:203` — `daemon submit` IPC 호출 시 동일하게 키 계산 + 응답의 `existed` 표시.

### Out (이 session 에서 하지 않음)
- LBB3 Safe Parallel Executor (Phase 1.1).
- LBB2 Conversation Spine outbound mirror (Phase 1.2).
- `result` 필드의 hash dedup (assistant 메시지). 본 session 은 user-driven append 만 dedup. Assistant tool result 는 매번 새 hash 가 자연스럽게 생기므로 unaffected.
- Time window 동적 설정. 30s hard-coded.
- Cross-process locking. SQLite 의 atomic INSERT 만 사용.
- DBHistoryStore Save 의 message truncation 시 stale row cleanup. (LBB2 가 처리)
- `task_completion_deliveries` 테이블의 nightly cleanup.

---

## 4. 파일별 변경 상세

### 4.1 `internal/identity/idempotency.go` (신설)

```go
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// KeyFor returns a 16-hex-char idempotency key derived from the principal
// identity (excluding surface) and the prompt content. Stable across CLI and
// Telegram entry points so the same user issuing the same command twice from
// different surfaces is deduped.
func KeyFor(p Principal, prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(p.UserID))
	h.Write([]byte{0})
	h.Write([]byte(p.ProjectID))
	h.Write([]byte{0})
	h.Write([]byte(strings.TrimSpace(prompt)))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:16]
}
```

테스트: `idempotency_test.go` — 동일 principal+prompt → 동일 키, surface 만 다름 → 동일 키, prompt 의 leading/trailing whitespace 무시, 빈 prompt → 빈 키.

### 4.2 `internal/daemon/queue.go`

**Schema**:
- `createQueueTable` 에 `idempotency_key TEXT NOT NULL DEFAULT ''` 컬럼.
- `ensureColumns` migrations 리스트에 `{name: "idempotency_key", sql: "ALTER TABLE task_queue ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT ''"}` 추가.
- `NewQueue` 안에서 `CREATE UNIQUE INDEX IF NOT EXISTS task_queue_idem_active ON task_queue(idempotency_key) WHERE status IN ('pending','running') AND idempotency_key != ''` 실행.

**시그니처**:
```go
const idempotencyWindow = 30 * time.Second

func (q *Queue) Enqueue(ctx context.Context, payload string, idemKey string) (int64, bool, error) {
	now := time.Now().UnixMilli()

	if idemKey != "" {
		cutoff := now - idempotencyWindow.Milliseconds()
		var existingID int64
		err := q.db.QueryRowContext(ctx, `
			SELECT id FROM task_queue
			WHERE idempotency_key = ?
			  AND status IN ('pending','running')
			  AND created_at >= ?
			ORDER BY created_at DESC
			LIMIT 1`,
			idemKey, cutoff,
		).Scan(&existingID)
		if err == nil {
			return existingID, true, nil
		}
		if err != sql.ErrNoRows {
			return 0, false, fmt.Errorf("queue: enqueue dedup lookup: %w", err)
		}
	}

	res, err := q.db.ExecContext(ctx, `
		INSERT INTO task_queue (payload, status, idempotency_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		payload, string(StatusPending), idemKey, now, now,
	)
	if err != nil {
		if idemKey != "" && isUniqueViolation(err) {
			var existingID int64
			if scanErr := q.db.QueryRowContext(ctx,
				`SELECT id FROM task_queue WHERE idempotency_key = ? AND status IN ('pending','running') ORDER BY created_at DESC LIMIT 1`,
				idemKey,
			).Scan(&existingID); scanErr == nil {
				return existingID, true, nil
			}
		}
		return 0, false, fmt.Errorf("queue: enqueue: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("queue: enqueue: last id: %w", err)
	}
	return id, false, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
```

`Next`, `List`, `Get` 의 SELECT 컬럼 목록에 `idempotency_key` 추가, scan 변수 + `Task.IdempotencyKey` 필드 채우기.

### 4.3 `internal/daemon/daemon.go:216-239`

```go
func (d *Daemon) handleSubmit(ctx context.Context, conn net.Conn, req IPCRequest) {
	var payload string
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("invalid payload: %v", err)})
			return
		}
	}
	if payload == "" {
		d.writeResponse(conn, IPCResponse{Err: "payload is required"})
		return
	}

	parsed := ParseTaskPayload(payload)
	principal := parsed.Principal
	if principal.IsZero() {
		principal = d.fallbackPrincipal
	}
	idemKey := identity.KeyFor(principal, parsed.Prompt)

	id, existed, err := d.queue.Enqueue(ctx, payload, idemKey)
	if err != nil {
		d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("enqueue: %v", err)})
		return
	}

	d.writeResponse(conn, IPCResponse{
		OK: true,
		Data: map[string]any{
			"task_id": id,
			"existed": existed,
		},
	})
}
```

### 4.4 `internal/daemon/delivery.go`

```go
const createDeliveryTable = `
CREATE TABLE IF NOT EXISTS task_completion_deliveries (
	task_id      INTEGER NOT NULL,
	sink_name    TEXT    NOT NULL,
	delivered_at INTEGER NOT NULL,
	PRIMARY KEY (task_id, sink_name)
);`

type DeliveryRouter struct {
	sinks  []CompletionSink
	db     *sql.DB
	logger *slog.Logger
}

func NewDeliveryRouter(db *sql.DB, logger *slog.Logger) (*DeliveryRouter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if db != nil {
		if _, err := db.Exec(createDeliveryTable); err != nil {
			return nil, fmt.Errorf("delivery: create table: %w", err)
		}
	}
	return &DeliveryRouter{db: db, logger: logger}, nil
}

func (r *DeliveryRouter) Deliver(ctx context.Context, completion TaskCompletion) error {
	if len(r.sinks) == 0 {
		return nil
	}
	var errs []error
	for _, sink := range r.sinks {
		sinkName := sinkNameOf(sink)
		if r.db != nil {
			res, err := r.db.ExecContext(ctx,
				`INSERT OR IGNORE INTO task_completion_deliveries(task_id, sink_name, delivered_at) VALUES (?, ?, ?)`,
				completion.TaskID, sinkName, time.Now().UnixMilli(),
			)
			if err != nil {
				r.logger.Error("delivery: dedup insert failed", "task_id", completion.TaskID, "sink", sinkName, "error", err)
			} else if n, _ := res.RowsAffected(); n == 0 {
				r.logger.Debug("delivery: skipped duplicate", "task_id", completion.TaskID, "sink", sinkName)
				continue
			}
		}
		if err := sink.NotifyCompletion(ctx, completion); err != nil {
			r.logger.Error("delivery: sink failed", "task_id", completion.TaskID, "sink", sinkName, "error", err)
			errs = append(errs, err)
		}
	}
	if len(errs) == len(r.sinks) {
		return fmt.Errorf("delivery: all sinks failed: %w", errors.Join(errs...))
	}
	return nil
}

func sinkNameOf(s CompletionSink) string {
	if named, ok := s.(interface{ String() string }); ok {
		return named.String()
	}
	return fmt.Sprintf("%T", s)
}
```

`NewDeliveryRouter` 호출처 (`cmd/elnath/cmd_daemon.go` 등) 에서 db 인자 추가. 테스트는 `db = nil` 로 dedup skip 가능.

### 4.5 `internal/agent/session.go`

```go
func messageHash(m llm.Message) string {
	data, _ := json.Marshal(m)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}
```

`Session` struct 에 `appliedHashes map[string]struct{}` 필드. `NewSession` / `LoadSession` 끝에 `s.appliedHashes = make(map[string]struct{})`. `LoadSession` 의 message scan 루프 안에서 `s.appliedHashes[messageHash(msg)] = struct{}{}`.

`AppendMessage`:
```go
func (s *Session) AppendMessage(msg llm.Message) error {
	hash := messageHash(msg)
	if s.appliedHashes == nil {
		s.appliedHashes = make(map[string]struct{})
	}
	if _, ok := s.appliedHashes[hash]; ok {
		if s.logger != nil {
			s.logger("session: skipped duplicate append", "session_id", s.ID, "hash", hash)
		}
		return nil
	}
	// ... existing marshal + write logic ...
	s.appliedHashes[hash] = struct{}{}
	s.Messages = append(s.Messages, msg)
	return nil
}
```

`AppendMessages` 의 secondary persister 호출은 그대로 유지.

**주의**: `messageHash` 가 결정적이려면 `json.Marshal(llm.Message)` 가 결정적이어야 함. `llm.Message` 의 필드 (Role string, Content blocks) 가 map 이면 비결정적. `llm.Message` 정의를 확인해 비결정적 필드가 있으면 `Role + content text join` 으로 hash 폴백.

### 4.6 `internal/conversation/history.go`

**Schema** (`InitSchema`):
```sql
CREATE TABLE IF NOT EXISTS conversation_messages (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id   TEXT NOT NULL REFERENCES conversations(id),
	role         TEXT NOT NULL,
	content      TEXT NOT NULL,
	content_hash TEXT NOT NULL DEFAULT '',
	created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_conv_msgs_session_hash
	ON conversation_messages(session_id, content_hash)
	WHERE content_hash != '';
```

기존 DB 마이그레이션:
- `ALTER TABLE conversation_messages ADD COLUMN content_hash TEXT NOT NULL DEFAULT ''` (PRAGMA table_info 로 컬럼 존재 검사 후).
- `backfillContentHash` 함수 — SELECT id, content WHERE content_hash = '' → sha256 → UPDATE.

```go
func backfillContentHash(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, content FROM conversation_messages WHERE content_hash = ''`)
	if err != nil {
		return fmt.Errorf("history: backfill query: %w", err)
	}
	type row struct{ id int64; content string }
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.content); err != nil {
			rows.Close()
			return fmt.Errorf("history: backfill scan: %w", err)
		}
		pending = append(pending, r)
	}
	rows.Close()
	for _, r := range pending {
		sum := sha256.Sum256([]byte(r.content))
		h := hex.EncodeToString(sum[:])[:16]
		if _, err := db.Exec(`UPDATE conversation_messages SET content_hash = ? WHERE id = ?`, h, r.id); err != nil {
			return fmt.Errorf("history: backfill update: %w", err)
		}
	}
	return nil
}
```

`InitSchema` 끝에서 컬럼 존재 보장 + `backfillContentHash` 호출 + UNIQUE index 생성.

**`Save` 재작성**:
```go
func (s *DBHistoryStore) Save(ctx context.Context, sessionID string, messages []llm.Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversations(id, created_at, updated_at)
		VALUES (?, strftime('%Y-%m-%dT%H:%M:%fZ','now'), strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(id) DO UPDATE SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	`, sessionID); err != nil {
		return fmt.Errorf("history: upsert conversation: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO conversation_messages(session_id, role, content, content_hash)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, content_hash) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("history: prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, m := range messages {
		data, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("history: marshal message: %w", err)
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])[:16]
		if _, err := stmt.ExecContext(ctx, sessionID, m.Role, string(data), hash); err != nil {
			return fmt.Errorf("history: insert message: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history: commit: %w", err)
	}
	if s.hasFTS {
		if err := s.rebuildFTS(ctx, sessionID); err != nil {
			s.logger.Warn("history: FTS rebuild failed", "session_id", sessionID, "error", err)
		}
	}
	return nil
}
```

DELETE 가 사라지므로 메시지가 줄어드는 케이스 (compression 후 truncated history 저장) 는 stale row 가 누적될 수 있음 — non-goal.

### 4.7 `internal/telegram/shell.go:404, 433`

```go
encoded := daemon.EncodeTaskPayload(payload)
parsed := daemon.ParseTaskPayload(encoded)
idemKey := identity.KeyFor(payload.Principal, parsed.Prompt)
id, existed, err := s.queue.Enqueue(ctx, encoded, idemKey)
if err != nil {
	return 0, err
}
if existed {
	s.logger.Info("telegram: enqueue deduplicated", "task_id", id, "user_id", payload.Principal.UserID)
	// 신규 reaction/active task 등록 skip. caller 가 "이미 처리 중 (#id)" 안내.
}
return id, nil
```

`shell.go:404` (chat path) 와 `shell.go:433` (task path) 양쪽 동일 패턴. `existed == true` 일 때 caller 가 사용자에게 "이미 처리 중입니다 (#%d)" 메시지 reply, `TrackUserMessage` 등 부수 등록 skip.

### 4.8 `cmd/elnath/cmd_daemon.go:203`

`daemon submit` IPC 응답의 `existed` 필드를 읽어 표준 출력에:
```
Task #42 already running (deduplicated)
```
또는
```
Task #42 enqueued
```

---

## 5. 마이그레이션 / Backward Compat

- `task_queue.idempotency_key` 컬럼은 `DEFAULT ''` 로 추가 → 기존 row 는 빈 문자열로 정상.
- Partial unique index 는 빈 문자열 row 를 제외 (`WHERE idempotency_key != ''`).
- `conversation_messages.content_hash` 는 `DEFAULT ''` 후 `backfillContentHash` 가 1회 채움.
- `task_completion_deliveries` 테이블은 새로 생성.
- `Enqueue` 시그니처 변경되므로 모든 테스트 (queue_test.go 다수 + alpha_gate_test.go + telegram/shell_test.go + cmd_task_test.go) 의 `q.Enqueue(ctx, "...")` 호출을 `q.Enqueue(ctx, "...", "")` 로 갱신. 빈 키 → dedup 비활성화 → 기존 동작 유지.
- `NewDeliveryRouter` 시그니처 변경되므로 호출처에서 db 인자 추가. 테스트는 `db = nil` 통과 가능.

---

## 6. Acceptance Criteria

빌드/정적분석:
- `make build` 통과.
- `go vet ./...` 통과.

테스트:
- `go test -race ./...` **17/17 패키지 통과** (현재 baseline).

신규 테스트 (반드시 포함):

**1. `internal/identity/idempotency_test.go`**:
- 동일 (user, project, prompt) → 동일 키.
- Surface 만 다름 → 동일 키.
- Whitespace 차이 → 동일 키.
- 빈 prompt → 빈 키.

**2. `internal/daemon/queue_test.go` 추가 케이스**:
- `TestEnqueueDedupReturnsExistingID`: 동일 키로 두 번 Enqueue → 두번째가 `(firstID, true, nil)` 반환, row 1개.
- `TestEnqueueDedupExpiresAfterWindow`: created_at 직접 조작 → 30s 초과 후 새 row.
- `TestEnqueueEmptyKeyAlwaysInserts`: idemKey="" 두 번 → row 2개.
- `TestEnqueueDedupAcrossPendingAndRunning`: 첫 task running 상태에서 동일 키 두번째 enqueue → existed=true.
- `TestEnqueueDedupAfterDoneAllowsNew`: 첫 task done → 동일 키 다시 enqueue → 새 row, existed=false.
- Race test: 100 goroutine 이 동일 키로 동시 Enqueue → 정확히 1 row.

**3. `internal/daemon/delivery_test.go` 추가 케이스**:
- `TestDeliverDedupSameTaskSameSink`: 동일 (taskID, sink) 두 번 Deliver → sink 한 번만 호출.
- `TestDeliverDifferentSinksBothCalled`: 두 sink 등록, 한 번 Deliver → 둘 다 호출.
- `TestDeliverNoDBSkipsDedup`: db=nil → 두 번 호출 모두 sink 도달.

**4. `internal/agent/session_test.go` 추가 케이스**:
- `TestAppendMessageDedupesIdenticalMessage`: 동일 메시지 두 번 AppendMessage → JSONL 한 줄, `s.Messages` 길이 1.
- `TestLoadSessionPopulatesAppliedHashes`: 메시지 3개 저장 후 LoadSession → 동일 메시지 다시 AppendMessage → no-op.

**5. `internal/conversation/history_test.go` 추가 케이스**:
- `TestSaveUpsertsMessages`: 같은 메시지 배열 두 번 Save → row 수 동일.
- `TestSaveAppendsNewMessages`: Save([m1,m2]) → Save([m1,m2,m3]) → row 3개.
- `TestBackfillContentHash`: 빈 hash row 삽입 → InitSchema 재호출 → hash 채워짐.

**6. `internal/telegram/shell_test.go` 보강**:
- 동일 user 가 동일 메시지 두 번 → queue row 1개, 두번째 호출이 "이미 처리 중" path.

수동 검증 (결과 보고에 명시):
- `elnath daemon` 실행 → `elnath daemon submit "test"` 두 번 빠르게 → 두번째에 "already running" 표시.
- Telegram 동일 메시지 두 번 → 두번째에서 같은 task ID 의 reaction 중복 없음.

---

## 7. Test Plan (실행 순서)

```bash
cd /Users/stello/elnath
go test -race ./internal/identity/...
go test -race ./internal/daemon/...
go test -race ./internal/agent/...
go test -race ./internal/conversation/...
go test -race ./internal/telegram/...
go test -race ./...
go vet ./...
make build
```

모든 단계가 통과해야 acceptance.

---

## 8. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Partial unique index 가 modernc.org/sqlite 에서 동작 안 함 | NewQueue 직후 INSERT 두 번 시도하는 self-test 로 확인. modernc.org/sqlite 는 SQLite 3.45+ 라 partial index 지원. |
| `Enqueue` 시그니처 변경 → 대규모 grep+rewrite | NO SEMANTIC SEARCH 원칙: `q.Enqueue(`, `queue.Enqueue(`, `.Enqueue(` 셋 모두 grep + 수동 검토. 사전 grep 결과 24 hit. |
| `messageHash` 가 비결정적 (`json.Marshal` map ordering) | `llm.Message` 정의 확인. 비결정적이면 `Role + content text` 필드만 hash. |
| `DBHistoryStore.Save` UPSERT 가 truncation 시 stale row 누적 | 본 session non-goal. Phase 1.2 LBB2 가 처리. |
| Delivery dedup 테이블 무한 증가 | non-goal. nightly cleanup 은 향후. |
| 사용자가 30s 후 동일 명령 의도적 재전송 | FD2 의 의도된 동작. |

---

## 9. Discipline (반드시 지킴)

1. **No stubs / hardcode / TODO**: 모든 path 가 실제 작동.
2. **No spec violation**: 7개 Fixed Decision 위반 금지.
3. **Read first**: 구현 시작 전 다음 파일 전부 읽기 — `internal/daemon/queue.go`, `internal/daemon/daemon.go`, `internal/daemon/delivery.go`, `internal/daemon/task_payload.go`, `internal/daemon/sink_log.go`, `internal/agent/session.go`, `internal/conversation/history.go`, `internal/conversation/manager.go`, `internal/telegram/shell.go`, `internal/telegram/sink.go`, `cmd/elnath/cmd_daemon.go`, `internal/identity/principal.go`, `internal/identity/resolve.go`, `internal/llm/types.go` (또는 `Message` 정의 위치).
4. **Phased**: 4.1 → 4.2 → 4.3 → 4.4 → 4.5 → 4.6 → 4.7 → 4.8 순서. 각 단계 후 `go build ./...` 확인.
5. **Verification**: 실제 `go test -race ./...`, `go vet ./...`, `make build` 명령 실행 + 출력 캡처. 캡처를 commit message 본문에 인용.
6. **Adjacent bugs**: 발견 시 같이 fix + 결과 보고서에 명시 (Session 0.1 패턴).
7. **Commit**: 작업 중 여러 checkpoint commit, 최종 단일 squash commit `feat(sf2): session append idempotency + queue dedup + delivery once-only`. 본문에 verification 명령 출력 인용.
8. **Result report**: 변경 파일 목록, 추가 테스트 목록, 발견한 인접 이슈, 모든 verification 명령어 출력.

---

## 10. Predecessor Context

Session 0.1 (SF1 Principal Key) 가 만든 substrate:
- `internal/identity/principal.go` — `Principal{UserID, ProjectID, Surface}` 타입, `LegacyPrincipal()`, `NewPrincipal()`, `IsZero()`.
- `internal/identity/resolve.go` — CLI flag / config / env / Telegram / git remote 에서 값 추출.
- `internal/daemon/task_payload.go` — `TaskPayload.Principal` 필드 + `EncodeTaskPayload` / `ParseTaskPayload`.
- `internal/agent/session.go` — `Session.Principal`, `NewSession(dataDir, principal)`, JSONL header 에 principal 영속화.
- `internal/conversation/manager.go` — `NewSessionWithPrincipal`.
- `internal/telegram/shell.go` — Telegram message → Principal 변환.
- `cmd/elnath/cmd_run.go` — `--principal` flag.
- `internal/daemon/daemon.go` — `WithFallbackPrincipal`, worker 가 task 당 principal 로깅.

이 substrate 는 본 session 의 핵심 입력. SF2 는 이 plumbing 을 dedup key 의 source 로 사용.
