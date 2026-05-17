package agentic

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/daemon"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	return db
}

func openConcurrentTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agentic.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open concurrent db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			t.Fatalf("exec %s: %v", pragma, err)
		}
	}
	return db
}

func newTestStore(t *testing.T) (*sql.DB, *Store) {
	t.Helper()
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return db, NewStore(db)
}

func TestAgenticSchema_InitIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema first: %v", err)
	}
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema second: %v", err)
	}
}

func TestAgenticSchema_TablesExist(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	for _, table := range []string{
		"standing_goals",
		"signal_watchers",
		"goal_signals",
		"agentic_tasks",
		"task_edges",
		"agent_actors",
		"actor_handoffs",
		"policy_decisions",
		"tool_action_receipts",
		"verification_runs",
		"completion_gates",
		"task_enqueue_decisions",
		"memory_updates",
		"followups",
		"activation_runs",
	} {
		t.Run(table, func(t *testing.T) {
			var name string
			err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
			if err != nil {
				t.Fatalf("table %s missing: %v", table, err)
			}
		})
	}
}

func TestAgenticSchema_RoadmapColumnsExist(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	required := map[string][]string{
		"signal_watchers": {
			"id", "goal_id", "source", "config_json", "enabled", "interval_s", "last_cursor", "created_at", "updated_at",
		},
		"agentic_tasks": {
			"id", "goal_id", "signal_id", "parent_id", "queue_task_id", "title", "prompt", "status", "priority",
			"risk_level", "autonomy_decision", "approval_request_id", "verification_status", "created_at", "updated_at", "due_at",
		},
		"agent_actors": {
			"id", "task_id", "role", "state_json", "inbox_json", "outbox_json", "tool_allowlist_json", "budget_json", "status", "created_at", "updated_at",
		},
		"actor_handoffs": {
			"id", "task_id", "from_actor_id", "to_actor_id", "handoff_type", "payload_json", "status", "created_at",
		},
		"policy_decisions": {
			"id", "task_id", "actor_id", "action_kind", "tool_name", "risk_level", "decision", "reason", "policy_version", "created_at",
		},
		"tool_action_receipts": {
			"id", "task_id", "actor_id", "policy_decision_id", "approval_request_id", "tool_name", "input_hash", "output_hash",
			"tool_call_id", "raw_output_hash", "visible_output_hash", "output_summary", "status", "failure_reason",
			"hook_provenance_json", "reversible", "started_at", "completed_at",
		},
		"verification_runs": {
			"id", "task_id", "verifier_actor_id", "criteria_json", "evidence_refs_json", "verdict", "reason", "created_at",
		},
		"completion_gates": {
			"id", "task_id", "queue_task_id", "verification_run_id", "status", "reason", "receipt_summary_json", "created_at", "updated_at",
		},
		"task_enqueue_decisions": {
			"id", "task_id", "queue_task_id", "operator_id", "decision", "reason", "requested_enforcement", "requested_completion_gate", "status", "failure_reason", "created_at", "updated_at",
		},
		"memory_updates": {
			"id", "task_id", "receipt_id", "verification_run_id", "target", "operation", "payload_hash", "status", "source", "reason", "created_at", "applied_at",
		},
		"activation_runs": {
			"id", "execution_policy", "limit_n", "followup_processed", "followup_created", "followup_skipped", "followup_failed",
			"signal_processed", "signal_created", "signal_linked", "signal_failed", "enqueue_performed", "proposed_task_ids_json", "status", "reason", "created_at",
		},
	}

	for table, columns := range required {
		t.Run(table, func(t *testing.T) {
			got := tableColumns(t, db, table)
			for _, col := range columns {
				if !got[col] {
					t.Fatalf("table %s missing column %s; got %v", table, col, got)
				}
			}
		})
	}
}

func TestActivationRunStore_CreateGetList(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	created, err := store.CreateActivationRun(ctx, ActivationRun{
		ExecutionPolicy:   "propose_only",
		Limit:             7,
		FollowupProcessed: 2,
		FollowupCreated:   1,
		FollowupSkipped:   1,
		SignalProcessed:   3,
		SignalCreated:     2,
		SignalLinked:      1,
		EnqueuePerformed:  false,
		ProposedTaskIDs:   []int64{101, 102},
		Status:            ActivationRunStatusSucceeded,
	})
	if err != nil {
		t.Fatalf("CreateActivationRun: %v", err)
	}
	if created.ID == 0 || created.Limit != 7 || created.FollowupProcessed != 2 || created.SignalLinked != 1 || created.EnqueuePerformed || len(created.ProposedTaskIDs) != 2 {
		t.Fatalf("created activation run = %+v", created)
	}
	got, err := store.GetActivationRun(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetActivationRun: %v", err)
	}
	if got.Status != ActivationRunStatusSucceeded || got.ExecutionPolicy != "propose_only" {
		t.Fatalf("activation run = %+v", got)
	}
	if len(got.ProposedTaskIDs) != 2 || got.ProposedTaskIDs[0] != 101 || got.ProposedTaskIDs[1] != 102 {
		t.Fatalf("activation run proposed task ids = %+v", got.ProposedTaskIDs)
	}
	runs, err := store.ListActivationRuns(ctx, 10)
	if err != nil {
		t.Fatalf("ListActivationRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != created.ID {
		t.Fatalf("activation runs = %+v, want created run", runs)
	}
}

func TestActivationRunsMigrationAddsProposedTaskIDsColumn(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE activation_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			execution_policy TEXT NOT NULL,
			limit_n INTEGER NOT NULL,
			followup_processed INTEGER NOT NULL DEFAULT 0,
			followup_created INTEGER NOT NULL DEFAULT 0,
			followup_skipped INTEGER NOT NULL DEFAULT 0,
			followup_failed INTEGER NOT NULL DEFAULT 0,
			signal_processed INTEGER NOT NULL DEFAULT 0,
			signal_created INTEGER NOT NULL DEFAULT 0,
			signal_linked INTEGER NOT NULL DEFAULT 0,
			signal_failed INTEGER NOT NULL DEFAULT 0,
			enqueue_performed INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create old activation_runs: %v", err)
	}
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if !tableColumns(t, db, "activation_runs")["proposed_task_ids_json"] {
		t.Fatalf("activation_runs missing proposed_task_ids_json after migration")
	}
}

