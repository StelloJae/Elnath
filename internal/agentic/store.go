package agentic

import (
	"context"
	"database/sql"
	"time"
)

type Store struct {
	db *sql.DB
}

type ToolActionReceiptCompletion struct {
	OutputHash    string
	OutputSummary string
	Status        string
	Reversible    bool
	CompletedAt   sql.NullTime
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
		INSERT INTO tool_action_receipts(task_id, actor_id, policy_decision_id, approval_request_id, tool_name, input_hash, output_hash, output_summary, status, reversible, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, receipt.TaskID, nullableInt(receipt.ActorID), nullableInt(receipt.PolicyDecisionID), receipt.ApprovalRequestID, receipt.ToolName, receipt.InputHash, receipt.OutputHash, receipt.OutputSummary, receipt.Status, boolInt(receipt.Reversible), timeMillis(receipt.StartedAt), nullableSQLTime(receipt.CompletedAt))
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
		SET output_hash = ?, output_summary = ?, status = ?, reversible = ?, completed_at = ?
		WHERE id = ?
	`, completion.OutputHash, completion.OutputSummary, completion.Status, boolInt(completion.Reversible), nullableSQLTime(completedAt), id)
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
		SELECT id, task_id, actor_id, policy_decision_id, approval_request_id, tool_name, input_hash, output_hash, output_summary, status, reversible, started_at, completed_at
		FROM tool_action_receipts WHERE id = ?
	`, id).Scan(&receipt.ID, &receipt.TaskID, &actorID, &decisionID, &receipt.ApprovalRequestID, &receipt.ToolName, &receipt.InputHash, &receipt.OutputHash, &receipt.OutputSummary, &receipt.Status, &reversible, &startedAt, &completedAt)
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

func (s *Store) CreateMemoryUpdate(ctx context.Context, update MemoryUpdate) (*MemoryUpdate, error) {
	if update.CreatedAt.IsZero() {
		update.CreatedAt = nowTime()
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_updates(task_id, receipt_id, verification_run_id, target, operation, payload_hash, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, update.TaskID, nullableInt(update.ReceiptID), nullableInt(update.VerificationRunID), update.Target, update.Operation, update.PayloadHash, update.Status, timeMillis(update.CreatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetMemoryUpdate(ctx, id)
}

func (s *Store) GetMemoryUpdate(ctx context.Context, id int64) (*MemoryUpdate, error) {
	var update MemoryUpdate
	var receiptID, verificationRunID sql.NullInt64
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, receipt_id, verification_run_id, target, operation, payload_hash, status, created_at
		FROM memory_updates WHERE id = ?
	`, id).Scan(&update.ID, &update.TaskID, &receiptID, &verificationRunID, &update.Target, &update.Operation, &update.PayloadHash, &update.Status, &createdAt)
	if err != nil {
		return nil, err
	}
	update.ReceiptID = intFromNull(receiptID)
	update.VerificationRunID = intFromNull(verificationRunID)
	update.CreatedAt = millisTime(createdAt)
	return &update, nil
}

func (s *Store) CreateFollowup(ctx context.Context, followup Followup) (*Followup, error) {
	now := nowTime()
	if followup.TriggerAt.IsZero() {
		followup.TriggerAt = now
	}
	if followup.CreatedAt.IsZero() {
		followup.CreatedAt = now
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO followups(task_id, goal_id, reason, status, trigger_at, created_task_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, nullableInt(followup.TaskID), nullableInt(followup.GoalID), followup.Reason, followup.Status, timeMillis(followup.TriggerAt), nullableInt(followup.CreatedTaskID), timeMillis(followup.CreatedAt))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetFollowup(ctx, id)
}

func (s *Store) GetFollowup(ctx context.Context, id int64) (*Followup, error) {
	var followup Followup
	var taskID, goalID, createdTaskID sql.NullInt64
	var triggerAt, createdAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, goal_id, reason, status, trigger_at, created_task_id, created_at
		FROM followups WHERE id = ?
	`, id).Scan(&followup.ID, &taskID, &goalID, &followup.Reason, &followup.Status, &triggerAt, &createdTaskID, &createdAt)
	if err != nil {
		return nil, err
	}
	followup.TaskID = intFromNull(taskID)
	followup.GoalID = intFromNull(goalID)
	followup.CreatedTaskID = intFromNull(createdTaskID)
	followup.TriggerAt = millisTime(triggerAt)
	followup.CreatedAt = millisTime(createdAt)
	return &followup, nil
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
