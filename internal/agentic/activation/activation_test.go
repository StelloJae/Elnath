package activation

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"

	_ "modernc.org/sqlite"
)

func TestService_RunOnceProcessesFollowupsAndTriagesSignalsWithoutEnqueue(t *testing.T) {
	ctx := context.Background()
	db, store := newActivationTestStore(t)
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	parent := createActivationTask(t, ctx, store)
	fu, err := store.CreateFollowup(ctx, agentic.Followup{
		TaskID:    parent.ID,
		GoalID:    parent.GoalID,
		Reason:    "wake and propose bounded work",
		Status:    agentic.FollowupStatusPending,
		TriggerAt: time.Now().Add(-time.Hour),
		WakeAgent: true,
	})
	if err != nil {
		t.Fatalf("CreateFollowup: %v", err)
	}
	beforeQueue := activationCountRows(t, db, "task_queue")

	result, err := NewService(store).RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.ExecutionPolicy != ExecutionPolicyProposeOnly || result.EnqueuePerformed {
		t.Fatalf("result policy/enqueue = %+v", result)
	}
	if result.RunID == 0 || result.Status != agentic.ActivationRunStatusSucceeded {
		t.Fatalf("result run/status = %+v", result)
	}
	if result.Followups.Processed != 1 || result.Followups.Created != 1 {
		t.Fatalf("followup result = %+v", result.Followups)
	}
	if result.Signals.Processed != 1 {
		t.Fatalf("signal result = %+v, want one followup signal triaged", result.Signals)
	}
	got, err := store.GetFollowup(ctx, fu.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	if got.Status != agentic.FollowupStatusCreated || got.CreatedTaskID == 0 {
		t.Fatalf("followup after activation = %+v", got)
	}
	child, err := store.GetAgenticTask(ctx, got.CreatedTaskID)
	if err != nil {
		t.Fatalf("GetAgenticTask child: %v", err)
	}
	if child.Status != agentic.TaskStatusProposed || child.QueueTaskID != 0 || child.ParentID != parent.ID {
		t.Fatalf("child task = %+v, want proposed followup task without queue link", child)
	}
	if len(result.ProposedTaskIDs) != 1 || result.ProposedTaskIDs[0] != child.ID {
		t.Fatalf("proposed task ids = %+v, want child %d", result.ProposedTaskIDs, child.ID)
	}
	if _, err := queue.Get(ctx, child.QueueTaskID); err == nil {
		t.Fatalf("queue lookup unexpectedly succeeded for child queue_task_id=%d", child.QueueTaskID)
	}
	if afterQueue := activationCountRows(t, db, "task_queue"); afterQueue != beforeQueue {
		t.Fatalf("queue rows changed: before=%d after=%d", beforeQueue, afterQueue)
	}
	signal, err := store.GetGoalSignal(ctx, child.SignalID)
	if err != nil {
		t.Fatalf("GetGoalSignal: %v", err)
	}
	if signal.Status != agentic.SignalStatusTriaged {
		t.Fatalf("signal status = %q, want triaged", signal.Status)
	}
	run, err := store.GetActivationRun(ctx, result.RunID)
	if err != nil {
		t.Fatalf("GetActivationRun: %v", err)
	}
	if run.FollowupProcessed != 1 || run.FollowupCreated != 1 || run.SignalProcessed != 1 || run.EnqueuePerformed {
		t.Fatalf("activation run = %+v", run)
	}
	if len(run.ProposedTaskIDs) != 1 || run.ProposedTaskIDs[0] != child.ID {
		t.Fatalf("activation run proposed task ids = %+v, want child %d", run.ProposedTaskIDs, child.ID)
	}
}

func TestService_RunOnceDefaultsLimit(t *testing.T) {
	_, store := newActivationTestStore(t)
	result, err := NewService(store).RunOnce(context.Background(), 0)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Limit != 25 {
		t.Fatalf("limit = %d, want default 25", result.Limit)
	}
}

func TestService_RunOnceRecordsFailedActivation(t *testing.T) {
	ctx := context.Background()
	db, store := newActivationTestStore(t)
	if _, err := db.Exec(`DROP TABLE goal_signals`); err != nil {
		t.Fatalf("drop goal_signals: %v", err)
	}

	result, err := NewService(store).RunOnce(ctx, 3)
	if err == nil {
		t.Fatal("RunOnce error = nil, want failed signal listing")
	}
	if result.RunID == 0 || result.Status != agentic.ActivationRunStatusFailed || result.Reason == "" {
		t.Fatalf("failed result = %+v", result)
	}
	run, getErr := store.GetActivationRun(ctx, result.RunID)
	if getErr != nil {
		t.Fatalf("GetActivationRun: %v", getErr)
	}
	if run.Status != agentic.ActivationRunStatusFailed || run.Reason == "" || run.Limit != 3 {
		t.Fatalf("failed run = %+v", run)
	}
}

func newActivationTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
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
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return db, agentic.NewStore(db)
}

func createActivationTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	goal, err := store.CreateStandingGoal(ctx, agentic.StandingGoal{
		Title:         "Activation goal",
		Status:        agentic.GoalStatusActive,
		AutonomyLevel: agentic.AutonomyLevelObserve,
	})
	if err != nil {
		t.Fatalf("CreateStandingGoal: %v", err)
	}
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		GoalID:             goal.ID,
		Title:              "Verified parent task",
		Prompt:             "Parent finished with evidence.",
		Status:             agentic.TaskStatusSucceeded,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func activationCountRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