func TestAgenticStore_CreateListTaskEnqueueDecision(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task, err := store.CreateAgenticTask(ctx, AgenticTask{
		Title:              "Proposed enqueue",
		Prompt:             "review and enqueue",
		Status:             TaskStatusProposed,
		RiskLevel:          RiskLevelLow,
		AutonomyDecision:   PolicyDecisionObserve,
		VerificationStatus: VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}

	decision, err := store.CreateTaskEnqueueDecision(ctx, TaskEnqueueDecision{
		TaskID:                  task.ID,
		OperatorID:              "operator:stello",
		Decision:                TaskEnqueueDecisionApproved,
		Reason:                  "operator approved bounded work",
		RequestedEnforcement:    "gateway",
		RequestedCompletionGate: "verification",
		Status:                  TaskEnqueueStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateTaskEnqueueDecision: %v", err)
	}
	if decision.ID == 0 || decision.TaskID != task.ID || decision.OperatorID != "operator:stello" || decision.Decision != TaskEnqueueDecisionApproved || decision.Status != TaskEnqueueStatusPending {
		t.Fatalf("unexpected enqueue decision: %+v", decision)
	}

	got, err := store.ListTaskEnqueueDecisionsByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListTaskEnqueueDecisionsByTask: %v", err)
	}
	if len(got) != 1 || got[0].ID != decision.ID || got[0].RequestedEnforcement != "gateway" || got[0].RequestedCompletionGate != "verification" {
		t.Fatalf("decisions = %+v, want created decision", got)
	}
}

func TestAgenticStore_MarkTaskEnqueueDecisionEnqueuedLinksTask(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task, err := store.CreateAgenticTask(ctx, AgenticTask{
		Title:              "Proposed enqueue",
		Prompt:             "review and enqueue",
		Status:             TaskStatusProposed,
		RiskLevel:          RiskLevelLow,
		AutonomyDecision:   PolicyDecisionObserve,
		VerificationStatus: VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	decision, err := store.CreateTaskEnqueueDecision(ctx, TaskEnqueueDecision{
		TaskID:   task.ID,
		Decision: TaskEnqueueDecisionApproved,
		Status:   TaskEnqueueStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateTaskEnqueueDecision: %v", err)
	}

	updatedDecision, updatedTask, err := store.MarkTaskEnqueueDecisionEnqueued(ctx, decision.ID, task.ID, 77)
	if err != nil {
		t.Fatalf("MarkTaskEnqueueDecisionEnqueued: %v", err)
	}
	if updatedDecision.QueueTaskID != 77 || updatedDecision.Status != TaskEnqueueStatusEnqueued {
		t.Fatalf("updated decision = %+v, want enqueued queue id", updatedDecision)
	}
	if updatedTask.QueueTaskID != 77 || updatedTask.Status != TaskStatusPending {
		t.Fatalf("updated task = %+v, want queue link and pending status", updatedTask)
	}
}

func TestAgenticSchema_ReconcilesDuplicateActiveTaskEnqueueDecisionsBeforeUniqueIndex(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE agentic_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER,
			signal_id INTEGER,
			parent_id INTEGER,
			queue_task_id INTEGER,
			title TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			risk_level TEXT NOT NULL,
			autonomy_decision TEXT NOT NULL,
			approval_request_id TEXT NOT NULL DEFAULT '',
			verification_status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			due_at INTEGER
		);
		CREATE TABLE task_enqueue_decisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			queue_task_id INTEGER,
			operator_id TEXT NOT NULL DEFAULT '',
			decision TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			requested_enforcement TEXT NOT NULL DEFAULT '',
			requested_completion_gate TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			failure_reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO agentic_tasks(id, title, prompt, status, risk_level, autonomy_decision, verification_status, created_at, updated_at)
		VALUES (1, 'dirty task', 'prompt', 'proposed', 'low', 'observe', 'pending', 1, 1);
		INSERT INTO task_enqueue_decisions(task_id, operator_id, decision, status, created_at, updated_at)
		VALUES
			(1, 'cli', 'approved', 'pending', 1, 1),
			(1, 'cli', 'approved', 'pending', 2, 2),
			(1, 'cli', 'approved', 'enqueued', 3, 3);
	`); err != nil {
		t.Fatalf("seed legacy duplicate enqueue decisions: %v", err)
	}
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	var active int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_enqueue_decisions WHERE task_id = 1 AND status IN ('pending','enqueued')`).Scan(&active); err != nil {
		t.Fatalf("count active decisions: %v", err)
	}
	if active != 1 {
		t.Fatalf("active decisions = %d, want 1", active)
	}
	var failed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_enqueue_decisions WHERE task_id = 1 AND status = 'failed' AND failure_reason <> ''`).Scan(&failed); err != nil {
		t.Fatalf("count failed decisions: %v", err)
	}
	if failed != 2 {
		t.Fatalf("failed duplicate decisions = %d, want 2", failed)
	}
}

func TestAgenticSchema_MigratesMemoryUpdateColumns(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE standing_goals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			autonomy_level TEXT NOT NULL,
			risk_budget TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE agentic_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			signal_id INTEGER,
			parent_id INTEGER,
			queue_task_id INTEGER,
			title TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			risk_level TEXT NOT NULL,
			autonomy_decision TEXT NOT NULL,
			approval_request_id TEXT NOT NULL DEFAULT '',
			verification_status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			due_at INTEGER
		);
		CREATE TABLE verification_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			verifier_actor_id INTEGER,
			criteria_json TEXT NOT NULL,
			evidence_refs_json TEXT NOT NULL,
			verdict TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		);
		CREATE TABLE memory_updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL REFERENCES agentic_tasks(id) ON DELETE CASCADE,
			receipt_id INTEGER,
			verification_run_id INTEGER REFERENCES verification_runs(id) ON DELETE SET NULL,
			target TEXT NOT NULL,
			operation TEXT NOT NULL,
			payload_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		INSERT INTO agentic_tasks(title, prompt, status, risk_level, autonomy_decision, verification_status, created_at, updated_at)
		VALUES ('legacy', 'legacy', 'succeeded', 'low', 'observe_only', 'passed', 1, 1);
		INSERT INTO verification_runs(task_id, criteria_json, evidence_refs_json, verdict, reason, created_at)
		VALUES (1, '{}', '[]', 'passed', 'legacy', 1);
		INSERT INTO memory_updates(task_id, verification_run_id, target, operation, payload_hash, status, created_at)
		VALUES (1, 1, 'wiki', 'append', 'abc', 'pending', 1);
	`); err != nil {
		t.Fatalf("seed legacy memory_updates: %v", err)
	}

	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	columns := tableColumns(t, db, "memory_updates")
	for _, col := range []string{"source", "reason", "applied_at"} {
		if !columns[col] {
			t.Fatalf("memory_updates missing migrated column %s; got %v", col, columns)
		}
	}
	got, err := NewStore(db).GetMemoryUpdate(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetMemoryUpdate legacy row: %v", err)
	}
	if got.Source != "" || got.Reason != "" || got.AppliedAt.Valid {
		t.Fatalf("unexpected migrated defaults: %+v", got)
	}
}

func TestAgenticStore_InsertReadStandingGoal(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)

	goal, err := store.CreateStandingGoal(ctx, StandingGoal{
		Title:         "Keep benchmark signal healthy",
		Description:   "Track canary evidence and repair signal quality.",
		Status:        GoalStatusActive,
		Priority:      7,
		AutonomyLevel: AutonomyLevelObserve,
		RiskBudget:    "low",
	})
	if err != nil {
		t.Fatalf("CreateStandingGoal: %v", err)
	}

	got, err := store.GetStandingGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetStandingGoal: %v", err)
	}
	if got.Title != goal.Title || got.Status != GoalStatusActive || got.Priority != 7 || got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("unexpected goal: %+v", got)
	}
}

