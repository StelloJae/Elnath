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
	session_id   TEXT    NOT NULL DEFAULT '',
	status       TEXT    NOT NULL DEFAULT 'pending',
	progress     TEXT    NOT NULL DEFAULT '',
	summary      TEXT    NOT NULL DEFAULT '',
	result       TEXT    NOT NULL DEFAULT '',
	completion   TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL DEFAULT 0,
	started_at   INTEGER NOT NULL DEFAULT 0,
	completed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS task_queue_status ON task_queue(status);
CREATE INDEX IF NOT EXISTS task_queue_created ON task_queue(created_at);
`

const defaultStaleTimeout = 5 * time.Minute

// TaskStatus represents the lifecycle state of a queued task.
type TaskStatus string

const (
	StatusPending TaskStatus = "pending"
	StatusRunning TaskStatus = "running"
	StatusDone    TaskStatus = "done"
	StatusFailed  TaskStatus = "failed"
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

// Task is a single unit of work in the queue.
type Task struct {
	ID          int64
	Payload     string
	SessionID   string
	Status      TaskStatus
	Progress    string
	Summary     string
	Result      string
	Completion  *TaskCompletion
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   time.Time
	CompletedAt time.Time
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

	if _, err := q.RecoverStale(context.Background(), defaultStaleTimeout); err != nil {
		return nil, fmt.Errorf("queue: recover stale: %w", err)
	}

	return q, nil
}

// Enqueue inserts a new pending task and returns its ID.
func (q *Queue) Enqueue(ctx context.Context, payload string) (int64, error) {
	now := time.Now().UnixMilli()
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO task_queue (payload, status, created_at, updated_at)
		VALUES (?, ?, ?, ?)`,
		payload, string(StatusPending), now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("queue: enqueue: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("queue: enqueue: last id: %w", err)
	}
	return id, nil
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
	var createdMs, updatedMs, startedMs, completedMs int64

	err = tx.QueryRowContext(ctx, `
		SELECT id, payload, session_id, status, progress, summary, result, completion, created_at, updated_at, started_at, completed_at
		FROM task_queue
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1`,
		string(StatusPending),
	).Scan(&t.ID, &t.Payload, &t.SessionID, &statusStr, &t.Progress, &t.Summary, &t.Result, &completionJSON, &createdMs, &updatedMs, &startedMs, &completedMs)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: next: select: %w", err)
	}

	now := time.Now().UnixMilli()
	_, err = tx.ExecContext(ctx, `
		UPDATE task_queue SET status = ?, started_at = ?, updated_at = ? WHERE id = ?`,
		string(StatusRunning), now, now, t.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("queue: next: update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("queue: next: commit: %w", err)
	}

	t.Status = StatusRunning
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
		UPDATE task_queue SET progress = ?, updated_at = ? WHERE id = ?`,
		progress, time.Now().UnixMilli(), id,
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
		UPDATE task_queue SET status = ?, progress = ?, summary = ?, result = ?, completion = ?, updated_at = ?, completed_at = ? WHERE id = ? AND status = ?`,
		string(StatusFailed), "failed", "", errMsg, completionJSON, time.Now().UnixMilli(), time.Now().UnixMilli(), id, string(StatusRunning),
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
		SELECT id, payload, session_id, status, progress, summary, result, completion, created_at, updated_at, started_at, completed_at
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
		var createdMs, updatedMs, startedMs, completedMs int64
		if err := rows.Scan(&t.ID, &t.Payload, &t.SessionID, &statusStr, &t.Progress, &t.Summary, &t.Result, &completionJSON,
			&createdMs, &updatedMs, &startedMs, &completedMs); err != nil {
			return nil, fmt.Errorf("queue: list: scan: %w", err)
		}
		t.Status = TaskStatus(statusStr)
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
	var createdMs, updatedMs, startedMs, completedMs int64

	err := q.db.QueryRowContext(ctx, `
		SELECT id, payload, session_id, status, progress, summary, result, completion, created_at, updated_at, started_at, completed_at
		FROM task_queue
		WHERE id = ?`, id,
	).Scan(&t.ID, &t.Payload, &t.SessionID, &statusStr, &t.Progress, &t.Summary, &t.Result, &completionJSON,
		&createdMs, &updatedMs, &startedMs, &completedMs)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("queue: get: %w", core.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("queue: get: %w", err)
	}

	t.Status = TaskStatus(statusStr)
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

// RecoverStale resets tasks that have been in 'running' state longer than
// the given timeout back to 'pending'. Returns the number of recovered tasks.
func (q *Queue) RecoverStale(ctx context.Context, staleTimeout time.Duration) (int, error) {
	cutoff := time.Now().Add(-staleTimeout).UnixMilli()

	res, err := q.db.ExecContext(ctx, `
		UPDATE task_queue
		SET status = ?, progress = ?, started_at = 0, updated_at = ?
		WHERE status = ? AND started_at > 0 AND started_at < ?`,
		string(StatusPending), "recovered", time.Now().UnixMilli(), string(StatusRunning), cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("queue: recover stale: %w", err)
	}

	n, _ := res.RowsAffected()
	return int(n), nil
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
		{name: "session_id", sql: `ALTER TABLE task_queue ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`},
		{name: "progress", sql: `ALTER TABLE task_queue ADD COLUMN progress TEXT NOT NULL DEFAULT ''`},
		{name: "summary", sql: `ALTER TABLE task_queue ADD COLUMN summary TEXT NOT NULL DEFAULT ''`},
		{name: "completion", sql: `ALTER TABLE task_queue ADD COLUMN completion TEXT NOT NULL DEFAULT ''`},
		{name: "updated_at", sql: `ALTER TABLE task_queue ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0`},
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
