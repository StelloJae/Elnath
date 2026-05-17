package activation

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	agenticenqueue "github.com/stello/elnath/internal/agentic/enqueue"
	"github.com/stello/elnath/internal/config"
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

func TestService_RunOnceAutoEnqueuesLowRiskWhenConfigured(t *testing.T) {
	ctx := context.Background()
	db, store := newActivationTestStore(t)
	queue, err := daemon.NewQueueNoRecover(db)
	if err != nil {
		t.Fatalf("NewQueueNoRecover: %v", err)
	}
	parent := createActivationTask(t, ctx, store)
	if _, err := store.CreateFollowup(ctx, agentic.Followup{
		TaskID:    parent.ID,
		GoalID:    parent.GoalID,
		Reason:    "wake and enqueue bounded work",
		Status:    agentic.FollowupStatusPending,
		TriggerAt: time.Now().Add(-time.Hour),
		WakeAgent: true,
	}); err != nil {
		t.Fatalf("CreateFollowup: %v", err)
	}
	beforeQueue := activationCountRows(t, db, "task_queue")
	enqueuer := agenticenqueue.NewService(store, queue, agenticenqueue.Options{
		EnforcementMode:    config.AgenticEnforcementModeGateway,
		CompletionGateMode: config.AgenticCompletionGateModeVerification,
	})

	result, err := NewService(store, WithAutoEnqueue(enqueuer, AutoEnqueueOptions{
		Enabled:                 true,
		Limit:                   10,
		OperatorID:              "activation-test",
		Reason:                  "activation test auto enqueue",
		MaxRiskLevel:            agentic.RiskLevelLow,
		RequestedEnforcement:    config.AgenticEnforcementModeGateway,
		RequestedCompletionGate: config.AgenticCompletionGateModeVerification,
	})).RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.ExecutionPolicy != ExecutionPolicyAutoEnqueueLowRisk || !result.EnqueuePerformed {
		t.Fatalf("result policy/enqueue = %+v", result)
	}
	if result.AutoEnqueue.Considered != 1 || result.AutoEnqueue.Enqueued != 1 || result.AutoEnqueue.Skipped != 0 || result.AutoEnqueue.Failed != 0 {
		t.Fatalf("auto enqueue result = %+v", result.AutoEnqueue)
	}
	if len(result.AutoEnqueue.QueueTaskIDs) != 1 || result.AutoEnqueue.QueueTaskIDs[0] == 0 {
		t.Fatalf("auto enqueue queue task ids = %+v", result.AutoEnqueue.QueueTaskIDs)
	}
	child, err := store.GetAgenticTask(ctx, result.ProposedTaskIDs[0])
	if err != nil {
		t.Fatalf("GetAgenticTask child: %v", err)
	}
	if child.Status != agentic.TaskStatusPending || child.QueueTaskID != result.AutoEnqueue.QueueTaskIDs[0] {
		t.Fatalf("child task = %+v, want pending queue-backed task", child)
	}
	if _, err := queue.Get(ctx, child.QueueTaskID); err != nil {
		t.Fatalf("queue.Get(%d): %v", child.QueueTaskID, err)
	}
	if afterQueue := activationCountRows(t, db, "task_queue"); afterQueue != beforeQueue+1 {
		t.Fatalf("queue rows = %d, want %d", afterQueue, beforeQueue+1)
	}
	decisions, err := store.ListTaskEnqueueDecisionsByTask(ctx, child.ID)
	if err != nil {
		t.Fatalf("ListTaskEnqueueDecisionsByTask: %v", err)
	}
	if len(decisions) != 1 || decisions[0].QueueTaskID != child.QueueTaskID || decisions[0].OperatorID != "activation-test" || decisions[0].RequestedEnforcement != config.AgenticEnforcementModeGateway || decisions[0].RequestedCompletionGate != config.AgenticCompletionGateModeVerification {
		t.Fatalf("enqueue decisions = %+v", decisions)
	}
	run, err := store.GetActivationRun(ctx, result.RunID)
	if err != nil {
		t.Fatalf("GetActivationRun: %v", err)
	}
	if run.ExecutionPolicy != ExecutionPolicyAutoEnqueueLowRisk || !run.EnqueuePerformed || len(run.ProposedTaskIDs) != 1 || run.ProposedTaskIDs[0] != child.ID {
		t.Fatalf("activation run = %+v", run)
	}
}

func TestService_AutoEnqueueSkipsAboveLowRisk(t *testing.T) {
	ctx := context.Background()
	db, store := newActivationTestStore(t)
	queue, err := daemon.NewQueueNoRecover(db)
	if err != nil {
		t.Fatalf("NewQueueNoRecover: %v", err)
	}
	parent := createActivationTask(t, ctx, store)
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		GoalID:             parent.GoalID,
		ParentID:           parent.ID,
		Title:              "Risky proposed task",
		Prompt:             "Requires human approval.",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelMedium,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	enqueuer := agenticenqueue.NewService(store, queue, agenticenqueue.Options{})
	service := NewService(store, WithAutoEnqueue(enqueuer, AutoEnqueueOptions{
		Enabled:      true,
		MaxRiskLevel: agentic.RiskLevelLow,
	}))
	result, err := service.autoEnqueueProposed(ctx, []int64{task.ID})
	if err != nil {
		t.Fatalf("autoEnqueueProposed: %v", err)
	}
	if result.Considered != 1 || result.Enqueued != 0 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("auto enqueue result = %+v", result)
	}
	updated, err := store.GetAgenticTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if updated.Status != agentic.TaskStatusProposed || updated.QueueTaskID != 0 {
		t.Fatalf("updated task = %+v, want untouched proposed task", updated)
	}
	if rows := activationCountRows(t, db, "task_queue"); rows != 0 {
		t.Fatalf("queue rows = %d, want 0", rows)
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