func TestAgenticStore_ListStandingGoalsOrdersByUpdatedAt(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	base := time.Unix(1714478400, 0)
	for _, goal := range []StandingGoal{
		{Title: "older goal", Status: GoalStatusActive, Priority: 1, AutonomyLevel: AutonomyLevelObserve, RiskBudget: "low", CreatedAt: base, UpdatedAt: base},
		{Title: "newest goal", Status: GoalStatusActive, Priority: 3, AutonomyLevel: AutonomyLevelObserve, RiskBudget: "low", CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
		{Title: "middle goal", Status: GoalStatusActive, Priority: 2, AutonomyLevel: AutonomyLevelObserve, RiskBudget: "low", CreatedAt: base, UpdatedAt: base.Add(time.Hour)},
	} {
		if _, err := store.CreateStandingGoal(ctx, goal); err != nil {
			t.Fatalf("CreateStandingGoal(%q): %v", goal.Title, err)
		}
	}

	got, err := store.ListStandingGoals(ctx, 2)
	if err != nil {
		t.Fatalf("ListStandingGoals: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(goals) = %d, want 2", len(got))
	}
	if got[0].Title != "newest goal" || got[1].Title != "middle goal" {
		t.Fatalf("goal order = %q, %q; want newest, middle", got[0].Title, got[1].Title)
	}
}

func TestAgenticStore_InsertReadSignalWatcher(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	goal := createTestGoal(t, ctx, store)

	watcher, err := store.CreateSignalWatcher(ctx, SignalWatcher{
		GoalID:     goal.ID,
		Source:     "benchmark",
		ConfigJSON: `{"corpus":"month3-canary"}`,
		Enabled:    true,
		IntervalS:  3600,
		LastCursor: "cycle-006",
	})
	if err != nil {
		t.Fatalf("CreateSignalWatcher: %v", err)
	}

	got, err := store.GetSignalWatcher(ctx, watcher.ID)
	if err != nil {
		t.Fatalf("GetSignalWatcher: %v", err)
	}
	if got.GoalID != goal.ID || got.Source != "benchmark" || !got.Enabled || got.IntervalS != 3600 || got.LastCursor != "cycle-006" || got.CreatedAt.IsZero() {
		t.Fatalf("unexpected watcher: %+v", got)
	}
}

func TestSignalWatcher_InsertReadUpdateCursor(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	goal := createTestGoal(t, ctx, store)

	watcher, err := store.CreateSignalWatcher(ctx, SignalWatcher{
		GoalID:     goal.ID,
		Source:     "scheduler",
		ConfigJSON: `{"path":"scheduled_tasks.yaml"}`,
		Enabled:    true,
		IntervalS:  60,
		LastCursor: "before",
	})
	if err != nil {
		t.Fatalf("CreateSignalWatcher: %v", err)
	}

	updated, err := store.UpdateSignalWatcherCursor(ctx, watcher.ID, "after")
	if err != nil {
		t.Fatalf("UpdateSignalWatcherCursor: %v", err)
	}
	if updated.LastCursor != "after" || !updated.UpdatedAt.After(watcher.UpdatedAt) {
		t.Fatalf("unexpected updated watcher: %+v before=%+v", updated, watcher)
	}
}

func TestSignalWatcher_CreateOrGetAllowsNullableGoal(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)

	first, existed, err := store.CreateOrGetSignalWatcher(ctx, SignalWatcher{
		Source:     "scheduler",
		ConfigJSON: `{"bridge":"agentic_pr3","source":"scheduler"}`,
		Enabled:    true,
		LastCursor: "before",
	})
	if err != nil {
		t.Fatalf("CreateOrGetSignalWatcher first: %v", err)
	}
	if existed || first.GoalID != 0 {
		t.Fatalf("unexpected first watcher: existed=%v watcher=%+v", existed, first)
	}

	second, existed, err := store.CreateOrGetSignalWatcher(ctx, SignalWatcher{
		Source:     "scheduler",
		ConfigJSON: `{"bridge":"agentic_pr3","source":"scheduler"}`,
		Enabled:    true,
		LastCursor: "ignored",
	})
	if err != nil {
		t.Fatalf("CreateOrGetSignalWatcher second: %v", err)
	}
	if !existed || second.ID != first.ID || second.LastCursor != first.LastCursor {
		t.Fatalf("dedupe watcher = existed %v %+v, want original %+v", existed, second, first)
	}
}

func TestSignalWatcher_CreateOrGetConcurrentSingleton(t *testing.T) {
	ctx := context.Background()
	db := openConcurrentTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewStore(db)

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := store.CreateOrGetSignalWatcher(ctx, SignalWatcher{
				Source:     "scheduler",
				ConfigJSON: `{"bridge":"agentic_pr3","source":"scheduler"}`,
				Enabled:    true,
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("CreateOrGetSignalWatcher concurrent: %v", err)
		}
	}

	if got := countTableRows(t, db, "signal_watchers"); got != 1 {
		t.Fatalf("signal_watchers rows = %d, want 1", got)
	}
}

func TestAgenticStore_InsertReadGoalSignal(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	goal := createTestGoal(t, ctx, store)

	signal, err := store.CreateGoalSignal(ctx, GoalSignal{
		GoalID:      goal.ID,
		Source:      "benchmark",
		Type:        "canary_regression",
		PayloadJSON: `{"task":"GO-BF-002"}`,
		Fingerprint: "fp-go-bf-002",
		Severity:    4,
		Status:      SignalStatusNew,
		DedupeKey:   "canary:GO-BF-002",
	})
	if err != nil {
		t.Fatalf("CreateGoalSignal: %v", err)
	}

	got, err := store.GetGoalSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("GetGoalSignal: %v", err)
	}
	if got.GoalID != goal.ID || got.Type != "canary_regression" || got.PayloadJSON == "" || got.ObservedAt.IsZero() {
		t.Fatalf("unexpected signal: %+v", got)
	}
}

func TestGoalSignal_InsertReadNullableGoal(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)

	signal, err := store.CreateGoalSignal(ctx, GoalSignal{
		Source:      "manual",
		Type:        "daemon_submit",
		PayloadJSON: `{"prompt":"hello"}`,
		Fingerprint: "manual-submit-hello",
		Severity:    1,
		Status:      SignalStatusNew,
		DedupeKey:   "manual:1",
	})
	if err != nil {
		t.Fatalf("CreateGoalSignal nullable goal: %v", err)
	}

	got, err := store.GetGoalSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("GetGoalSignal: %v", err)
	}
	if got.GoalID != 0 || got.Source != "manual" || got.Type != "daemon_submit" || got.PayloadJSON == "" || got.ObservedAt.IsZero() {
		t.Fatalf("unexpected nullable-goal signal: %+v", got)
	}
}

func TestGoalSignal_DedupeByKey(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	goal := createTestGoal(t, ctx, store)

	first, existed, err := store.CreateOrGetGoalSignal(ctx, GoalSignal{
		GoalID:      goal.ID,
		Source:      "scheduler",
		Type:        "scheduled_task",
		PayloadJSON: `{"task":"task1"}`,
		Fingerprint: "first",
		Status:      SignalStatusNew,
		DedupeKey:   "scheduler:task1",
	})
	if err != nil {
		t.Fatalf("CreateOrGetGoalSignal first: %v", err)
	}
	if existed {
		t.Fatal("first signal unexpectedly reported deduped")
	}

	second, existed, err := store.CreateOrGetGoalSignal(ctx, GoalSignal{
		GoalID:      goal.ID,
		Source:      "scheduler",
		Type:        "scheduled_task",
		PayloadJSON: `{"task":"task1","changed":true}`,
		Fingerprint: "second",
		Status:      SignalStatusNew,
		DedupeKey:   "scheduler:task1",
	})
	if err != nil {
		t.Fatalf("CreateOrGetGoalSignal second: %v", err)
	}
	if !existed {
		t.Fatal("duplicate signal did not report deduped")
	}
	if second.ID != first.ID || second.Fingerprint != "first" {
		t.Fatalf("dedupe returned %+v, want original %+v", second, first)
	}
}

