package agentic

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

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
		"policy_decisions",
		"tool_action_receipts",
		"verification_runs",
		"memory_updates",
		"followups",
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
	})
	if err != nil {
		t.Fatalf("CreateMemoryUpdate: %v", err)
	}

	got, err := store.GetMemoryUpdate(ctx, update.ID)
	if err != nil {
		t.Fatalf("GetMemoryUpdate: %v", err)
	}
	if got.TaskID != task.ID || got.ReceiptID != receipt.ID || got.VerificationRunID != verification.ID || got.Status != MemoryUpdateStatusPending {
		t.Fatalf("unexpected memory update: %+v", got)
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
	actor, err := store.CreateAgentActor(ctx, AgentActor{
		TaskID:            taskID,
		Role:              "executor",
		StateJSON:         `{}`,
		InboxJSON:         `[]`,
		OutboxJSON:        `[]`,
		ToolAllowlistJSON: `[]`,
		BudgetJSON:        `{}`,
		Status:            "ready",
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
