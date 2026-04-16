package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/core"
)

const createQueueTable = `
CREATE TABLE IF NOT EXISTS task_queue (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	payload      TEXT    NOT NULL,
	idempotency_key TEXT NOT NULL DEFAULT '',
	session_id   TEXT    NOT NULL DEFAULT '',
	status       TEXT    NOT NULL DEFAULT 'pending',
	progress     TEXT    NOT NULL DEFAULT '',
	summary      TEXT    NOT NULL DEFAULT '',
	result       TEXT    NOT NULL DEFAULT '',
	completion   TEXT    NOT NULL DEFAULT '',
	timeout_class TEXT   NOT NULL DEFAULT '',
	idle_timeout_count INTEGER NOT NULL DEFAULT 0,
	active_timeout_count INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL DEFAULT 0,
	started_at   INTEGER NOT NULL DEFAULT 0,
	completed_at INTEGER NOT NULL DEFAULT 0
);
`

const defaultStaleTimeout = 5 * time.Minute
const defaultMaxRecoveries = 3

// idempotencyWindow controls Enqueue's cheap initial lookup only. The
// authoritative dedup gate is task_queue_idem_active, the partial unique index
// that blocks duplicate inserts while a task is pending or running. The window
// exists to avoid extra work for fast repeat submissions; stale active rows are
// cleaned up by RecoverStale instead of mutating live idempotency keys.
const idempotencyWindow = 30 * time.Second

// TaskStatus represents the lifecycle state of a queued task.
type TaskStatus string

const (
	StatusPending TaskStatus = "pending"
	StatusRunning TaskStatus = "running"
	StatusDone    TaskStatus = "done"
	StatusFailed  TaskStatus = "failed"
)

// TimeoutClass captures why a stale running task was recovered.
type TimeoutClass string

const (
	TimeoutClassNone            TimeoutClass = ""
	TimeoutClassIdle            TimeoutClass = "idle"
	TimeoutClassActiveButKilled TimeoutClass = "active_but_killed"
)

// TaskCompletion is the durable, UI-safe completion contract for a finished task.
type TaskCompletion struct {
	TaskID      int64
	SessionID   string
	Summary     string
	Status      TaskStatus
	CreatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
}

func (c TaskCompletion) Duration() time.Duration {
	if c.StartedAt.IsZero() || c.CompletedAt.IsZero() || c.CompletedAt.Before(c.StartedAt) {
		return 0
	}
	return c.CompletedAt.Sub(c.StartedAt)
}

// Task is a single unit of work in the queue.
type Task struct {
	ID                 int64
	Payload            string
	IdempotencyKey     string
	SessionID          string
	Status             TaskStatus
	Progress           string
	Summary            string
	Result             string
	Completion         *TaskCompletion
	TimeoutClass       TimeoutClass
	IdleTimeoutCount   int
	ActiveTimeoutCount int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	StartedAt          time.Time
	CompletedAt        time.Time
}

// TimeoutMetrics aggregates recovered-task timeout classifications.
type TimeoutMetrics struct {
	IdleRecoveries            int
	ActiveButKilledRecoveries int
	FalseTimeoutRate          float64
}

// Queue manages a SQLite-backed FIFO task queue.
type Queue struct {
	db *sql.DB
}