func TestAgenticSchema_MigratesDuplicateGoalSignalDedupeKeys(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE standing_goals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			autonomy_level TEXT NOT NULL,
			risk_budget TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE signal_watchers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			source TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			interval_s INTEGER NOT NULL DEFAULT 0,
			last_cursor TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE goal_signals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			watcher_id INTEGER REFERENCES signal_watchers(id) ON DELETE SET NULL,
			source TEXT NOT NULL,
			type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			severity INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			dedupe_key TEXT NOT NULL DEFAULT '',
			observed_at INTEGER NOT NULL
		);
		INSERT INTO goal_signals(id, goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at)
		VALUES
			(1, NULL, NULL, 'scheduler', 'scheduled_task', '{}', 'first', 1, 'new', 'scheduler:dup', 100),
			(2, NULL, NULL, 'scheduler', 'scheduled_task', '{}', 'second', 1, 'new', 'scheduler:dup', 101);
	`); err != nil {
		t.Fatalf("seed duplicate goal_signals: %v", err)
	}

	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema with duplicate goal_signals: %v", err)
	}

	var duplicateCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT COALESCE(goal_id, 0), source, type, dedupe_key, COUNT(*) n
			FROM goal_signals
			WHERE dedupe_key <> ''
			GROUP BY COALESCE(goal_id, 0), source, type, dedupe_key
			HAVING n > 1
		)
	`).Scan(&duplicateCount); err != nil {
		t.Fatalf("count duplicate dedupe keys: %v", err)
	}
	if duplicateCount != 0 {
		t.Fatalf("duplicate dedupe keys remain: %d", duplicateCount)
	}

	if _, _, err := NewStore(db).CreateOrGetGoalSignal(context.Background(), GoalSignal{
		Source:      "scheduler",
		Type:        "scheduled_task",
		PayloadJSON: `{}`,
		Fingerprint: "third",
		Status:      SignalStatusNew,
		DedupeKey:   "scheduler:dup",
	}); err != nil {
		t.Fatalf("CreateOrGetGoalSignal after dedupe migration: %v", err)
	}
}

func TestAgenticSchema_MigratesDuplicateSignalWatcherKeys(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE standing_goals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			autonomy_level TEXT NOT NULL,
			risk_budget TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE signal_watchers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			source TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			interval_s INTEGER NOT NULL DEFAULT 0,
			last_cursor TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO signal_watchers(id, goal_id, source, config_json, enabled, interval_s, last_cursor, created_at, updated_at)
		VALUES
			(1, NULL, 'scheduler', '{"bridge":"agentic_pr3","source":"scheduler"}', 1, 0, 'old-1', 100, 100),
			(2, NULL, 'scheduler', '{"bridge":"agentic_pr3","source":"scheduler"}', 1, 0, 'old-2', 101, 101);
	`); err != nil {
		t.Fatalf("seed duplicate signal_watchers: %v", err)
	}

	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema with duplicate signal_watchers: %v", err)
	}

	var duplicateCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT COALESCE(goal_id, 0), source, config_json, COUNT(*) n
			FROM signal_watchers
			GROUP BY COALESCE(goal_id, 0), source, config_json
			HAVING n > 1
		)
	`).Scan(&duplicateCount); err != nil {
		t.Fatalf("count duplicate watcher keys: %v", err)
	}
	if duplicateCount != 0 {
		t.Fatalf("duplicate watcher keys remain: %d", duplicateCount)
	}

	if _, _, err := NewStore(db).CreateOrGetSignalWatcher(context.Background(), SignalWatcher{
		Source:     "scheduler",
		ConfigJSON: `{"bridge":"agentic_pr3","source":"scheduler"}`,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("CreateOrGetSignalWatcher after watcher migration: %v", err)
	}
}

func TestAgenticSchema_MigratesDuplicateAgenticTaskSignalIDs(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE standing_goals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			autonomy_level TEXT NOT NULL,
			risk_budget TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE goal_signals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			watcher_id INTEGER,
			source TEXT NOT NULL,
			type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			severity INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			dedupe_key TEXT NOT NULL DEFAULT '',
			observed_at INTEGER NOT NULL
		);
		CREATE TABLE agentic_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER REFERENCES standing_goals(id) ON DELETE SET NULL,
			signal_id INTEGER REFERENCES goal_signals(id) ON DELETE SET NULL,
			parent_id INTEGER REFERENCES agentic_tasks(id) ON DELETE SET NULL,
			queue_task_id INTEGER,
			title TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			risk_level TEXT NOT NULL,
			autonomy_decision TEXT NOT NULL,
			approval_request_id TEXT NOT NULL DEFAULT '',
			verification_status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			due_at INTEGER
		);
		INSERT INTO goal_signals(id, goal_id, watcher_id, source, type, payload_json, fingerprint, severity, status, dedupe_key, observed_at)
		VALUES (1, NULL, NULL, 'ambient', 'ambient_boot_task', '{}', 'signal', 1, 'new', 'signal:1', 100);
		INSERT INTO agentic_tasks(id, signal_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at)
		VALUES
			(1, 1, 'first', 'first', 'proposed', 0, 'low', 'observe', '', 'pending', 100, 100),
			(2, 1, 'duplicate', 'duplicate', 'proposed', 0, 'low', 'observe', '', 'pending', 101, 101);
	`); err != nil {
		t.Fatalf("seed duplicate agentic_tasks signal_id: %v", err)
	}

	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema with duplicate agentic_tasks signal_id: %v", err)
	}

	var linked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agentic_tasks WHERE signal_id = 1`).Scan(&linked); err != nil {
		t.Fatalf("count linked tasks: %v", err)
	}
	if linked != 1 {
		t.Fatalf("signal_id duplicate cleanup kept %d links, want 1", linked)
	}
	if _, err := db.Exec(`
		INSERT INTO agentic_tasks(signal_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at)
		VALUES (1, 'should fail', 'should fail', 'proposed', 0, 'low', 'observe', '', 'pending', 102, 102)
	`); err == nil {
		t.Fatal("duplicate signal_id insert unexpectedly succeeded")
	}
}

