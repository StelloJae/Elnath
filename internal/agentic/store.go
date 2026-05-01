package agentic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrSignalTriageFailed = errors.New("agentic: signal triage failed")

type Store struct {
	db *sql.DB
}

type ToolActionReceiptCompletion struct {
	ApprovalRequestID  string
	OutputHash         string
	RawOutputHash      string
	VisibleOutputHash  string
	OutputSummary      string
	Status             string
	FailureReason      string
	HookProvenanceJSON string
	Reversible         bool
	CompletedAt        sql.NullTime
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) CreateStandingGoal(ctx context.Context, goal StandingGoal) (*StandingGoal, error) {
	now := nowTime()
	if goal.CreatedAt.IsZero() {
		goal.CreatedAt = now
	}
	if goal.UpdatedAt.IsZero() {
		goal.UpdatedAt = goal.CreatedAt
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO standing_goals(title, description, status, priority, autonomy_level, risk_budget, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, goal.Title, goal.Description, goal.Status, goal.Priority, goal.AutonomyLevel, goal.RiskBudget, timeMillis(goal.CreatedAt), timeMillis(goal.UpdatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetStandingGoal(ctx, id)
}

func (s *Store) GetStandingGoal(ctx context.Context, id int64) (*StandingGoal, error) {
	var goal StandingGoal
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, description, status, priority, autonomy_level, risk_budget, created_at, updated_at
		FROM standing_goals WHERE id = ?
	`, id).Scan(&goal.ID, &goal.Title, &goal.Description, &goal.Status, &goal.Priority, &goal.AutonomyLevel, &goal.RiskBudget, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	goal.CreatedAt = millisTime(createdAt)
	goal.UpdatedAt = millisTime(updatedAt)
	return &goal, nil
}

func (s *Store) CreateSignalWatcher(ctx context.Context, watcher SignalWatcher) (*SignalWatcher, error) {
	now := nowTime()
	if watcher.CreatedAt.IsZero() {
		watcher.CreatedAt = now
	}
	if watcher.UpdatedAt.IsZero() {
		watcher.UpdatedAt = watcher.CreatedAt
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO signal_watchers(goal_id, source, config_json, enabled, interval_s, last_cursor, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(watcher.GoalID), watcher.Source, watcher.ConfigJSON, boolInt(watcher.Enabled), watcher.IntervalS, watcher.LastCursor, timeMillis(watcher.CreatedAt), timeMillis(watcher.UpdatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetSignalWatcher(ctx, id)
}

func (s *Store) GetSignalWatcher(ctx context.Context, id int64) (*SignalWatcher, error) {
	var watcher SignalWatcher
	var goalID sql.NullInt64
	var enabled int
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, goal_id, source, config_json, enabled, interval_s, last_cursor, created_at, updated_at
		FROM signal_watchers WHERE id = ?
	`, id).Scan(&watcher.ID, &goalID, &watcher.Source, &watcher.ConfigJSON, &enabled, &watcher.IntervalS, &watcher.LastCursor, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	watcher.GoalID = intFromNull(goalID)
	watcher.Enabled = enabled != 0
	watcher.CreatedAt = millisTime(createdAt)
	watcher.UpdatedAt = millisTime(updatedAt)
	return &watcher, nil
}

func (s *Store) CreateOrGetSignalWatcher(ctx context.Context, watcher SignalWatcher) (*SignalWatcher, bool, error) {
	now := nowTime()
	if watcher.CreatedAt.IsZero() {
		watcher.CreatedAt = now
	}
	if watcher.UpdatedAt.IsZero() {
		watcher.UpdatedAt = watcher.CreatedAt
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO signal_watchers(goal_id, source, config_json, enabled, interval_s, last_cursor, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(watcher.GoalID), watcher.Source, watcher.ConfigJSON, boolInt(watcher.Enabled), watcher.IntervalS, watcher.LastCursor, timeMillis(watcher.CreatedAt), timeMillis(watcher.UpdatedAt))
	if err != nil {
		return nil, false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		id, err := res.LastInsertId()
		if err != nil {
			return nil, false, err
		}
		created, err := s.GetSignalWatcher(ctx, id)
		return created, false, err
	}

	var id int64
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM signal_watchers
		WHERE COALESCE(goal_id, 0) = ?
			AND source = ?
			AND config_json = ?
		ORDER BY id
		LIMIT 1
	`, watcher.GoalID, watcher.Source, watcher.ConfigJSON).Scan(&id)
	if err != nil {
		return nil, false, err
	}
	existing, err := s.GetSignalWatcher(ctx, id)
	return existing, true, err
}

func (s *Store) UpdateSignalWatcherCursor(ctx context.Context, id int64, cursor string) (*SignalWatcher, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE signal_watchers SET last_cursor = ?, updated_at = MAX(?, updated_at + 1) WHERE id = ?
	`, cursor, timeMillis(nowTime()), id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetSignalWatcher(ctx, id)
}

func (s *Store) CreateGoalSignal(ctx context.Context, signal GoalSignal) (*GoalSignal, error) {
	if signal.ObservedAt.IsZero() {
		signal.ObservedAt = nowTime()
	}
	if signal.Status == "" {
		signal.Status = SignalStatusNew
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO goal_signals(goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(signal.GoalID), nullableInt(signal.WatcherID), signal.Source, signal.Type, signal.PayloadJSON, signal.Fingerprint, signal.Severity, signal.Status, signal.DedupeKey, timeMillis(signal.ObservedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetGoalSignal(ctx, id)
}

func (s *Store) CreateOrGetGoalSignal(ctx context.Context, signal GoalSignal) (*GoalSignal, bool, error) {
	if signal.DedupeKey == "" {
		created, err := s.CreateGoalSignal(ctx, signal)
		return created, false, err
	}
	if signal.ObservedAt.IsZero() {
		signal.ObservedAt = nowTime()
	}
	if signal.Status == "" {
		signal.Status = SignalStatusNew
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO goal_signals(goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(signal.GoalID), nullableInt(signal.WatcherID), signal.Source, signal.Type, signal.PayloadJSON, signal.Fingerprint, signal.Severity, signal.Status, signal.DedupeKey, timeMillis(signal.ObservedAt))
	if err != nil {
		return nil, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		existing, err := s.GetGoalSignalByDedupeKey(ctx, signal.GoalID, signal.Source, signal.Type, signal.DedupeKey)
		return existing, true, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	created, err := s.GetGoalSignal(ctx, id)
	return created, false, err
}

func (s *Store) GetGoalSignal(ctx context.Context, id int64) (*GoalSignal, error) {
	var signal GoalSignal
	var goalID, watcherID sql.NullInt64
	var observedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at
		FROM goal_signals WHERE id = ?
	`, id).Scan(&signal.ID, &goalID, &watcherID, &signal.Source, &signal.Type, &signal.PayloadJSON, &signal.Fingerprint, &signal.Severity, &signal.Status, &signal.DedupeKey, &observedAt)
	if err != nil {
		return nil, err
	}
	signal.GoalID = intFromNull(goalID)
	signal.WatcherID = intFromNull(watcherID)
	signal.ObservedAt = millisTime(observedAt)
	return &signal, nil
}

func (s *Store) GetGoalSignalByDedupeKey(ctx context.Context, goalID int64, source, signalType, dedupeKey string) (*GoalSignal, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM goal_signals
		WHERE COALESCE(goal_id, 0) = ?
			AND source = ?
			AND type = ?
			AND dedupe_key = ?
	`, goalID, source, signalType, dedupeKey).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetGoalSignal(ctx, id)
}

func (s *Store) ListGoalSignalsByStatus(ctx context.Context, status string, limit int) ([]GoalSignal, error) {
	query := `
		SELECT id, goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at
		FROM goal_signals
		WHERE status = ?
		ORDER BY observed_at, id
	`
	args := []any{status}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var signals []GoalSignal
	for rows.Next() {
		signal, err := scanGoalSignal(rows)
		if err != nil {
			return nil, err
		}
		signals = append(signals, *signal)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return signals, nil
}

func (s *Store) UpdateGoalSignalStatus(ctx context.Context, id int64, status string) (*GoalSignal, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE goal_signals SET status = ? WHERE id = ?
	`, status, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetGoalSignal(ctx, id)
}

func (s *Store) CreateAgenticTask(ctx context.Context, task AgenticTask) (*AgenticTask, error) {
	now := nowTime()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO agentic_tasks(goal_id, signal_id, parent_id, queue_task_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at, due_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(task.GoalID), nullableInt(task.SignalID), nullableInt(task.ParentID), nullableInt(task.QueueTaskID), task.Title, task.Prompt, task.Status, task.Priority, task.RiskLevel, task.AutonomyDecision, task.ApprovalRequestID, task.VerificationStatus, timeMillis(task.CreatedAt), timeMillis(task.UpdatedAt), nullableSQLTime(task.DueAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetAgenticTask(ctx, id)
}

func (s *Store) GetAgenticTask(ctx context.Context, id int64) (*AgenticTask, error) {
	var task AgenticTask
	var goalID, signalID, parentID, queueTaskID, dueAt sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, goal_id, signal_id, parent_id, queue_task_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at, due_at
		FROM agentic_tasks WHERE id = ?
	`, id).Scan(&task.ID, &goalID, &signalID, &parentID, &queueTaskID, &task.Title, &task.Prompt, &task.Status, &task.Priority, &task.RiskLevel, &task.AutonomyDecision, &task.ApprovalRequestID, &task.VerificationStatus, &createdAt, &updatedAt, &dueAt)
	if err != nil {
		return nil, err
	}
	task.GoalID = intFromNull(goalID)
	task.SignalID = intFromNull(signalID)
	task.ParentID = intFromNull(parentID)
	task.QueueTaskID = intFromNull(queueTaskID)
	task.CreatedAt = millisTime(createdAt)
	task.UpdatedAt = millisTime(updatedAt)
	task.DueAt = nullTimeFromMillis(dueAt)
	return &task, nil
}

func (s *Store) GetAgenticTaskByQueueTaskID(ctx context.Context, queueTaskID int64) (*AgenticTask, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM agentic_tasks WHERE queue_task_id = ?
	`, queueTaskID).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetAgenticTask(ctx, id)
}

func (s *Store) GetAgenticTaskBySignalID(ctx context.Context, signalID int64) (*AgenticTask, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM agentic_tasks WHERE signal_id = ?
	`, signalID).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetAgenticTask(ctx, id)
}

func (s *Store) LinkAgenticTaskSignal(ctx context.Context, taskID, signalID int64) (*AgenticTask, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE agentic_tasks
		SET signal_id = ?, updated_at = MAX(?, updated_at + 1)
		WHERE id = ? AND signal_id IS NULL
	`, signalID, timeMillis(nowTime()), taskID)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	task, getErr := s.GetAgenticTask(ctx, taskID)
	if getErr != nil {
		return nil, getErr
	}
	if n == 0 && task.SignalID != signalID {
		return nil, fmt.Errorf("agentic: task %d is already linked to signal %d", taskID, task.SignalID)
	}
	return task, nil
}

func (s *Store) TriageGoalSignal(ctx context.Context, signalID int64) (*SignalTriageResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	result, err := s.triageGoalSignalTx(ctx, tx, signalID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) UpdateAgenticTaskStatus(ctx context.Context, id int64, status string) (*AgenticTask, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE agentic_tasks SET status = ?, updated_at = MAX(?, updated_at + 1) WHERE id = ?
	`, status, timeMillis(nowTime()), id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetAgenticTask(ctx, id)
}

func (s *Store) SetAgenticTaskApprovalRequestID(ctx context.Context, id int64, approvalRequestID string) (*AgenticTask, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE agentic_tasks
		SET approval_request_id = ?, updated_at = MAX(?, updated_at + 1)
		WHERE id = ?
	`, approvalRequestID, timeMillis(nowTime()), id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetAgenticTask(ctx, id)
}

func (s *Store) SetAgenticTaskApprovalRequestIDTx(ctx context.Context, tx *sql.Tx, id int64, approvalRequestID string) (*AgenticTask, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE agentic_tasks
		SET approval_request_id = ?, updated_at = MAX(?, updated_at + 1)
		WHERE id = ?
	`, approvalRequestID, timeMillis(nowTime()), id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return getAgenticTaskTx(ctx, tx, id)
}

func (s *Store) ReconcileDaemonTaskStatuses(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE agentic_tasks
		SET status = CASE (
				SELECT status FROM task_queue WHERE task_queue.id = agentic_tasks.queue_task_id
			)
			WHEN 'done' THEN ?
			WHEN 'failed' THEN ?
			ELSE status
			END,
			updated_at = MAX((
				SELECT updated_at FROM task_queue WHERE task_queue.id = agentic_tasks.queue_task_id
			), updated_at + 1)
		WHERE queue_task_id IS NOT NULL
			AND status NOT IN (?, ?, ?)
			AND EXISTS (
				SELECT 1 FROM task_queue
				WHERE task_queue.id = agentic_tasks.queue_task_id
					AND task_queue.status IN ('done', 'failed')
			)
	`, TaskStatusSucceeded, TaskStatusFailed, TaskStatusSucceeded, TaskStatusFailed, TaskStatusCanceled)
	return err
}

func (s *Store) triageGoalSignalTx(ctx context.Context, tx *sql.Tx, signalID int64) (*SignalTriageResult, error) {
	signal, err := getGoalSignalTx(ctx, tx, signalID)
	if err != nil {
		return nil, err
	}
	if task, err := getAgenticTaskBySignalIDTx(ctx, tx, signal.ID); err == nil {
		if signal.Status == SignalStatusNew {
			if err := updateGoalSignalStatusTx(ctx, tx, signal.ID, SignalStatusTriaged); err != nil {
				return nil, err
			}
		}
		return &SignalTriageResult{Task: task}, nil
	} else if err != sql.ErrNoRows {
		return nil, err
	}

	queueTaskID, payloadErr := queueTaskIDFromSignalPayload(signal.PayloadJSON)
	if payloadErr == nil && queueTaskID > 0 {
		if task, err := getAgenticTaskByQueueTaskIDTx(ctx, tx, queueTaskID); err == nil {
			linked := false
			if signal.Status == SignalStatusNew {
				if task.SignalID == 0 {
					task, err = linkAgenticTaskSignalTx(ctx, tx, task.ID, signal.ID)
					if err != nil {
						return nil, err
					}
					linked = true
				}
				if err := updateGoalSignalStatusTx(ctx, tx, signal.ID, SignalStatusTriaged); err != nil {
					return nil, err
				}
			}
			return &SignalTriageResult{Task: task, Linked: linked}, nil
		} else if err != sql.ErrNoRows {
			return nil, err
		}
	}

	if signal.Status != SignalStatusNew {
		return nil, fmt.Errorf("agentic: signal %d status %q is not triageable", signal.ID, signal.Status)
	}

	if payloadErr != nil {
		if err := updateGoalSignalStatusTx(ctx, tx, signal.ID, SignalStatusFailed); err != nil {
			return nil, err
		}
		return &SignalTriageResult{Failed: true}, nil
	}

	task, err := createAgenticTaskTx(ctx, tx, taskFromSignal(signal, queueTaskID))
	if err != nil {
		return nil, err
	}
	if err := updateGoalSignalStatusTx(ctx, tx, signal.ID, SignalStatusTriaged); err != nil {
		return nil, err
	}
	return &SignalTriageResult{Task: task, Created: true}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanGoalSignal(scanner rowScanner) (*GoalSignal, error) {
	var signal GoalSignal
	var goalID, watcherID sql.NullInt64
	var observedAt int64
	if err := scanner.Scan(&signal.ID, &goalID, &watcherID, &signal.Source, &signal.Type, &signal.PayloadJSON, &signal.Fingerprint, &signal.Severity, &signal.Status, &signal.DedupeKey, &observedAt); err != nil {
		return nil, err
	}
	signal.GoalID = intFromNull(goalID)
	signal.WatcherID = intFromNull(watcherID)
	signal.ObservedAt = millisTime(observedAt)
	return &signal, nil
}

func scanAgenticTask(scanner rowScanner) (*AgenticTask, error) {
	var task AgenticTask
	var goalID, signalID, parentID, queueTaskID, dueAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := scanner.Scan(&task.ID, &goalID, &signalID, &parentID, &queueTaskID, &task.Title, &task.Prompt, &task.Status, &task.Priority, &task.RiskLevel, &task.AutonomyDecision, &task.ApprovalRequestID, &task.VerificationStatus, &createdAt, &updatedAt, &dueAt); err != nil {
		return nil, err
	}
	task.GoalID = intFromNull(goalID)
	task.SignalID = intFromNull(signalID)
	task.ParentID = intFromNull(parentID)
	task.QueueTaskID = intFromNull(queueTaskID)
	task.CreatedAt = millisTime(createdAt)
	task.UpdatedAt = millisTime(updatedAt)
	task.DueAt = nullTimeFromMillis(dueAt)
	return &task, nil
}

func getGoalSignalTx(ctx context.Context, tx *sql.Tx, id int64) (*GoalSignal, error) {
	return scanGoalSignal(tx.QueryRowContext(ctx, `
		SELECT id, goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at
		FROM goal_signals WHERE id = ?
	`, id))
}

func getAgenticTaskTx(ctx context.Context, tx *sql.Tx, id int64) (*AgenticTask, error) {
	return scanAgenticTask(tx.QueryRowContext(ctx, `
		SELECT id, goal_id, signal_id, parent_id, queue_task_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at, due_at
		FROM agentic_tasks WHERE id = ?
	`, id))
}

func getAgenticTaskBySignalIDTx(ctx context.Context, tx *sql.Tx, signalID int64) (*AgenticTask, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM agentic_tasks WHERE signal_id = ?`, signalID).Scan(&id); err != nil {
		return nil, err
	}
	return getAgenticTaskTx(ctx, tx, id)
}

func getAgenticTaskByQueueTaskIDTx(ctx context.Context, tx *sql.Tx, queueTaskID int64) (*AgenticTask, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM agentic_tasks WHERE queue_task_id = ?`, queueTaskID).Scan(&id); err != nil {
		return nil, err
	}
	return getAgenticTaskTx(ctx, tx, id)
}

func createAgenticTaskTx(ctx context.Context, tx *sql.Tx, task AgenticTask) (*AgenticTask, error) {
	now := nowTime()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO agentic_tasks(goal_id, signal_id, parent_id, queue_task_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at, due_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(task.GoalID), nullableInt(task.SignalID), nullableInt(task.ParentID), nullableInt(task.QueueTaskID), task.Title, task.Prompt, task.Status, task.Priority, task.RiskLevel, task.AutonomyDecision, task.ApprovalRequestID, task.VerificationStatus, timeMillis(task.CreatedAt), timeMillis(task.UpdatedAt), nullableSQLTime(task.DueAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return getAgenticTaskTx(ctx, tx, id)
}

func linkAgenticTaskSignalTx(ctx context.Context, tx *sql.Tx, taskID, signalID int64) (*AgenticTask, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE agentic_tasks
		SET signal_id = ?, updated_at = MAX(?, updated_at + 1)
		WHERE id = ? AND signal_id IS NULL
	`, signalID, timeMillis(nowTime()), taskID)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	task, err := getAgenticTaskTx(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if n == 0 && task.SignalID != signalID {
		return nil, fmt.Errorf("agentic: task %d is already linked to signal %d", taskID, task.SignalID)
	}
	return task, nil
}

func updateGoalSignalStatusTx(ctx context.Context, tx *sql.Tx, id int64, status string) error {
	res, err := tx.ExecContext(ctx, `UPDATE goal_signals SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func queueTaskIDFromSignalPayload(payloadJSON string) (int64, error) {
	if strings.TrimSpace(payloadJSON) == "" {
		return 0, nil
	}
	var payload struct {
		QueueTaskID int64 `json:"queue_task_id"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return 0, fmt.Errorf("agentic: parse signal payload: %w", err)
	}
	if payload.QueueTaskID < 0 {
		return 0, fmt.Errorf("agentic: signal payload has negative queue_task_id %d", payload.QueueTaskID)
	}
	return payload.QueueTaskID, nil
}

func taskFromSignal(signal *GoalSignal, queueTaskID int64) AgenticTask {
	status := TaskStatusProposed
	title := fmt.Sprintf("Proposed task from %s signal", signal.Source)
	prompt := fmt.Sprintf("Triage proposal for signal %d (%s/%s). Raw prompt is not reconstructed from signal payload.", signal.ID, signal.Source, signal.Type)
	if queueTaskID > 0 {
		status = TaskStatusPending
		title = fmt.Sprintf("Queue-backed task from %s signal", signal.Source)
		prompt = fmt.Sprintf("Queue-backed signal %d (%s/%s) references daemon queue task %d. Raw prompt is not reconstructed from signal payload.", signal.ID, signal.Source, signal.Type, queueTaskID)
	}
	return AgenticTask{
		GoalID:             signal.GoalID,
		SignalID:           signal.ID,
		QueueTaskID:        queueTaskID,
		Title:              title,
		Prompt:             prompt,
		Status:             status,
		Priority:           signal.Severity,
		RiskLevel:          RiskLevelLow,
		AutonomyDecision:   PolicyDecisionObserve,
		VerificationStatus: VerificationStatusPending,
	}
}

func (s *Store) CreateTaskEdge(ctx context.Context, edge TaskEdge) (*TaskEdge, error) {
	if edge.CreatedAt.IsZero() {
		edge.CreatedAt = nowTime()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO task_edges(parent_id, child_id, edge_type, created_at)
		VALUES (?, ?, ?, ?)
	`, edge.ParentID, edge.ChildID, edge.EdgeType, timeMillis(edge.CreatedAt))
	if err != nil {
		return nil, err
	}
	return s.GetTaskEdge(ctx, edge.ParentID, edge.ChildID, edge.EdgeType)
}

func (s *Store) GetTaskEdge(ctx context.Context, parentID, childID int64, edgeType string) (*TaskEdge, error) {
	var edge TaskEdge
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT parent_id, child_id, edge_type, created_at
		FROM task_edges WHERE parent_id = ? AND child_id = ? AND edge_type = ?
	`, parentID, childID, edgeType).Scan(&edge.ParentID, &edge.ChildID, &edge.EdgeType, &createdAt)
	if err != nil {
		return nil, err
	}
	edge.CreatedAt = millisTime(createdAt)
	return &edge, nil
}

func (s *Store) CreateAgentActor(ctx context.Context, actor AgentActor) (*AgentActor, error) {
	now := nowTime()
	if actor.CreatedAt.IsZero() {
		actor.CreatedAt = now
	}
	if actor.UpdatedAt.IsZero() {
		actor.UpdatedAt = actor.CreatedAt
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_actors(task_id, role, state_json, inbox_json, outbox_json, tool_allowlist_json, budget_json, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, actor.TaskID, actor.Role, actor.StateJSON, actor.InboxJSON, actor.OutboxJSON, actor.ToolAllowlistJSON, actor.BudgetJSON, actor.Status, timeMillis(actor.CreatedAt), timeMillis(actor.UpdatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetAgentActor(ctx, id)
}

func (s *Store) GetAgentActor(ctx context.Context, id int64) (*AgentActor, error) {
	var actor AgentActor
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, role, state_json, inbox_json, outbox_json, tool_allowlist_json, budget_json, status, created_at, updated_at
		FROM agent_actors WHERE id = ?
	`, id).Scan(&actor.ID, &actor.TaskID, &actor.Role, &actor.StateJSON, &actor.InboxJSON, &actor.OutboxJSON, &actor.ToolAllowlistJSON, &actor.BudgetJSON, &actor.Status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	actor.CreatedAt = millisTime(createdAt)
	actor.UpdatedAt = millisTime(updatedAt)
	return &actor, nil
}

func (s *Store) UpdateAgentActor(ctx context.Context, actor AgentActor) (*AgentActor, error) {
	if actor.ID == 0 {
		return nil, fmt.Errorf("agentic: update agent actor: missing id")
	}
	existing, err := s.GetAgentActor(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	if actor.TaskID == 0 {
		actor.TaskID = existing.TaskID
	}
	if actor.Role == "" {
		actor.Role = existing.Role
	}
	if actor.StateJSON == "" {
		actor.StateJSON = existing.StateJSON
	}
	if actor.InboxJSON == "" {
		actor.InboxJSON = existing.InboxJSON
	}
	if actor.OutboxJSON == "" {
		actor.OutboxJSON = existing.OutboxJSON
	}
	if actor.ToolAllowlistJSON == "" {
		actor.ToolAllowlistJSON = existing.ToolAllowlistJSON
	}
	if actor.BudgetJSON == "" {
		actor.BudgetJSON = existing.BudgetJSON
	}
	if actor.Status == "" {
		actor.Status = existing.Status
	}
	actor.UpdatedAt = nowTime()
	if timeMillis(actor.UpdatedAt) <= timeMillis(existing.UpdatedAt) {
		actor.UpdatedAt = existing.UpdatedAt.Add(time.Millisecond)
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE agent_actors
		SET task_id = ?, role = ?, state_json = ?, inbox_json = ?, outbox_json = ?, tool_allowlist_json = ?, budget_json = ?, status = ?, updated_at = ?
		WHERE id = ?
	`, actor.TaskID, actor.Role, actor.StateJSON, actor.InboxJSON, actor.OutboxJSON, actor.ToolAllowlistJSON, actor.BudgetJSON, actor.Status, timeMillis(actor.UpdatedAt), actor.ID)
	if err != nil {
		return nil, err
	}
	return s.GetAgentActor(ctx, actor.ID)
}

func (s *Store) ListAgentActorsByTask(ctx context.Context, taskID int64) ([]AgentActor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, role, state_json, inbox_json, outbox_json, tool_allowlist_json, budget_json, status, created_at, updated_at
		FROM agent_actors
		WHERE task_id = ?
		ORDER BY id
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var actors []AgentActor
	for rows.Next() {
		var actor AgentActor
		var createdAt, updatedAt int64
		if err := rows.Scan(&actor.ID, &actor.TaskID, &actor.Role, &actor.StateJSON, &actor.InboxJSON, &actor.OutboxJSON, &actor.ToolAllowlistJSON, &actor.BudgetJSON, &actor.Status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		actor.CreatedAt = millisTime(createdAt)
		actor.UpdatedAt = millisTime(updatedAt)
		actors = append(actors, actor)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return actors, nil
}

func (s *Store) CreateActorHandoff(ctx context.Context, handoff ActorHandoff) (*ActorHandoff, error) {
	if handoff.CreatedAt.IsZero() {
		handoff.CreatedAt = nowTime()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO actor_handoffs(task_id, from_actor_id, to_actor_id, handoff_type, payload_json, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, handoff.TaskID, handoff.FromActorID, handoff.ToActorID, handoff.HandoffType, handoff.PayloadJSON, handoff.Status, timeMillis(handoff.CreatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.getActorHandoff(ctx, id)
}

func (s *Store) ListActorHandoffsByTask(ctx context.Context, taskID int64) ([]ActorHandoff, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, from_actor_id, to_actor_id, handoff_type, payload_json, status, created_at
		FROM actor_handoffs
		WHERE task_id = ?
		ORDER BY id
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanActorHandoffs(rows)
}

func (s *Store) getActorHandoff(ctx context.Context, id int64) (*ActorHandoff, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, from_actor_id, to_actor_id, handoff_type, payload_json, status, created_at
		FROM actor_handoffs
		WHERE id = ?
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	handoffs, err := scanActorHandoffs(rows)
	if err != nil {
		return nil, err
	}
	if len(handoffs) == 0 {
		return nil, sql.ErrNoRows
	}
	return &handoffs[0], nil
}

func scanActorHandoffs(rows *sql.Rows) ([]ActorHandoff, error) {
	var handoffs []ActorHandoff
	for rows.Next() {
		var handoff ActorHandoff
		var createdAt int64
		if err := rows.Scan(&handoff.ID, &handoff.TaskID, &handoff.FromActorID, &handoff.ToActorID, &handoff.HandoffType, &handoff.PayloadJSON, &handoff.Status, &createdAt); err != nil {
			return nil, err
		}
		handoff.CreatedAt = millisTime(createdAt)
		handoffs = append(handoffs, handoff)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return handoffs, nil
}

func (s *Store) CreatePolicyDecision(ctx context.Context, decision PolicyDecisionRecord) (*PolicyDecisionRecord, error) {
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = nowTime()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO policy_decisions(task_id, actor_id, action_kind, tool_name, risk_level, decision, reason, policy_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, decision.TaskID, nullableInt(decision.ActorID), decision.ActionKind, decision.ToolName, decision.RiskLevel, decision.Decision, decision.Reason, decision.PolicyVersion, timeMillis(decision.CreatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetPolicyDecision(ctx, id)
}

func (s *Store) GetPolicyDecision(ctx context.Context, id int64) (*PolicyDecisionRecord, error) {
	var decision PolicyDecisionRecord
	var actorID sql.NullInt64
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, actor_id, action_kind, tool_name, risk_level, decision, reason, policy_version, created_at
		FROM policy_decisions WHERE id = ?
	`, id).Scan(&decision.ID, &decision.TaskID, &actorID, &decision.ActionKind, &decision.ToolName, &decision.RiskLevel, &decision.Decision, &decision.Reason, &decision.PolicyVersion, &createdAt)
	if err != nil {
		return nil, err
	}
	decision.ActorID = intFromNull(actorID)
	decision.CreatedAt = millisTime(createdAt)
	return &decision, nil
}

func (s *Store) CreateToolActionReceipt(ctx context.Context, receipt ToolActionReceipt) (*ToolActionReceipt, error) {
	if receipt.StartedAt.IsZero() {
		receipt.StartedAt = nowTime()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_action_receipts(task_id, actor_id, policy_decision_id, approval_request_id, tool_name, tool_call_id, input_hash, output_hash, raw_output_hash, visible_output_hash, output_summary, status, failure_reason, hook_provenance_json, reversible, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, receipt.TaskID, nullableInt(receipt.ActorID), nullableInt(receipt.PolicyDecisionID), receipt.ApprovalRequestID, receipt.ToolName, receipt.ToolCallID, receipt.InputHash, receipt.OutputHash, receipt.RawOutputHash, receipt.VisibleOutputHash, receipt.OutputSummary, receipt.Status, receipt.FailureReason, receipt.HookProvenanceJSON, boolInt(receipt.Reversible), timeMillis(receipt.StartedAt), nullableSQLTime(receipt.CompletedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetToolActionReceipt(ctx, id)
}

func (s *Store) CompleteToolActionReceipt(ctx context.Context, id int64, completion ToolActionReceiptCompletion) (*ToolActionReceipt, error) {
	completedAt := completion.CompletedAt
	if !completedAt.Valid {
		completedAt = sql.NullTime{Time: nowTime(), Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE tool_action_receipts
		SET approval_request_id = CASE WHEN ? <> '' THEN ? ELSE approval_request_id END,
			output_hash = ?, raw_output_hash = ?, visible_output_hash = ?, output_summary = ?, status = ?, failure_reason = ?, hook_provenance_json = ?, reversible = ?, completed_at = ?
		WHERE id = ?
	`, completion.ApprovalRequestID, completion.ApprovalRequestID, completion.OutputHash, completion.RawOutputHash, completion.VisibleOutputHash, completion.OutputSummary, completion.Status, completion.FailureReason, completion.HookProvenanceJSON, boolInt(completion.Reversible), nullableSQLTime(completedAt), id)
	if err != nil {
		return nil, err
	}
	return s.GetToolActionReceipt(ctx, id)
}

func (s *Store) GetToolActionReceipt(ctx context.Context, id int64) (*ToolActionReceipt, error) {
	var receipt ToolActionReceipt
	var actorID, decisionID, completedAt sql.NullInt64
	var reversible int
	var startedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, actor_id, policy_decision_id, approval_request_id, tool_name, tool_call_id, input_hash, output_hash, raw_output_hash, visible_output_hash, output_summary, status, failure_reason, hook_provenance_json, reversible, started_at, completed_at
		FROM tool_action_receipts WHERE id = ?
	`, id).Scan(&receipt.ID, &receipt.TaskID, &actorID, &decisionID, &receipt.ApprovalRequestID, &receipt.ToolName, &receipt.ToolCallID, &receipt.InputHash, &receipt.OutputHash, &receipt.RawOutputHash, &receipt.VisibleOutputHash, &receipt.OutputSummary, &receipt.Status, &receipt.FailureReason, &receipt.HookProvenanceJSON, &reversible, &startedAt, &completedAt)
	if err != nil {
		return nil, err
	}
	receipt.ActorID = intFromNull(actorID)
	receipt.PolicyDecisionID = intFromNull(decisionID)
	receipt.Reversible = reversible != 0
	receipt.StartedAt = millisTime(startedAt)
	receipt.CompletedAt = nullTimeFromMillis(completedAt)
	return &receipt, nil
}

func (s *Store) ListToolActionReceiptsByTask(ctx context.Context, taskID int64) ([]ToolActionReceipt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, actor_id, policy_decision_id, approval_request_id, tool_name, tool_call_id, input_hash, output_hash, raw_output_hash, visible_output_hash, output_summary, status, failure_reason, hook_provenance_json, reversible, started_at, completed_at
		FROM tool_action_receipts
		WHERE task_id = ?
		ORDER BY id
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var receipts []ToolActionReceipt
	for rows.Next() {
		var receipt ToolActionReceipt
		var actorID, decisionID, completedAt sql.NullInt64
		var reversible int
		var startedAt int64
		if err := rows.Scan(&receipt.ID, &receipt.TaskID, &actorID, &decisionID, &receipt.ApprovalRequestID, &receipt.ToolName, &receipt.ToolCallID, &receipt.InputHash, &receipt.OutputHash, &receipt.RawOutputHash, &receipt.VisibleOutputHash, &receipt.OutputSummary, &receipt.Status, &receipt.FailureReason, &receipt.HookProvenanceJSON, &reversible, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		receipt.ActorID = intFromNull(actorID)
		receipt.PolicyDecisionID = intFromNull(decisionID)
		receipt.Reversible = reversible != 0
		receipt.StartedAt = millisTime(startedAt)
		receipt.CompletedAt = nullTimeFromMillis(completedAt)
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return receipts, nil
}

func (s *Store) FindReusableApprovalRequestID(ctx context.Context, taskID, actorID int64, toolName, inputHash string) (string, error) {
	var approvalRequestID string
	err := s.db.QueryRowContext(ctx, `
		SELECT r.approval_request_id
		FROM tool_action_receipts r
		JOIN approval_requests a ON a.id = CAST(r.approval_request_id AS INTEGER)
		WHERE r.task_id = ?
			AND COALESCE(r.actor_id, 0) = ?
			AND r.tool_name = ?
			AND r.input_hash = ?
			AND r.status = ?
			AND r.approval_request_id <> ''
			AND a.decision = 'pending'
		ORDER BY r.id
		LIMIT 1
	`, taskID, actorID, toolName, inputHash, ReceiptStatusApprovalRequired).Scan(&approvalRequestID)
	if err != nil {
		return "", err
	}
	return approvalRequestID, nil
}

func (s *Store) CreateVerificationRun(ctx context.Context, run VerificationRun) (*VerificationRun, error) {
	if run.CreatedAt.IsZero() {
		run.CreatedAt = nowTime()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO verification_runs(task_id, verifier_actor_id, criteria_json, evidence_refs_json, verdict, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, run.TaskID, nullableInt(run.VerifierActorID), run.CriteriaJSON, run.EvidenceRefsJSON, run.Verdict, run.Reason, timeMillis(run.CreatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetVerificationRun(ctx, id)
}

func (s *Store) GetVerificationRun(ctx context.Context, id int64) (*VerificationRun, error) {
	var run VerificationRun
	var actorID sql.NullInt64
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, verifier_actor_id, criteria_json, evidence_refs_json, verdict, reason, created_at
		FROM verification_runs WHERE id = ?
	`, id).Scan(&run.ID, &run.TaskID, &actorID, &run.CriteriaJSON, &run.EvidenceRefsJSON, &run.Verdict, &run.Reason, &createdAt)
	if err != nil {
		return nil, err
	}
	run.VerifierActorID = intFromNull(actorID)
	run.CreatedAt = millisTime(createdAt)
	return &run, nil
}

func (s *Store) ListVerificationRunsByTask(ctx context.Context, taskID int64) ([]VerificationRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, verifier_actor_id, criteria_json, evidence_refs_json, verdict, reason, created_at
		FROM verification_runs
		WHERE task_id = ?
		ORDER BY id
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []VerificationRun
	for rows.Next() {
		var run VerificationRun
		var actorID sql.NullInt64
		var createdAt int64
		if err := rows.Scan(&run.ID, &run.TaskID, &actorID, &run.CriteriaJSON, &run.EvidenceRefsJSON, &run.Verdict, &run.Reason, &createdAt); err != nil {
			return nil, err
		}
		run.VerifierActorID = intFromNull(actorID)
		run.CreatedAt = millisTime(createdAt)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return runs, nil
}

func (s *Store) CreateCompletionGate(ctx context.Context, gate CompletionGate) (*CompletionGate, error) {
	now := nowTime()
	if gate.CreatedAt.IsZero() {
		gate.CreatedAt = now
	}
	if gate.UpdatedAt.IsZero() {
		gate.UpdatedAt = gate.CreatedAt
	}
	if gate.ReceiptSummaryJSON == "" {
		gate.ReceiptSummaryJSON = "{}"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO completion_gates(task_id, queue_task_id, verification_run_id, status, reason, receipt_summary_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, gate.TaskID, nullableInt(gate.QueueTaskID), nullableInt(gate.VerificationRunID), gate.Status, gate.Reason, gate.ReceiptSummaryJSON, timeMillis(gate.CreatedAt), timeMillis(gate.UpdatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetCompletionGate(ctx, id)
}

func (s *Store) GetCompletionGate(ctx context.Context, id int64) (*CompletionGate, error) {
	var gate CompletionGate
	var queueTaskID, verificationRunID sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, queue_task_id, verification_run_id, status, reason, receipt_summary_json, created_at, updated_at
		FROM completion_gates WHERE id = ?
	`, id).Scan(&gate.ID, &gate.TaskID, &queueTaskID, &verificationRunID, &gate.Status, &gate.Reason, &gate.ReceiptSummaryJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	gate.QueueTaskID = intFromNull(queueTaskID)
	gate.VerificationRunID = intFromNull(verificationRunID)
	gate.CreatedAt = millisTime(createdAt)
	gate.UpdatedAt = millisTime(updatedAt)
	return &gate, nil
}

func (s *Store) ListCompletionGatesByTask(ctx context.Context, taskID int64) ([]CompletionGate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, queue_task_id, verification_run_id, status, reason, receipt_summary_json, created_at, updated_at
		FROM completion_gates
		WHERE task_id = ?
		ORDER BY id
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var gates []CompletionGate
	for rows.Next() {
		var gate CompletionGate
		var queueTaskID, verificationRunID sql.NullInt64
		var createdAt, updatedAt int64
		if err := rows.Scan(&gate.ID, &gate.TaskID, &queueTaskID, &verificationRunID, &gate.Status, &gate.Reason, &gate.ReceiptSummaryJSON, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		gate.QueueTaskID = intFromNull(queueTaskID)
		gate.VerificationRunID = intFromNull(verificationRunID)
		gate.CreatedAt = millisTime(createdAt)
		gate.UpdatedAt = millisTime(updatedAt)
		gates = append(gates, gate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return gates, nil
}

func (s *Store) CreateMemoryUpdate(ctx context.Context, update MemoryUpdate) (*MemoryUpdate, error) {
	if update.CreatedAt.IsZero() {
		update.CreatedAt = nowTime()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO memory_updates(task_id, receipt_id, verification_run_id, target, operation, payload_hash, status, source, reason, created_at, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, update.TaskID, nullableInt(update.ReceiptID), nullableInt(update.VerificationRunID), update.Target, update.Operation, update.PayloadHash, update.Status, update.Source, update.Reason, timeMillis(update.CreatedAt), nullableSQLTime(update.AppliedAt))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return s.findMemoryUpdateByKey(ctx, update)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetMemoryUpdate(ctx, id)
}

func (s *Store) GetMemoryUpdate(ctx context.Context, id int64) (*MemoryUpdate, error) {
	var update MemoryUpdate
	var receiptID, verificationRunID, appliedAt sql.NullInt64
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, receipt_id, verification_run_id, target, operation, payload_hash, status, source, reason, created_at, applied_at
		FROM memory_updates WHERE id = ?
	`, id).Scan(&update.ID, &update.TaskID, &receiptID, &verificationRunID, &update.Target, &update.Operation, &update.PayloadHash, &update.Status, &update.Source, &update.Reason, &createdAt, &appliedAt)
	if err != nil {
		return nil, err
	}
	update.ReceiptID = intFromNull(receiptID)
	update.VerificationRunID = intFromNull(verificationRunID)
	update.CreatedAt = millisTime(createdAt)
	update.AppliedAt = nullTimeFromMillis(appliedAt)
	return &update, nil
}

func (s *Store) ListMemoryUpdatesByTask(ctx context.Context, taskID int64) ([]MemoryUpdate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, receipt_id, verification_run_id, target, operation, payload_hash, status, source, reason, created_at, applied_at
		FROM memory_updates
		WHERE task_id = ?
		ORDER BY id
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var updates []MemoryUpdate
	for rows.Next() {
		var update MemoryUpdate
		var receiptID, verificationRunID, appliedAt sql.NullInt64
		var createdAt int64
		if err := rows.Scan(&update.ID, &update.TaskID, &receiptID, &verificationRunID, &update.Target, &update.Operation, &update.PayloadHash, &update.Status, &update.Source, &update.Reason, &createdAt, &appliedAt); err != nil {
			return nil, err
		}
		update.ReceiptID = intFromNull(receiptID)
		update.VerificationRunID = intFromNull(verificationRunID)
		update.CreatedAt = millisTime(createdAt)
		update.AppliedAt = nullTimeFromMillis(appliedAt)
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return updates, nil
}

func (s *Store) FindMemoryUpdate(ctx context.Context, update MemoryUpdate) (*MemoryUpdate, error) {
	query := `
		SELECT id
		FROM memory_updates
		WHERE task_id = ?
			AND target = ?
			AND operation = ?
			AND payload_hash = ?
			AND status = ?
	`
	args := []any{update.TaskID, update.Target, update.Operation, update.PayloadHash, update.Status}
	if update.Source != "" {
		query += ` AND source = ?`
		args = append(args, update.Source)
	}
	if update.VerificationRunID == 0 {
		query += ` AND verification_run_id IS NULL`
	} else {
		query += ` AND verification_run_id = ?`
		args = append(args, update.VerificationRunID)
	}
	query += ` ORDER BY id LIMIT 1`

	var id int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetMemoryUpdate(ctx, id)
}

func (s *Store) findMemoryUpdateByKey(ctx context.Context, update MemoryUpdate) (*MemoryUpdate, error) {
	query := `
		SELECT id
		FROM memory_updates
		WHERE task_id = ?
			AND target = ?
			AND operation = ?
			AND payload_hash = ?
			AND source = ?
	`
	args := []any{update.TaskID, update.Target, update.Operation, update.PayloadHash, update.Source}
	if update.VerificationRunID == 0 {
		query += ` AND verification_run_id IS NULL`
	} else {
		query += ` AND verification_run_id = ?`
		args = append(args, update.VerificationRunID)
	}
	query += ` ORDER BY id LIMIT 1`

	var id int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetMemoryUpdate(ctx, id)
}

func (s *Store) UpdateMemoryUpdateStatus(ctx context.Context, id int64, status, reason string, appliedAt sql.NullTime) (*MemoryUpdate, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE memory_updates
		SET status = ?, reason = ?, applied_at = ?
		WHERE id = ?
	`, status, reason, nullableSQLTime(appliedAt), id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetMemoryUpdate(ctx, id)
}

func (s *Store) CreateFollowup(ctx context.Context, followup Followup) (*Followup, error) {
	now := nowTime()
	if followup.Status == "" {
		followup.Status = FollowupStatusPending
	}
	if followup.TriggerAt.IsZero() {
		followup.TriggerAt = now
	}
	if followup.CreatedAt.IsZero() {
		followup.CreatedAt = now
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO followups(task_id, goal_id, reason, status, trigger_at, created_task_id, dedupe_key, failure_reason, processed_at, wake_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(followup.TaskID), nullableInt(followup.GoalID), followup.Reason, followup.Status, timeMillis(followup.TriggerAt), nullableInt(followup.CreatedTaskID), followup.DedupeKey, followup.FailureReason, nullableSQLTime(followup.ProcessedAt), boolInt(followup.WakeAgent), timeMillis(followup.CreatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if followup.DedupeKey == "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE followups SET dedupe_key = ? WHERE id = ?`, followupDedupeKey(id), id); err != nil {
			return nil, err
		}
	}
	return s.GetFollowup(ctx, id)
}

func (s *Store) CreateOrGetFollowup(ctx context.Context, followup Followup) (*Followup, bool, error) {
	if followup.DedupeKey == "" {
		created, err := s.CreateFollowup(ctx, followup)
		return created, false, err
	}
	now := nowTime()
	if followup.Status == "" {
		followup.Status = FollowupStatusPending
	}
	if followup.TriggerAt.IsZero() {
		followup.TriggerAt = now
	}
	if followup.CreatedAt.IsZero() {
		followup.CreatedAt = now
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO followups(task_id, goal_id, reason, status, trigger_at, created_task_id, dedupe_key, failure_reason, processed_at, wake_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(followup.TaskID), nullableInt(followup.GoalID), followup.Reason, followup.Status, timeMillis(followup.TriggerAt), nullableInt(followup.CreatedTaskID), followup.DedupeKey, followup.FailureReason, nullableSQLTime(followup.ProcessedAt), boolInt(followup.WakeAgent), timeMillis(followup.CreatedAt))
	if err != nil {
		return nil, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		existing, err := s.GetFollowupByDedupeKey(ctx, followup.DedupeKey)
		return existing, true, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	created, err := s.GetFollowup(ctx, id)
	return created, false, err
}

func (s *Store) GetFollowup(ctx context.Context, id int64) (*Followup, error) {
	var followup Followup
	var taskID, goalID, createdTaskID sql.NullInt64
	var processedAt sql.NullInt64
	var triggerAt, createdAt int64
	var wakeAgent int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, goal_id, reason, status, trigger_at, created_task_id, dedupe_key, failure_reason, processed_at, wake_agent, created_at
		FROM followups WHERE id = ?
	`, id).Scan(&followup.ID, &taskID, &goalID, &followup.Reason, &followup.Status, &triggerAt, &createdTaskID, &followup.DedupeKey, &followup.FailureReason, &processedAt, &wakeAgent, &createdAt)
	if err != nil {
		return nil, err
	}
	followup.TaskID = intFromNull(taskID)
	followup.GoalID = intFromNull(goalID)
	followup.CreatedTaskID = intFromNull(createdTaskID)
	followup.ProcessedAt = nullTimeFromMillis(processedAt)
	followup.WakeAgent = wakeAgent != 0
	followup.TriggerAt = millisTime(triggerAt)
	followup.CreatedAt = millisTime(createdAt)
	return &followup, nil
}

func (s *Store) GetFollowupByDedupeKey(ctx context.Context, dedupeKey string) (*Followup, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM followups WHERE dedupe_key = ?`, dedupeKey).Scan(&id); err != nil {
		return nil, err
	}
	return s.GetFollowup(ctx, id)
}

func (s *Store) FindFollowupInCooldown(ctx context.Context, followup Followup, cooldown time.Duration) (*Followup, error) {
	if cooldown <= 0 {
		return nil, sql.ErrNoRows
	}
	triggerAt := timeMillis(followup.TriggerAt)
	window := cooldown.Milliseconds()
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM followups
		WHERE COALESCE(task_id, 0) = ?
			AND COALESCE(goal_id, 0) = ?
			AND reason = ?
			AND wake_agent = ?
			AND status NOT IN (?, ?)
			AND trigger_at BETWEEN ? AND ?
		ORDER BY ABS(trigger_at - ?), id
		LIMIT 1
	`, followup.TaskID, followup.GoalID, followup.Reason, boolInt(followup.WakeAgent), FollowupStatusFailed, FollowupStatusCanceled, triggerAt-window, triggerAt+window, triggerAt).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetFollowup(ctx, id)
}

func (s *Store) ListDueFollowups(ctx context.Context, now time.Time, limit int) ([]Followup, error) {
	query := `
		SELECT id, task_id, goal_id, reason, status, trigger_at, created_task_id, dedupe_key, failure_reason, processed_at, wake_agent, created_at
		FROM followups
		WHERE status IN (?, ?)
			AND trigger_at <= ?
		ORDER BY trigger_at, id
	`
	args := []any{FollowupStatusPending, FollowupStatusProcessing, timeMillis(now)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var followups []Followup
	for rows.Next() {
		followup, err := scanFollowup(rows)
		if err != nil {
			return nil, err
		}
		followups = append(followups, *followup)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return followups, nil
}

func (s *Store) MarkFollowupProcessing(ctx context.Context, id int64) (*Followup, error) {
	return s.updateFollowupStatus(ctx, id, FollowupStatusProcessing, "", sql.NullTime{}, 0)
}

func (s *Store) MarkFollowupCreated(ctx context.Context, id, createdTaskID int64) (*Followup, error) {
	return s.updateFollowupStatus(ctx, id, FollowupStatusCreated, "", sql.NullTime{Time: nowTime(), Valid: true}, createdTaskID)
}

func (s *Store) MarkFollowupSkipped(ctx context.Context, id int64, reason string) (*Followup, error) {
	return s.updateFollowupStatus(ctx, id, FollowupStatusSkipped, reason, sql.NullTime{Time: nowTime(), Valid: true}, 0)
}

func (s *Store) MarkFollowupFailed(ctx context.Context, id int64, reason string) (*Followup, error) {
	return s.updateFollowupStatus(ctx, id, FollowupStatusFailed, reason, sql.NullTime{Time: nowTime(), Valid: true}, 0)
}

func (s *Store) updateFollowupStatus(ctx context.Context, id int64, status, reason string, processedAt sql.NullTime, createdTaskID int64) (*Followup, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE followups
		SET status = ?,
			failure_reason = CASE WHEN ? != '' THEN ? ELSE failure_reason END,
			processed_at = CASE WHEN ? IS NOT NULL THEN ? ELSE processed_at END,
			created_task_id = CASE WHEN ? IS NOT NULL THEN ? ELSE created_task_id END
		WHERE id = ?
	`, status, reason, reason, nullableSQLTime(processedAt), nullableSQLTime(processedAt), nullableInt(createdTaskID), nullableInt(createdTaskID), id)
	if err != nil {
		return nil, err
	}
	return s.GetFollowup(ctx, id)
}

func scanFollowup(scanner rowScanner) (*Followup, error) {
	var followup Followup
	var taskID, goalID, createdTaskID, processedAt sql.NullInt64
	var triggerAt, createdAt int64
	var wakeAgent int
	if err := scanner.Scan(&followup.ID, &taskID, &goalID, &followup.Reason, &followup.Status, &triggerAt, &createdTaskID, &followup.DedupeKey, &followup.FailureReason, &processedAt, &wakeAgent, &createdAt); err != nil {
		return nil, err
	}
	followup.TaskID = intFromNull(taskID)
	followup.GoalID = intFromNull(goalID)
	followup.CreatedTaskID = intFromNull(createdTaskID)
	followup.ProcessedAt = nullTimeFromMillis(processedAt)
	followup.WakeAgent = wakeAgent != 0
	followup.TriggerAt = millisTime(triggerAt)
	followup.CreatedAt = millisTime(createdAt)
	return &followup, nil
}

func followupDedupeKey(id int64) string {
	return fmt.Sprintf("followup:%d:due", id)
}

func nowTime() time.Time {
	return time.Now().UTC()
}

func timeMillis(t time.Time) int64 {
	return t.UTC().UnixMilli()
}

func millisTime(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

func nullableInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func intFromNull(v sql.NullInt64) int64 {
	if !v.Valid {
		return 0
	}
	return v.Int64
}

func nullableSQLTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return timeMillis(t.Time)
}

func nullTimeFromMillis(v sql.NullInt64) sql.NullTime {
	if !v.Valid {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: millisTime(v.Int64), Valid: true}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