// NewQueue creates the task_queue table if it does not exist and
// recovers any tasks left in 'running' state from a previous crash.
func NewQueue(db *sql.DB) (*Queue, error) {
	if _, err := db.Exec(createQueueTable); err != nil {
		return nil, fmt.Errorf("queue: init schema: %w", err)
	}

	q := &Queue{db: db}
	if err := q.ensureColumns(context.Background()); err != nil {
		return nil, fmt.Errorf("queue: ensure schema columns: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS task_queue_status ON task_queue(status)`,
		`CREATE INDEX IF NOT EXISTS task_queue_created ON task_queue(created_at)`,
		`CREATE INDEX IF NOT EXISTS task_queue_session ON task_queue(session_id)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return nil, fmt.Errorf("queue: create index: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS task_queue_idem_active ON task_queue(idempotency_key) WHERE status IN ('pending','running') AND idempotency_key != ''`); err != nil {
		return nil, fmt.Errorf("queue: create idempotency index: %w", err)
	}

	if _, err := q.RecoverStale(context.Background(), defaultStaleTimeout, defaultMaxRecoveries); err != nil {
		return nil, fmt.Errorf("queue: recover stale: %w", err)
	}

	return q, nil
}

// Enqueue inserts a new pending task and returns its ID.
func (q *Queue) Enqueue(ctx context.Context, payload string, idemKey string) (int64, bool, error) {
	now := time.Now().UnixMilli()
	idemKey = strings.TrimSpace(idemKey)
	if idemKey != "" {
		cutoff := now - idempotencyWindow.Milliseconds()
		existingID, err := q.lookupActiveTaskID(ctx, idemKey, cutoff)
		if err != nil {
			return 0, false, fmt.Errorf("queue: enqueue dedup lookup: %w", err)
		}
		if existingID != 0 {
			return existingID, true, nil
		}
	}

	res, err := q.insertTask(ctx, payload, idemKey, now)
	if err != nil {
		if idemKey != "" && isUniqueViolation(err) {
			existingID, lookupErr := q.lookupActiveTaskIDAny(ctx, idemKey)
			if lookupErr != nil {
				return 0, false, fmt.Errorf("queue: enqueue unique lookup: %w", lookupErr)
			}
			if existingID != 0 {
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

// Next atomically claims the oldest pending task by transitioning it to
// running. Returns nil if the queue is empty.
func (q *Queue) Next(ctx context.Context) (*Task, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("queue: next: begin tx: %w", err)
	}
	defer tx.Rollback()

	var t Task
	var statusStr string
	var completionJSON string
	var timeoutClass string
	var createdMs, updatedMs, startedMs, completedMs int64

	err = tx.QueryRowContext(ctx, `
		SELECT id, payload, idempotency_key, session_id, status, progress, summary, result, completion, timeout_class, idle_timeout_count, active_timeout_count, created_at, updated_at, started_at, completed_at
		FROM task_queue
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1`,
		string(StatusPending),
	).Scan(&t.ID, &t.Payload, &t.IdempotencyKey, &t.SessionID, &statusStr, &t.Progress, &t.Summary, &t.Result, &completionJSON, &timeoutClass, &t.IdleTimeoutCount, &t.ActiveTimeoutCount, &createdMs, &updatedMs, &startedMs, &completedMs)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: next: select: %w", err)
	}

	now := time.Now().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		UPDATE task_queue SET status = ?, started_at = ?, updated_at = ? WHERE id = ? AND status = ?`,
		string(StatusRunning), now, now, t.ID, string(StatusPending),
	)
	if err != nil {
		return nil, fmt.Errorf("queue: next: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("queue: next: commit: %w", err)
	}

	t.Status = StatusRunning
	t.TimeoutClass = TimeoutClass(timeoutClass)
	t.CreatedAt = time.UnixMilli(createdMs)
	t.UpdatedAt = time.UnixMilli(now)
	t.StartedAt = time.UnixMilli(now)
	t.CompletedAt = time.UnixMilli(completedMs)
	completion, err := parseTaskCompletion(completionJSON)
	if err != nil {
		return nil, fmt.Errorf("queue: next: parse completion: %w", err)
	}
	t.Completion = completion
	return &t, nil
}

// BindSession associates a task with the execution session that is processing it.
func (q *Queue) BindSession(ctx context.Context, id int64, sessionID string) error {
	_, err := q.db.ExecContext(ctx, `
		UPDATE task_queue SET session_id = ?, updated_at = ? WHERE id = ?`,
		sessionID, time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("queue: bind session: %w", err)
	}
	return nil
}

// UpdateProgress stores a short progress string and refreshes updated_at.
func (q *Queue) UpdateProgress(ctx context.Context, id int64, progress string) error {
	_, err := q.db.ExecContext(ctx, `
		UPDATE task_queue SET progress = ?, updated_at = ? WHERE id = ? AND status = ?`,
		progress, time.Now().UnixMilli(), id, string(StatusRunning),
	)
	if err != nil {
		return fmt.Errorf("queue: update progress: %w", err)
	}
	return nil
}

// MarkDone sets a task to done with the given result.
func (q *Queue) MarkDone(ctx context.Context, id int64, result, summary string) error {
	completionJSON, err := q.buildCompletionJSON(ctx, id, StatusDone, summary, result)
	if err != nil {
		return fmt.Errorf("queue: mark done: %w", err)
	}
	res, err := q.db.ExecContext(ctx, `
		UPDATE task_queue SET status = ?, progress = ?, summary = ?, result = ?, completion = ?, updated_at = ?, completed_at = ? WHERE id = ? AND status = ?`,
		string(StatusDone), "completed", summary, result, completionJSON, time.Now().UnixMilli(), time.Now().UnixMilli(), id, string(StatusRunning),
	)
	if err != nil {
		return fmt.Errorf("queue: mark done: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("queue: mark done: %w", core.ErrNotFound)
	}
	return nil
}

// MarkFailed sets a task to failed with the given error message.
func (q *Queue) MarkFailed(ctx context.Context, id int64, errMsg string) error {
	completionJSON, err := q.buildCompletionJSON(ctx, id, StatusFailed, "", errMsg)
	if err != nil {
		return fmt.Errorf("queue: mark failed: %w", err)
	}
	res, err := q.db.ExecContext(ctx, `
		UPDATE task_queue SET status = ?, progress = ?, summary = ?, result = ?, completion = ?, updated_at = ?, completed_at = ?, timeout_class = ? WHERE id = ? AND status = ?`,
		string(StatusFailed), "failed", completionSummary(StatusFailed, "", errMsg), errMsg, completionJSON, time.Now().UnixMilli(), time.Now().UnixMilli(), string(TimeoutClassNone), id, string(StatusRunning),
	)
	if err != nil {
		return fmt.Errorf("queue: mark failed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("queue: mark failed: %w", core.ErrNotFound)
	}
	return nil
}

// List returns all tasks ordered by created_at descending.
func (q *Queue) List(ctx context.Context) ([]Task, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT id, payload, idempotency_key, session_id, status, progress, summary, result, completion, timeout_class, idle_timeout_count, active_timeout_count, created_at, updated_at, started_at, completed_at
		FROM task_queue
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("queue: list: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var statusStr string
		var completionJSON string
		var timeoutClass string
		var createdMs, updatedMs, startedMs, completedMs int64
		if err := rows.Scan(&t.ID, &t.Payload, &t.IdempotencyKey, &t.SessionID, &statusStr, &t.Progress, &t.Summary, &t.Result, &completionJSON, &timeoutClass, &t.IdleTimeoutCount, &t.ActiveTimeoutCount,
			&createdMs, &updatedMs, &startedMs, &completedMs); err != nil {
			return nil, fmt.Errorf("queue: list: scan: %w", err)
		}
		t.Status = TaskStatus(statusStr)
		t.TimeoutClass = TimeoutClass(timeoutClass)
		t.CreatedAt = time.UnixMilli(createdMs)
		t.UpdatedAt = time.UnixMilli(updatedMs)
		t.StartedAt = time.UnixMilli(startedMs)
		t.CompletedAt = time.UnixMilli(completedMs)
		completion, err := parseTaskCompletion(completionJSON)
		if err != nil {
			return nil, fmt.Errorf("queue: list: parse completion: %w", err)
		}
		t.Completion = completion
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// Get returns a single task by ID.
func (q *Queue) Get(ctx context.Context, id int64) (*Task, error) {
	var t Task
	var statusStr string
	var completionJSON string
	var timeoutClass string
	var createdMs, updatedMs, startedMs, completedMs int64

	err := q.db.QueryRowContext(ctx, `
		SELECT id, payload, idempotency_key, session_id, status, progress, summary, result, completion, timeout_class, idle_timeout_count, active_timeout_count, created_at, updated_at, started_at, completed_at
		FROM task_queue
		WHERE id = ?`, id,
	).Scan(&t.ID, &t.Payload, &t.IdempotencyKey, &t.SessionID, &statusStr, &t.Progress, &t.Summary, &t.Result, &completionJSON, &timeoutClass, &t.IdleTimeoutCount, &t.ActiveTimeoutCount,
		&createdMs, &updatedMs, &startedMs, &completedMs)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("queue: get: %w", core.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("queue: get: %w", err)
	}

	t.Status = TaskStatus(statusStr)
	t.TimeoutClass = TimeoutClass(timeoutClass)
	t.CreatedAt = time.UnixMilli(createdMs)
	t.UpdatedAt = time.UnixMilli(updatedMs)
	t.StartedAt = time.UnixMilli(startedMs)
	t.CompletedAt = time.UnixMilli(completedMs)
	completion, err := parseTaskCompletion(completionJSON)
	if err != nil {
		return nil, fmt.Errorf("queue: get: parse completion: %w", err)
	}
	t.Completion = completion
	return &t, nil
}

type taskCompletionRecord struct {
	TaskID      int64      `json:"task_id"`
	SessionID   string     `json:"session_id,omitempty"`
	Summary     string     `json:"summary"`
	Status      TaskStatus `json:"status"`
	CreatedAt   int64      `json:"created_at"`
	StartedAt   int64      `json:"started_at"`
	CompletedAt int64      `json:"completed_at"`
}

func (q *Queue) buildCompletionJSON(ctx context.Context, id int64, status TaskStatus, summary, fallback string) (string, error) {
	task, err := q.Get(ctx, id)
	if err != nil {
		return "", err
	}

	record := taskCompletionRecord{
		TaskID:      task.ID,
		SessionID:   task.SessionID,
		Summary:     completionSummary(status, summary, fallback),
		Status:      status,
		CreatedAt:   task.CreatedAt.UnixMilli(),
		StartedAt:   task.StartedAt.UnixMilli(),
		CompletedAt: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func completionSummary(status TaskStatus, summary, fallback string) string {
	if summary = strings.TrimSpace(summary); summary != "" {
		return summary
	}
	if fallback = summarizeProgress(fallback); fallback != "" {
		return fallback
	}
	if status == StatusFailed {
		return "failed"
	}
	return "completed"
}

func parseTaskCompletion(raw string) (*TaskCompletion, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var record taskCompletionRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return nil, err
	}

	return &TaskCompletion{
		TaskID:      record.TaskID,
		SessionID:   record.SessionID,
		Summary:     record.Summary,
		Status:      record.Status,
		CreatedAt:   time.UnixMilli(record.CreatedAt),
		StartedAt:   time.UnixMilli(record.StartedAt),
		CompletedAt: time.UnixMilli(record.CompletedAt),
	}, nil
}

func (c *TaskCompletion) View() map[string]interface{} {
	if c == nil {
		return nil
	}
	return map[string]interface{}{
		"task_id":      c.TaskID,
		"session_id":   c.SessionID,
		"summary":      c.Summary,
		"status":       string(c.Status),
		"created_at":   c.CreatedAt.UnixMilli(),
		"started_at":   c.StartedAt.UnixMilli(),
		"completed_at": c.CompletedAt.UnixMilli(),
	}
}

// CancelPendingTask marks the most recently created pending task as failed
// with the given reason. Returns (taskID, true, nil) when a task was found
// and cancelled, or (0, false, nil) when no pending task exists.
func (q *Queue) CancelPendingTask(ctx context.Context, reason string) (int64, bool, error) {
	// Find the most recent pending task.
	var taskID int64
	err := q.db.QueryRowContext(ctx, `
		SELECT id FROM task_queue WHERE status = ? ORDER BY created_at DESC LIMIT 1`,
		string(StatusPending),
	).Scan(&taskID)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("queue: cancel pending: select: %w", err)
	}

	completionJSON, err := q.buildCompletionJSON(ctx, taskID, StatusFailed, "", reason)
	if err != nil {
		return 0, false, fmt.Errorf("queue: cancel pending: build completion: %w", err)
	}
	now := time.Now().UnixMilli()
	res, err := q.db.ExecContext(ctx, `
		UPDATE task_queue SET status = ?, progress = ?, summary = ?, result = ?, completion = ?, updated_at = ?, completed_at = ?
		WHERE id = ? AND status = ?`,
		string(StatusFailed), "cancelled", completionSummary(StatusFailed, "", reason), reason, completionJSON, now, now,
		taskID, string(StatusPending),
	)
	if err != nil {
		return 0, false, fmt.Errorf("queue: cancel pending: update: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Race: task was picked up between SELECT and UPDATE.
		return 0, false, nil
	}
	return taskID, true, nil
}

// RecoverStale resets tasks that have been in 'running' state longer than
// the given timeout back to 'pending'. Tasks that have already been recovered
// maxRecoveries times are marked as failed instead. Returns the number of
// recovered tasks.
func (q *Queue) RecoverStale(ctx context.Context, staleTimeout time.Duration, maxRecoveries int) (int, error) {
	cutoff := time.Now().Add(-staleTimeout).UnixMilli()
	now := time.Now().UnixMilli()

	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("queue: recover stale: begin tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, session_id, created_at, started_at, updated_at, idle_timeout_count, active_timeout_count
		FROM task_queue
		WHERE status = ? AND started_at > 0 AND updated_at < ?`,
		string(StatusRunning), cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("queue: recover stale: select: %w", err)
	}
	defer rows.Close()

	var recovered int
	for rows.Next() {
		var id, createdAt, startedAt, updatedAt int64
		var sessionID string
		var idleCount, activeCount int
		if err := rows.Scan(&id, &sessionID, &createdAt, &startedAt, &updatedAt, &idleCount, &activeCount); err != nil {
			return 0, fmt.Errorf("queue: recover stale: scan: %w", err)
		}

		timeoutClass := classifyTimeout(startedAt, updatedAt)
		idleInc, activeInc := 0, 0
		if timeoutClass == TimeoutClassActiveButKilled {
			activeInc = 1
		} else {
			timeoutClass = TimeoutClassIdle
			idleInc = 1
		}

		totalRecoveries := idleCount + activeCount + idleInc + activeInc
		if maxRecoveries > 0 && totalRecoveries > maxRecoveries {
			errMsg := fmt.Sprintf("task failed after %d recovery attempts", totalRecoveries-1)
			completionJSON, err := json.Marshal(taskCompletionRecord{
				TaskID:      id,
				SessionID:   sessionID,
				Summary:     completionSummary(StatusFailed, "", errMsg),
				Status:      StatusFailed,
				CreatedAt:   createdAt,
				StartedAt:   startedAt,
				CompletedAt: now,
			})
			if err != nil {
				return 0, fmt.Errorf("queue: recover stale: completion %d: %w", id, err)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE task_queue
				SET status = ?, progress = ?, summary = ?, result = ?, completion = ?, timeout_class = ?, idle_timeout_count = idle_timeout_count + ?, active_timeout_count = active_timeout_count + ?, updated_at = ?, completed_at = ?
				WHERE id = ?`,
				string(StatusFailed), "failed", completionSummary(StatusFailed, "", errMsg), errMsg, string(completionJSON),
				string(timeoutClass), idleInc, activeInc, now, now, id,
			); err != nil {
				return 0, fmt.Errorf("queue: recover stale: fail %d: %w", id, err)
			}
			continue
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE task_queue
			SET status = ?, progress = ?, timeout_class = ?, idle_timeout_count = idle_timeout_count + ?, active_timeout_count = active_timeout_count + ?, started_at = 0, updated_at = ?
			WHERE id = ?`,
			string(StatusPending), "recovered", string(timeoutClass), idleInc, activeInc, now, id,
		); err != nil {
			return 0, fmt.Errorf("queue: recover stale: update %d: %w", id, err)
		}
		recovered++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("queue: recover stale: rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("queue: recover stale: commit: %w", err)
	}
	return recovered, nil
}

// TimeoutMetrics returns aggregate counts for recovered timeout classifications.
func (q *Queue) TimeoutMetrics(ctx context.Context) (TimeoutMetrics, error) {
	var metrics TimeoutMetrics
	if err := q.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(idle_timeout_count), 0), COALESCE(SUM(active_timeout_count), 0)
		FROM task_queue`,
	).Scan(&metrics.IdleRecoveries, &metrics.ActiveButKilledRecoveries); err != nil {
		return TimeoutMetrics{}, fmt.Errorf("queue: timeout metrics: %w", err)
	}

	total := metrics.IdleRecoveries + metrics.ActiveButKilledRecoveries
	if total > 0 {
		metrics.FalseTimeoutRate = float64(metrics.ActiveButKilledRecoveries) / float64(total)
	}
	return metrics, nil
}

func (q *Queue) ensureColumns(ctx context.Context) error {
	rows, err := q.db.QueryContext(ctx, `PRAGMA table_info(task_queue)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	cols := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		cols[name] = struct{}{}
	}
	type migration struct {
		name string
		sql  string
	}
	for _, m := range []migration{
		{name: "idempotency_key", sql: `ALTER TABLE task_queue ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT ''`},
		{name: "session_id", sql: `ALTER TABLE task_queue ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`},
		{name: "progress", sql: `ALTER TABLE task_queue ADD COLUMN progress TEXT NOT NULL DEFAULT ''`},
		{name: "summary", sql: `ALTER TABLE task_queue ADD COLUMN summary TEXT NOT NULL DEFAULT ''`},
		{name: "result", sql: `ALTER TABLE task_queue ADD COLUMN result TEXT NOT NULL DEFAULT ''`},
		{name: "completion", sql: `ALTER TABLE task_queue ADD COLUMN completion TEXT NOT NULL DEFAULT ''`},
		{name: "timeout_class", sql: `ALTER TABLE task_queue ADD COLUMN timeout_class TEXT NOT NULL DEFAULT ''`},
		{name: "idle_timeout_count", sql: `ALTER TABLE task_queue ADD COLUMN idle_timeout_count INTEGER NOT NULL DEFAULT 0`},
		{name: "active_timeout_count", sql: `ALTER TABLE task_queue ADD COLUMN active_timeout_count INTEGER NOT NULL DEFAULT 0`},
		{name: "updated_at", sql: `ALTER TABLE task_queue ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0`},
		{name: "started_at", sql: `ALTER TABLE task_queue ADD COLUMN started_at INTEGER NOT NULL DEFAULT 0`},
		{name: "completed_at", sql: `ALTER TABLE task_queue ADD COLUMN completed_at INTEGER NOT NULL DEFAULT 0`},
	} {
		if _, ok := cols[m.name]; ok {
			continue
		}
		if _, err := q.db.ExecContext(ctx, m.sql); err != nil {
			return err
		}
	}
	return nil
}

func (q *Queue) lookupActiveTaskID(ctx context.Context, idemKey string, cutoff int64) (int64, error) {
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
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return existingID, nil
}

func (q *Queue) lookupActiveTaskIDAny(ctx context.Context, idemKey string) (int64, error) {
	var existingID int64
	err := q.db.QueryRowContext(ctx, `
		SELECT id FROM task_queue
		WHERE idempotency_key = ?
		  AND status IN ('pending','running')
		ORDER BY created_at DESC
		LIMIT 1`,
		idemKey,
	).Scan(&existingID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return existingID, nil
}

func (q *Queue) insertTask(ctx context.Context, payload, idemKey string, now int64) (sql.Result, error) {
	sessionID := ParseTaskPayload(payload).SessionID
	var (
		res sql.Result
		err error
	)
	for attempt := 0; attempt < 5; attempt++ {
		res, err = q.db.ExecContext(ctx, `
			INSERT INTO task_queue (payload, idempotency_key, session_id, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			payload, idemKey, sessionID, string(StatusPending), now, now,
		)
		if !isBusyError(err) {
			return res, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return res, err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func isBusyError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "database is locked")
}

func classifyTimeout(startedAt, updatedAt int64) TimeoutClass {
	if updatedAt > startedAt && startedAt > 0 {
		return TimeoutClassActiveButKilled
	}
	return TimeoutClassIdle
}