func TestAgenticStore_InsertReadAgenticTask(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	goal := createTestGoal(t, ctx, store)
	signal := createTestSignal(t, ctx, store, goal.ID)

	task, err := store.CreateAgenticTask(ctx, AgenticTask{
		GoalID:             goal.ID,
		SignalID:           signal.ID,
		QueueTaskID:        42,
		Title:              "Diagnose GO-BF-002",
		Prompt:             "Inspect retained workspace.",
		Status:             TaskStatusPending,
		Priority:           5,
		RiskLevel:          RiskLevelLow,
		AutonomyDecision:   PolicyDecisionObserve,
		ApprovalRequestID:  "approval-123",
		VerificationStatus: VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}

	got, err := store.GetAgenticTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if got.GoalID != goal.ID || got.SignalID != signal.ID || got.QueueTaskID != 42 || got.ApprovalRequestID != "approval-123" || got.Status != TaskStatusPending {
		t.Fatalf("unexpected task: %+v", got)
	}
}

func TestAgenticStore_ListAgenticTasksFiltersStatusAndOrdersByUpdatedAt(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	base := time.Unix(1714478400, 0)
	for _, task := range []AgenticTask{
		{Title: "older proposed", Prompt: "older", Status: TaskStatusProposed, RiskLevel: RiskLevelLow, AutonomyDecision: PolicyDecisionObserve, VerificationStatus: VerificationStatusPending, CreatedAt: base, UpdatedAt: base},
		{Title: "running task", Prompt: "running", Status: TaskStatusRunning, RiskLevel: RiskLevelLow, AutonomyDecision: PolicyDecisionObserve, VerificationStatus: VerificationStatusPending, CreatedAt: base, UpdatedAt: base.Add(time.Hour)},
		{Title: "newer proposed", Prompt: "newer", Status: TaskStatusProposed, RiskLevel: RiskLevelLow, AutonomyDecision: PolicyDecisionObserve, VerificationStatus: VerificationStatusPending, CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
	} {
		if _, err := store.CreateAgenticTask(ctx, task); err != nil {
			t.Fatalf("CreateAgenticTask(%q): %v", task.Title, err)
		}
	}

	got, err := store.ListAgenticTasks(ctx, TaskStatusProposed, 2)
	if err != nil {
		t.Fatalf("ListAgenticTasks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(got))
	}
	if got[0].Title != "newer proposed" || got[1].Title != "older proposed" {
		t.Fatalf("task order = %q, %q; want newer, older proposed", got[0].Title, got[1].Title)
	}
}

func TestAgenticStore_InsertReadDaemonOriginTaskAllowsNullableGoalSignal(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)

	task, err := store.CreateAgenticTask(ctx, AgenticTask{
		QueueTaskID:        123,
		Title:              "Daemon task",
		Prompt:             "fix the failing test",
		Status:             TaskStatusRunning,
		Priority:           1,
		RiskLevel:          RiskLevelLow,
		AutonomyDecision:   PolicyDecisionObserve,
		VerificationStatus: VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask daemon-origin: %v", err)
	}

	got, err := store.GetAgenticTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if got.GoalID != 0 || got.SignalID != 0 || got.QueueTaskID != 123 || got.Status != TaskStatusRunning {
		t.Fatalf("unexpected daemon-origin task: %+v", got)
	}
}

func TestAgenticStore_GetByQueueTaskIDAndUpdateStatus(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)

	task, err := store.CreateAgenticTask(ctx, AgenticTask{
		QueueTaskID:        321,
		Title:              "Daemon task",
		Prompt:             "ship it",
		Status:             TaskStatusRunning,
		RiskLevel:          RiskLevelLow,
		AutonomyDecision:   PolicyDecisionObserve,
		VerificationStatus: VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}

	byQueue, err := store.GetAgenticTaskByQueueTaskID(ctx, 321)
	if err != nil {
		t.Fatalf("GetAgenticTaskByQueueTaskID: %v", err)
	}
	if byQueue.ID != task.ID {
		t.Fatalf("GetAgenticTaskByQueueTaskID ID = %d, want %d", byQueue.ID, task.ID)
	}

	updated, err := store.UpdateAgenticTaskStatus(ctx, task.ID, TaskStatusSucceeded)
	if err != nil {
		t.Fatalf("UpdateAgenticTaskStatus: %v", err)
	}
	if updated.Status != TaskStatusSucceeded || !updated.UpdatedAt.After(task.UpdatedAt) {
		t.Fatalf("unexpected updated task: %+v before=%+v", updated, task)
	}
}

func TestAgenticStore_InsertReadTaskEdge(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	parent := createTestTask(t, ctx, store)
	child := createTestTask(t, ctx, store)

	edge, err := store.CreateTaskEdge(ctx, TaskEdge{
		ParentID: parent.ID,
		ChildID:  child.ID,
		EdgeType: "depends_on",
	})
	if err != nil {
		t.Fatalf("CreateTaskEdge: %v", err)
	}

	got, err := store.GetTaskEdge(ctx, edge.ParentID, edge.ChildID, edge.EdgeType)
	if err != nil {
		t.Fatalf("GetTaskEdge: %v", err)
	}
	if got.ParentID != parent.ID || got.ChildID != child.ID || got.EdgeType != "depends_on" || got.CreatedAt.IsZero() {
		t.Fatalf("unexpected edge: %+v", got)
	}
}

func TestAgenticStore_ListTaskEdgesByParent(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	parent := createTestTask(t, ctx, store)
	childOne := createTestTask(t, ctx, store)
	childTwo := createTestTask(t, ctx, store)
	otherParent := createTestTask(t, ctx, store)
	otherChild := createTestTask(t, ctx, store)

	for _, edge := range []TaskEdge{
		{ParentID: parent.ID, ChildID: childOne.ID, EdgeType: "delegates_to"},
		{ParentID: parent.ID, ChildID: childTwo.ID, EdgeType: "delegates_to"},
		{ParentID: parent.ID, ChildID: otherChild.ID, EdgeType: "depends_on"},
		{ParentID: otherParent.ID, ChildID: otherChild.ID, EdgeType: "delegates_to"},
	} {
		if _, err := store.CreateTaskEdge(ctx, edge); err != nil {
			t.Fatalf("CreateTaskEdge: %v", err)
		}
	}

	edges, err := store.ListTaskEdgesByParent(ctx, parent.ID, "delegates_to")
	if err != nil {
		t.Fatalf("ListTaskEdgesByParent: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("edges = %+v, want two delegates_to edges", edges)
	}
	if edges[0].ChildID != childOne.ID || edges[1].ChildID != childTwo.ID {
		t.Fatalf("edges = %+v, want child ids in insertion order", edges)
	}
}

func TestAgenticStore_InsertReadAgentActor(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)

	actor, err := store.CreateAgentActor(ctx, AgentActor{
		TaskID:            task.ID,
		Role:              "verifier",
		StateJSON:         `{"phase":"idle"}`,
		InboxJSON:         `[]`,
		OutboxJSON:        `[]`,
		ToolAllowlistJSON: `["bash"]`,
		BudgetJSON:        `{"tool_calls":3}`,
		Status:            "ready",
	})
	if err != nil {
		t.Fatalf("CreateAgentActor: %v", err)
	}

	got, err := store.GetAgentActor(ctx, actor.ID)
	if err != nil {
		t.Fatalf("GetAgentActor: %v", err)
	}
	if got.TaskID != task.ID || got.Role != "verifier" || got.StateJSON == "" || got.ToolAllowlistJSON == "" || got.Status != "ready" {
		t.Fatalf("unexpected actor: %+v", got)
	}
}

func TestActorStore_ListAndUpdateActorLifecycle(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)

	planner, err := store.CreateAgentActor(ctx, AgentActor{
		TaskID:            task.ID,
		Role:              ActorRolePlanner,
		StateJSON:         `{"phase":"created"}`,
		InboxJSON:         `[{"kind":"task"}]`,
		OutboxJSON:        `[]`,
		ToolAllowlistJSON: `["read"]`,
		BudgetJSON:        `{"max_iterations":10}`,
		Status:            ActorStatusCreated,
	})
	if err != nil {
		t.Fatalf("CreateAgentActor planner: %v", err)
	}
	if _, err := store.CreateAgentActor(ctx, AgentActor{
		TaskID:            task.ID,
		Role:              ActorRoleExecutor,
		StateJSON:         `{}`,
		InboxJSON:         `[]`,
		OutboxJSON:        `[]`,
		ToolAllowlistJSON: `["read"]`,
		BudgetJSON:        `{"max_iterations":5}`,
		Status:            ActorStatusCreated,
	}); err != nil {
		t.Fatalf("CreateAgentActor executor: %v", err)
	}

	running, err := store.UpdateAgentActor(ctx, AgentActor{
		ID:                planner.ID,
		TaskID:            task.ID,
		Role:              ActorRolePlanner,
		StateJSON:         `{"phase":"planning"}`,
		InboxJSON:         `[{"kind":"task"}]`,
		OutboxJSON:        `[]`,
		ToolAllowlistJSON: `["read"]`,
		BudgetJSON:        `{"max_iterations":10}`,
		Status:            ActorStatusRunning,
	})
	if err != nil {
		t.Fatalf("UpdateAgentActor running: %v", err)
	}
	if running.Status != ActorStatusRunning || running.StateJSON != `{"phase":"planning"}` {
		t.Fatalf("unexpected running actor: %+v", running)
	}

	done, err := store.UpdateAgentActor(ctx, AgentActor{
		ID:                planner.ID,
		TaskID:            task.ID,
		Role:              ActorRolePlanner,
		StateJSON:         `{"phase":"planned"}`,
		InboxJSON:         `[{"kind":"task"}]`,
		OutboxJSON:        `[{"kind":"subtask","count":2}]`,
		ToolAllowlistJSON: `["read"]`,
		BudgetJSON:        `{"max_iterations":10}`,
		Status:            ActorStatusSucceeded,
	})
	if err != nil {
		t.Fatalf("UpdateAgentActor succeeded: %v", err)
	}
	if !done.UpdatedAt.After(planner.UpdatedAt) {
		t.Fatalf("updated timestamp did not advance: before=%s after=%s", planner.UpdatedAt, done.UpdatedAt)
	}

	actors, err := store.ListAgentActorsByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListAgentActorsByTask: %v", err)
	}
	if len(actors) != 2 {
		t.Fatalf("actors = %+v, want 2", actors)
	}
	if actors[0].Role != ActorRolePlanner || actors[0].Status != ActorStatusSucceeded {
		t.Fatalf("first actor = %+v, want succeeded planner", actors[0])
	}
	if actors[1].Role != ActorRoleExecutor {
		t.Fatalf("second actor = %+v, want executor", actors[1])
	}
}

func TestActorStore_CreateAndListHandoffs(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)
	planner := createTestActorWithRole(t, ctx, store, task.ID, ActorRolePlanner)
	executor := createTestActorWithRole(t, ctx, store, task.ID, ActorRoleExecutor)

	handoff, err := store.CreateActorHandoff(ctx, ActorHandoff{
		TaskID:      task.ID,
		FromActorID: planner.ID,
		ToActorID:   executor.ID,
		HandoffType: "planner_to_executor",
		PayloadJSON: `{"subtask_id":1,"title":"Inspect"}`,
		Status:      ActorStatusCreated,
	})
	if err != nil {
		t.Fatalf("CreateActorHandoff: %v", err)
	}
	if handoff.ID == 0 || handoff.CreatedAt.IsZero() {
		t.Fatalf("unexpected handoff: %+v", handoff)
	}

	handoffs, err := store.ListActorHandoffsByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListActorHandoffsByTask: %v", err)
	}
	if len(handoffs) != 1 {
		t.Fatalf("handoffs = %+v, want 1", handoffs)
	}
	got := handoffs[0]
	if got.FromActorID != planner.ID || got.ToActorID != executor.ID || got.HandoffType != "planner_to_executor" || got.PayloadJSON == "" {
		t.Fatalf("unexpected handoff: %+v", got)
	}
}

