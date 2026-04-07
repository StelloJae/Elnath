package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/stello/elnath/internal/core"
)

const createQueueTable = `
CREATE TABLE IF NOT EXISTS task_queue (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	payload      TEXT    NOT NULL,
	status       TEXT    NOT NULL DEFAULT 'pending',
	result       TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
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

// Task is a single unit of work in the queue.
type Task struct {
	ID          int64
	Payload     string
	Status      TaskStatus
	Result      string
	CreatedAt   time.Time
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

	if _, err := q.RecoverStale(context.Background(), defaultStaleTimeout); err != nil {
		return nil, fmt.Errorf("queue: recover stale: %w", err)
	}

	return q, nil
}

// Enqueue inserts a new pending task and returns its ID.
func (q *Queue) Enqueue(ctx context.Context, payload string) (int64, error) {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO task_queue (payload, status, created_at)
		VALUES (?, ?, ?)`,
		payload, string(StatusPending), time.Now().UnixMilli(),
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
	var createdMs, startedMs, completedMs int64

	err = tx.QueryRowContext(ctx, `
		SELECT id, payload, status, result, created_at, started_at, completed_at
		FROM task_queue
		WHERE status = ?
		ORDER BY created_at ASC
		LIMIT 1`,
		string(StatusPending),
	).Scan(&t.ID, &t.Payload, &statusStr, &t.Result, &createdMs, &startedMs, &completedMs)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: next: select: %w", err)
	}

	now := time.Now().UnixMilli()
	_, err = tx.ExecContext(ctx, `
		UPDATE task_queue SET status = ?, started_at = ? WHERE id = ?`,
		string(StatusRunning), now, t.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("queue: next: update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("queue: next: commit: %w", err)
	}

	t.Status = StatusRunning
	t.CreatedAt = time.UnixMilli(createdMs)
	t.StartedAt = time.UnixMilli(now)
	t.CompletedAt = time.UnixMilli(completedMs)
	return &t, nil
}

// MarkDone sets a task to done with the given result.
func (q *Queue) MarkDone(ctx context.Context, id int64, result string) error {
	res, err := q.db.ExecContext(ctx, `
		UPDATE task_queue SET status = ?, result = ?, completed_at = ? WHERE id = ? AND status = ?`,
		string(StatusDone), result, time.Now().UnixMilli(), id, string(StatusRunning),
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
	res, err := q.db.ExecContext(ctx, `
		UPDATE task_queue SET status = ?, result = ?, completed_at = ? WHERE id = ? AND status = ?`,
		string(StatusFailed), errMsg, time.Now().UnixMilli(), id, string(StatusRunning),
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
		SELECT id, payload, status, result, created_at, started_at, completed_at
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
		var createdMs, startedMs, completedMs int64
		if err := rows.Scan(&t.ID, &t.Payload, &statusStr, &t.Result,
			&createdMs, &startedMs, &completedMs); err != nil {
			return nil, fmt.Errorf("queue: list: scan: %w", err)
		}
		t.Status = TaskStatus(statusStr)
		t.CreatedAt = time.UnixMilli(createdMs)
		t.StartedAt = time.UnixMilli(startedMs)
		t.CompletedAt = time.UnixMilli(completedMs)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// Get returns a single task by ID.
func (q *Queue) Get(ctx context.Context, id int64) (*Task, error) {
	var t Task
	var statusStr string
	var createdMs, startedMs, completedMs int64

	err := q.db.QueryRowContext(ctx, `
		SELECT id, payload, status, result, created_at, started_at, completed_at
		FROM task_queue
		WHERE id = ?`, id,
	).Scan(&t.ID, &t.Payload, &statusStr, &t.Result,
		&createdMs, &startedMs, &completedMs)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("queue: get: %w", core.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("queue: get: %w", err)
	}

	t.Status = TaskStatus(statusStr)
	t.CreatedAt = time.UnixMilli(createdMs)
	t.StartedAt = time.UnixMilli(startedMs)
	t.CompletedAt = time.UnixMilli(completedMs)
	return &t, nil
}

// RecoverStale resets tasks that have been in 'running' state longer than
// the given timeout back to 'pending'. Returns the number of recovered tasks.
func (q *Queue) RecoverStale(ctx context.Context, staleTimeout time.Duration) (int, error) {
	cutoff := time.Now().Add(-staleTimeout).UnixMilli()

	res, err := q.db.ExecContext(ctx, `
		UPDATE task_queue
		SET status = ?, started_at = 0
		WHERE status = ? AND started_at > 0 AND started_at < ?`,
		string(StatusPending), string(StatusRunning), cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("queue: recover stale: %w", err)
	}

	n, _ := res.RowsAffected()
	return int(n), nil
}