func TestAgenticStore_InsertReadPolicyDecision(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)
	actor := createTestActor(t, ctx, store, task.ID)

	decision, err := store.CreatePolicyDecision(ctx, PolicyDecisionRecord{
		TaskID:        task.ID,
		ActorID:       actor.ID,
		ActionKind:    "tool_call",
		ToolName:      "bash",
		RiskLevel:     RiskLevelMedium,
		Decision:      PolicyDecisionRequireApproval,
		Reason:        "mutating shell command",
		PolicyVersion: "agentic-pr1",
	})
	if err != nil {
		t.Fatalf("CreatePolicyDecision: %v", err)
	}

	got, err := store.GetPolicyDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetPolicyDecision: %v", err)
	}
	if got.TaskID != task.ID || got.ActorID != actor.ID || got.ToolName != "bash" || got.Decision != PolicyDecisionRequireApproval {
		t.Fatalf("unexpected decision: %+v", got)
	}
}

func TestAgenticStore_InsertReadToolActionReceipt(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)
	decision := createTestPolicyDecision(t, ctx, store, task.ID)

	receipt, err := store.CreateToolActionReceipt(ctx, ToolActionReceipt{
		TaskID:           task.ID,
		PolicyDecisionID: decision.ID,
		ToolName:         "bash",
		InputHash:        "input-sha256",
		OutputHash:       "output-sha256",
		OutputSummary:    "tests passed",
		Status:           ReceiptStatusSucceeded,
		Reversible:       true,
	})
	if err != nil {
		t.Fatalf("CreateToolActionReceipt: %v", err)
	}

	got, err := store.GetToolActionReceipt(ctx, receipt.ID)
	if err != nil {
		t.Fatalf("GetToolActionReceipt: %v", err)
	}
	if got.TaskID != task.ID || got.PolicyDecisionID != decision.ID || got.Status != ReceiptStatusSucceeded || !got.Reversible {
		t.Fatalf("unexpected receipt: %+v", got)
	}
}

func TestAgenticStore_ToolActionReceiptLifecycle(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)
	decision := createTestPolicyDecision(t, ctx, store, task.ID)

	receipt, err := store.CreateToolActionReceipt(ctx, ToolActionReceipt{
		TaskID:           task.ID,
		PolicyDecisionID: decision.ID,
		ToolName:         "bash",
		InputHash:        "input-sha256",
		Status:           ReceiptStatusStarted,
	})
	if err != nil {
		t.Fatalf("CreateToolActionReceipt: %v", err)
	}
	if receipt.OutputHash != "" || receipt.CompletedAt.Valid {
		t.Fatalf("new receipt should not require output before completion: %+v", receipt)
	}

	completed, err := store.CompleteToolActionReceipt(ctx, receipt.ID, ToolActionReceiptCompletion{
		OutputHash:    "output-sha256",
		OutputSummary: "command completed",
		Status:        ReceiptStatusSucceeded,
		Reversible:    true,
	})
	if err != nil {
		t.Fatalf("CompleteToolActionReceipt: %v", err)
	}
	if completed.OutputHash != "output-sha256" || completed.OutputSummary == "" || completed.Status != ReceiptStatusSucceeded || !completed.Reversible || !completed.CompletedAt.Valid {
		t.Fatalf("unexpected completed receipt: %+v", completed)
	}
}

func TestAgenticStore_InsertReadVerificationRun(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)

	run, err := store.CreateVerificationRun(ctx, VerificationRun{
		TaskID:           task.ID,
		CriteriaJSON:     `["go test ./..."]`,
		EvidenceRefsJSON: `["benchmarks/results/current.json"]`,
		Verdict:          VerificationVerdictPass,
		Reason:           "all focused checks passed",
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}

	got, err := store.GetVerificationRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetVerificationRun: %v", err)
	}
	if got.TaskID != task.ID || got.Verdict != VerificationVerdictPass || got.CriteriaJSON == "" || got.CreatedAt.IsZero() {
		t.Fatalf("unexpected verification run: %+v", got)
	}
}

func TestAgenticStore_InsertReadCompletionGate(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)
	run := createTestVerificationRun(t, ctx, store, task.ID)

	gate, err := store.CreateCompletionGate(ctx, CompletionGate{
		TaskID:             task.ID,
		QueueTaskID:        42,
		VerificationRunID:  run.ID,
		Status:             CompletionGateStatusPassed,
		Reason:             "verification passed",
		ReceiptSummaryJSON: `{"started":0}`,
	})
	if err != nil {
		t.Fatalf("CreateCompletionGate: %v", err)
	}

	got, err := store.GetCompletionGate(ctx, gate.ID)
	if err != nil {
		t.Fatalf("GetCompletionGate: %v", err)
	}
	if got.TaskID != task.ID || got.QueueTaskID != 42 || got.VerificationRunID != run.ID || got.Status != CompletionGateStatusPassed {
		t.Fatalf("unexpected completion gate: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("completion gate timestamps not set: %+v", got)
	}
}

func TestAgenticStore_ListCompletionGatesByTask(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)
	other := createTestTask(t, ctx, store)

	for _, req := range []CompletionGate{
		{TaskID: task.ID, QueueTaskID: 10, Status: CompletionGateStatusBlocked, Reason: "missing verifier", ReceiptSummaryJSON: `{}`},
		{TaskID: task.ID, QueueTaskID: 11, Status: CompletionGateStatusPassed, Reason: "passed", ReceiptSummaryJSON: `{}`},
		{TaskID: other.ID, QueueTaskID: 12, Status: CompletionGateStatusBlocked, Reason: "other", ReceiptSummaryJSON: `{}`},
	} {
		if _, err := store.CreateCompletionGate(ctx, req); err != nil {
			t.Fatalf("CreateCompletionGate: %v", err)
		}
	}

	got, err := store.ListCompletionGatesByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListCompletionGatesByTask: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("completion gates = %+v, want two for task", got)
	}
	if got[0].QueueTaskID != 10 || got[1].QueueTaskID != 11 {
		t.Fatalf("completion gates ordered unexpectedly: %+v", got)
	}
}

func TestAgenticStore_InsertReadMemoryUpdate(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)
	decision := createTestPolicyDecision(t, ctx, store, task.ID)
	receipt := createTestReceipt(t, ctx, store, task.ID, decision.ID)
	verification := createTestVerificationRun(t, ctx, store, task.ID)

	update, err := store.CreateMemoryUpdate(ctx, MemoryUpdate{
		TaskID:            task.ID,
		ReceiptID:         receipt.ID,
		VerificationRunID: verification.ID,
		Target:            "wiki",
		Operation:         "append",
		PayloadHash:       "payload-sha256",
		Status:            MemoryUpdateStatusPending,
		Source:            "agentic",
		Reason:            "waiting for target write",
	})
	if err != nil {
		t.Fatalf("CreateMemoryUpdate: %v", err)
	}

	got, err := store.GetMemoryUpdate(ctx, update.ID)
	if err != nil {
		t.Fatalf("GetMemoryUpdate: %v", err)
	}
	if got.TaskID != task.ID || got.ReceiptID != receipt.ID || got.VerificationRunID != verification.ID || got.Status != MemoryUpdateStatusPending || got.Source != "agentic" || got.Reason == "" {
		t.Fatalf("unexpected memory update: %+v", got)
	}
}

func TestAgenticStore_MemoryUpdateConcurrentSingleton(t *testing.T) {
	ctx := context.Background()
	db := openConcurrentTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewStore(db)
	task := createTestTask(t, ctx, store)
	verification := createTestVerificationRun(t, ctx, store, task.ID)

	const workers = 12
	var wg sync.WaitGroup
	ids := make(chan int64, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			update, err := store.CreateMemoryUpdate(ctx, MemoryUpdate{
				TaskID:            task.ID,
				VerificationRunID: verification.ID,
				Target:            "learning.lesson",
				Operation:         "append",
				PayloadHash:       "shared-payload-sha256",
				Status:            MemoryUpdateStatusPending,
				Source:            "agentic",
				Reason:            "verification passed",
			})
			if err != nil {
				errs <- err
				return
			}
			ids <- update.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("CreateMemoryUpdate concurrent: %v", err)
		}
	}
	var first int64
	for id := range ids {
		if first == 0 {
			first = id
			continue
		}
		if id != first {
			t.Fatalf("memory update id = %d, want singleton id %d", id, first)
		}
	}
	if got := countTableRows(t, db, "memory_updates"); got != 1 {
		t.Fatalf("memory_updates rows = %d, want 1", got)
	}
}

func TestAgenticStore_InsertReadFollowup(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	task := createTestTask(t, ctx, store)

	followup, err := store.CreateFollowup(ctx, Followup{
		TaskID: task.ID,
		GoalID: task.GoalID,
		Reason: "rerun full canary after targeted fix",
		Status: FollowupStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateFollowup: %v", err)
	}

	got, err := store.GetFollowup(ctx, followup.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	if got.TaskID != task.ID || got.GoalID != task.GoalID || got.Status != FollowupStatusPending || got.TriggerAt.IsZero() {
		t.Fatalf("unexpected followup: %+v", got)
	}
}

func TestAgenticSchema_DoesNotBreakExistingRuntimeSchema(t *testing.T) {
	db := openTestDB(t)
	if err := conversation.InitSchema(db); err != nil {
		t.Fatalf("conversation InitSchema before agentic: %v", err)
	}
	if _, err := daemon.NewQueue(db); err != nil {
		t.Fatalf("daemon queue before agentic: %v", err)
	}
	if err := InitSchema(db); err != nil {
		t.Fatalf("agentic InitSchema: %v", err)
	}
	if err := conversation.InitSchema(db); err != nil {
		t.Fatalf("conversation InitSchema after agentic: %v", err)
	}
	if _, err := daemon.NewQueue(db); err != nil {
		t.Fatalf("daemon queue after agentic: %v", err)
	}
}

func TestAgenticSchema_EnforcesForeignKeys(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	_, err := store.CreateGoalSignal(ctx, GoalSignal{
		GoalID:      999,
		Source:      "missing",
		Type:        "orphan",
		PayloadJSON: `{}`,
		Fingerprint: "orphan",
		Status:      SignalStatusNew,
	})
	if err == nil {
		t.Fatal("CreateGoalSignal with missing goal unexpectedly succeeded")
	}
}

func TestAgenticSchema_MigratesPR1AgenticTaskGoalNullable(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE standing_goals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			autonomy_level TEXT NOT NULL,
			risk_budget TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
			);
			CREATE TABLE signal_watchers (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				goal_id INTEGER NOT NULL REFERENCES standing_goals(id) ON DELETE CASCADE,
				source TEXT NOT NULL,
				config_json TEXT NOT NULL DEFAULT '{}',
				enabled INTEGER NOT NULL DEFAULT 1,
				interval_s INTEGER NOT NULL DEFAULT 0,
				last_cursor TEXT NOT NULL DEFAULT '',
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			);
			CREATE TABLE goal_signals (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				goal_id INTEGER NOT NULL REFERENCES standing_goals(id) ON DELETE CASCADE,
				watcher_id INTEGER REFERENCES signal_watchers(id) ON DELETE SET NULL,
				source TEXT NOT NULL,
				type TEXT NOT NULL,
				payload_json TEXT NOT NULL,
				fingerprint TEXT NOT NULL,
				severity INTEGER NOT NULL DEFAULT 0,
				status TEXT NOT NULL,
				dedupe_key TEXT NOT NULL DEFAULT '',
				observed_at INTEGER NOT NULL
			);
			CREATE TABLE agentic_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			goal_id INTEGER NOT NULL REFERENCES standing_goals(id) ON DELETE CASCADE,
			signal_id INTEGER REFERENCES goal_signals(id) ON DELETE SET NULL,
			parent_id INTEGER REFERENCES agentic_tasks(id) ON DELETE SET NULL,
			queue_task_id INTEGER,
			title TEXT NOT NULL,
			prompt TEXT NOT NULL,
			status TEXT NOT NULL,
			priority INTEGER NOT NULL DEFAULT 0,
			risk_level TEXT NOT NULL,
			autonomy_decision TEXT NOT NULL,
			approval_request_id TEXT NOT NULL DEFAULT '',
			verification_status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			due_at INTEGER
		);
		`); err != nil {
		t.Fatalf("create PR1-like schema: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO standing_goals(id, title, description, status, priority, autonomy_level, risk_budget, created_at, updated_at)
		VALUES (1, 'Existing goal', 'before PR2', 'active', 5, 'observe', 'low', 100, 100);
		INSERT INTO agentic_tasks(id, goal_id, queue_task_id, title, prompt, status, priority, risk_level, autonomy_decision, approval_request_id, verification_status, created_at, updated_at)
		VALUES (7, 1, 77, 'Existing task', 'keep this row', 'running', 3, 'low', 'observe', '', 'pending', 110, 110);
	`); err != nil {
		t.Fatalf("seed PR1-like agentic task: %v", err)
	}
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema migration: %v", err)
	}

	store := NewStore(db)
	preserved, err := store.GetAgenticTask(ctx, 7)
	if err != nil {
		t.Fatalf("GetAgenticTask preserved row: %v", err)
	}
	if preserved.GoalID != 1 || preserved.QueueTaskID != 77 || preserved.Title != "Existing task" || preserved.Prompt != "keep this row" {
		t.Fatalf("migration did not preserve existing task: %+v", preserved)
	}

	_, err = store.CreateAgenticTask(ctx, AgenticTask{
		QueueTaskID:        44,
		Title:              "Daemon task",
		Prompt:             "nullable goal migration",
		Status:             TaskStatusRunning,
		RiskLevel:          RiskLevelLow,
		AutonomyDecision:   PolicyDecisionObserve,
		VerificationStatus: VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask after migration: %v", err)
	}

	_, err = store.CreateGoalSignal(ctx, GoalSignal{
		Source:      "manual",
		Type:        "daemon_submit",
		PayloadJSON: `{"prompt":"nullable signal migration"}`,
		Fingerprint: "nullable-signal-migration",
		Status:      SignalStatusNew,
		DedupeKey:   "manual:44",
	})
	if err != nil {
		t.Fatalf("CreateGoalSignal with nullable goal after migration: %v", err)
	}
}

func createTestGoal(t *testing.T, ctx context.Context, store *Store) *StandingGoal {
	t.Helper()
	goal, err := store.CreateStandingGoal(ctx, StandingGoal{
		Title:         "Goal",
		Description:   "desc",
		Status:        GoalStatusActive,
		Priority:      1,
		AutonomyLevel: AutonomyLevelObserve,
		RiskBudget:    "low",
	})
	if err != nil {
		t.Fatalf("CreateStandingGoal: %v", err)
	}
	return goal
}

func createTestSignal(t *testing.T, ctx context.Context, store *Store, goalID int64) *GoalSignal {
	t.Helper()
	signal, err := store.CreateGoalSignal(ctx, GoalSignal{
		GoalID:      goalID,
		Source:      "test",
		Type:        "test_signal",
		PayloadJSON: `{}`,
		Fingerprint: "fp",
		Status:      SignalStatusNew,
	})
	if err != nil {
		t.Fatalf("CreateGoalSignal: %v", err)
	}
	return signal
}

func createTestTask(t *testing.T, ctx context.Context, store *Store) *AgenticTask {
	t.Helper()
	goal := createTestGoal(t, ctx, store)
	signal := createTestSignal(t, ctx, store, goal.ID)
	task, err := store.CreateAgenticTask(ctx, AgenticTask{
		GoalID:             goal.ID,
		SignalID:           signal.ID,
		Title:              "Task",
		Prompt:             "Prompt",
		Status:             TaskStatusPending,
		Priority:           1,
		RiskLevel:          RiskLevelLow,
		AutonomyDecision:   PolicyDecisionObserve,
		VerificationStatus: VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func createTestPolicyDecision(t *testing.T, ctx context.Context, store *Store, taskID int64) *PolicyDecisionRecord {
	t.Helper()
	decision, err := store.CreatePolicyDecision(ctx, PolicyDecisionRecord{
		TaskID:        taskID,
		ActionKind:    "tool_call",
		ToolName:      "bash",
		RiskLevel:     RiskLevelLow,
		Decision:      PolicyDecisionObserve,
		Reason:        "test",
		PolicyVersion: "test",
	})
	if err != nil {
		t.Fatalf("CreatePolicyDecision: %v", err)
	}
	return decision
}

func createTestActor(t *testing.T, ctx context.Context, store *Store, taskID int64) *AgentActor {
	t.Helper()
	return createTestActorWithRole(t, ctx, store, taskID, "executor")
}

func createTestActorWithRole(t *testing.T, ctx context.Context, store *Store, taskID int64, role string) *AgentActor {
	t.Helper()
	actor, err := store.CreateAgentActor(ctx, AgentActor{
		TaskID:            taskID,
		Role:              role,
		StateJSON:         `{}`,
		InboxJSON:         `[]`,
		OutboxJSON:        `[]`,
		ToolAllowlistJSON: `[]`,
		BudgetJSON:        `{}`,
		Status:            ActorStatusCreated,
	})
	if err != nil {
		t.Fatalf("CreateAgentActor: %v", err)
	}
	return actor
}

func createTestReceipt(t *testing.T, ctx context.Context, store *Store, taskID, decisionID int64) *ToolActionReceipt {
	t.Helper()
	receipt, err := store.CreateToolActionReceipt(ctx, ToolActionReceipt{
		TaskID:           taskID,
		PolicyDecisionID: decisionID,
		ToolName:         "bash",
		InputHash:        "input",
		OutputHash:       "output",
		OutputSummary:    "ok",
		Status:           ReceiptStatusSucceeded,
	})
	if err != nil {
		t.Fatalf("CreateToolActionReceipt: %v", err)
	}
	return receipt
}

func createTestVerificationRun(t *testing.T, ctx context.Context, store *Store, taskID int64) *VerificationRun {
	t.Helper()
	run, err := store.CreateVerificationRun(ctx, VerificationRun{
		TaskID:           taskID,
		CriteriaJSON:     `[]`,
		EvidenceRefsJSON: `[]`,
		Verdict:          VerificationVerdictPass,
		Reason:           "test",
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}
	return run
}

func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows %s: %v", table, err)
	}
	return columns
}

func countTableRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func assertNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("error = %v, want sql.ErrNoRows", err)
	}
}
