package followup

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/ambient"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/scheduler"

	_ "modernc.org/sqlite"
)

func TestFollowupStore_ListDuePendingFollowups(t *testing.T) {
	ctx := context.Background()
	_, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	now := time.Unix(100, 0).UTC()

	due, err := store.CreateFollowup(ctx, agentic.Followup{
		TaskID:    parent.ID,
		GoalID:    parent.GoalID,
		Reason:    "due now",
		Status:    agentic.FollowupStatusPending,
		TriggerAt: now.Add(-time.Minute),
		WakeAgent: true,
	})
	if err != nil {
		t.Fatalf("CreateFollowup due: %v", err)
	}
	if _, err := store.CreateFollowup(ctx, agentic.Followup{
		TaskID:    parent.ID,
		Reason:    "future",
		Status:    agentic.FollowupStatusPending,
		TriggerAt: now.Add(time.Hour),
		WakeAgent: true,
	}); err != nil {
		t.Fatalf("CreateFollowup future: %v", err)
	}
	if _, err := store.CreateFollowup(ctx, agentic.Followup{
		TaskID:    parent.ID,
		Reason:    "already created",
		Status:    agentic.FollowupStatusCreated,
		TriggerAt: now.Add(-time.Hour),
		WakeAgent: true,
	}); err != nil {
		t.Fatalf("CreateFollowup created: %v", err)
	}

	got, err := store.ListDueFollowups(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDueFollowups: %v", err)
	}
	if len(got) != 1 || got[0].ID != due.ID {
		t.Fatalf("due followups = %+v, want only %d", got, due.ID)
	}
}

func TestFollowupStore_MarkCreatedLinksTask(t *testing.T) {
	ctx := context.Background()
	_, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	child := createFollowupTestTask(t, ctx, store)
	fu := createDueFollowup(t, ctx, store, parent, true)

	got, err := store.MarkFollowupCreated(ctx, fu.ID, child.ID)
	if err != nil {
		t.Fatalf("MarkFollowupCreated: %v", err)
	}
	if got.Status != agentic.FollowupStatusCreated || got.CreatedTaskID != child.ID || !got.ProcessedAt.Valid {
		t.Fatalf("unexpected created followup: %+v", got)
	}
}

func TestFollowupRecorder_CreatesFollowupAfterPassedVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	task := createFollowupTestTask(t, ctx, store)
	createFollowupVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)

	fu, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, CreateRequest{
		TaskID:    task.ID,
		GoalID:    task.GoalID,
		Reason:    "check regression tomorrow",
		TriggerAt: time.Unix(200, 0).UTC(),
		WakeAgent: true,
	})
	if err != nil {
		t.Fatalf("CreateFromVerifiedOutcome: %v", err)
	}
	if fu.Status != agentic.FollowupStatusPending || fu.TaskID != task.ID || fu.GoalID != task.GoalID || !fu.WakeAgent {
		t.Fatalf("unexpected followup: %+v", fu)
	}
	if got := followupTableCount(t, db, "followups"); got != 1 {
		t.Fatalf("followups rows = %d, want 1", got)
	}
}

func TestFollowupRecorder_RerunIsIdempotentForSameOutcome(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	task := createFollowupTestTask(t, ctx, store)
	createFollowupVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	req := CreateRequest{
		TaskID:    task.ID,
		GoalID:    task.GoalID,
		Reason:    "check regression tomorrow",
		TriggerAt: time.Unix(200, 0).UTC(),
		WakeAgent: true,
	}

	first, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, req)
	if err != nil {
		t.Fatalf("CreateFromVerifiedOutcome first: %v", err)
	}
	second, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, req)
	if err != nil {
		t.Fatalf("CreateFromVerifiedOutcome second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate outcome created different followups: first=%+v second=%+v", first, second)
	}
	if got := followupTableCount(t, db, "followups"); got != 1 {
		t.Fatalf("followups rows = %d, want 1", got)
	}
}

func TestFollowupRecorder_CooldownDedupesSameTaskReasonWithinWindow(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	task := createFollowupTestTask(t, ctx, store)
	createFollowupVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	base := time.Unix(3600, 0).UTC()

	first, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, CreateRequest{
		TaskID:    task.ID,
		GoalID:    task.GoalID,
		Reason:    "same cooldown work",
		TriggerAt: base,
		WakeAgent: true,
		Cooldown:  time.Hour,
	})
	if err != nil {
		t.Fatalf("CreateFromVerifiedOutcome first: %v", err)
	}
	second, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, CreateRequest{
		TaskID:    task.ID,
		GoalID:    task.GoalID,
		Reason:    "same cooldown work",
		TriggerAt: base.Add(30 * time.Minute),
		WakeAgent: true,
		Cooldown:  time.Hour,
	})
	if err != nil {
		t.Fatalf("CreateFromVerifiedOutcome second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("cooldown duplicate created different followups: first=%+v second=%+v", first, second)
	}
	if got := followupTableCount(t, db, "followups"); got != 1 {
		t.Fatalf("followups rows = %d, want 1", got)
	}
}

func TestFollowupRecorder_CooldownDedupesAcrossBucketBoundary(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	task := createFollowupTestTask(t, ctx, store)
	createFollowupVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
	firstTrigger := time.Unix(7199, 0).UTC()
	secondTrigger := firstTrigger.Add(2 * time.Second)

	first, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, CreateRequest{
		TaskID:    task.ID,
		GoalID:    task.GoalID,
		Reason:    "same cooldown work",
		TriggerAt: firstTrigger,
		WakeAgent: true,
		Cooldown:  time.Hour,
	})
	if err != nil {
		t.Fatalf("CreateFromVerifiedOutcome first: %v", err)
	}
	second, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, CreateRequest{
		TaskID:    task.ID,
		GoalID:    task.GoalID,
		Reason:    "same cooldown work",
		TriggerAt: secondTrigger,
		WakeAgent: true,
		Cooldown:  time.Hour,
	})
	if err != nil {
		t.Fatalf("CreateFromVerifiedOutcome second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("cooldown boundary duplicate created different followups: first=%+v second=%+v", first, second)
	}
	if got := followupTableCount(t, db, "followups"); got != 1 {
		t.Fatalf("followups rows = %d, want 1", got)
	}
}

func TestFollowupRecorder_CooldownIgnoresFailedOrCanceledFollowups(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{agentic.FollowupStatusFailed, agentic.FollowupStatusCanceled} {
		t.Run(status, func(t *testing.T) {
			db, store := newFollowupTestStore(t)
			task := createFollowupTestTask(t, ctx, store)
			createFollowupVerification(t, ctx, store, task.ID, agentic.VerificationVerdictPassed)
			base := time.Unix(3600, 0).UTC()
			first, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, CreateRequest{
				TaskID:    task.ID,
				GoalID:    task.GoalID,
				Reason:    "retryable followup work",
				TriggerAt: base,
				WakeAgent: true,
				Cooldown:  time.Hour,
			})
			if err != nil {
				t.Fatalf("CreateFromVerifiedOutcome first: %v", err)
			}
			switch status {
			case agentic.FollowupStatusFailed:
				if _, err := store.MarkFollowupFailed(ctx, first.ID, "transient failure"); err != nil {
					t.Fatalf("MarkFollowupFailed: %v", err)
				}
			case agentic.FollowupStatusCanceled:
				if _, err := db.Exec(`UPDATE followups SET status = ? WHERE id = ?`, agentic.FollowupStatusCanceled, first.ID); err != nil {
					t.Fatalf("mark canceled: %v", err)
				}
			}

			second, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, CreateRequest{
				TaskID:    task.ID,
				GoalID:    task.GoalID,
				Reason:    "retryable followup work",
				TriggerAt: base.Add(30 * time.Minute),
				WakeAgent: true,
				Cooldown:  time.Hour,
			})
			if err != nil {
				t.Fatalf("CreateFromVerifiedOutcome second: %v", err)
			}
			if first.ID == second.ID {
				t.Fatalf("terminal followup reused inside cooldown: first=%+v second=%+v", first, second)
			}
			if got := followupTableCount(t, db, "followups"); got != 2 {
				t.Fatalf("followups rows = %d, want retry row", got)
			}
		})
	}
}

func TestFollowupRecorder_BlocksWithoutPassedVerification(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		verdict string
	}{
		{name: "missing"},
		{name: "failed", verdict: agentic.VerificationVerdictFailed},
		{name: "inconclusive", verdict: agentic.VerificationVerdictInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, store := newFollowupTestStore(t)
			task := createFollowupTestTask(t, ctx, store)
			if tc.verdict != "" {
				createFollowupVerification(t, ctx, store, task.ID, tc.verdict)
			}

			_, err := NewRecorder(store).CreateFromVerifiedOutcome(ctx, CreateRequest{
				TaskID:    task.ID,
				Reason:    "should not schedule",
				TriggerAt: time.Unix(200, 0).UTC(),
				WakeAgent: true,
			})
			if !errors.Is(err, ErrVerificationNotPassed) {
				t.Fatalf("CreateFromVerifiedOutcome err = %v, want ErrVerificationNotPassed", err)
			}
			if got := followupTableCount(t, db, "followups"); got != 0 {
				t.Fatalf("followups rows = %d, want 0", got)
			}
		})
	}
}

func TestFollowupScheduler_DueFollowupCreatesProposedTask(t *testing.T) {
	ctx := context.Background()
	_, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	fu := createDueFollowup(t, ctx, store, parent, true)

	result, err := NewScheduler(store).RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Processed != 1 || result.Created != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	got, err := store.GetFollowup(ctx, fu.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	child, err := store.GetAgenticTask(ctx, got.CreatedTaskID)
	if err != nil {
		t.Fatalf("GetAgenticTask child: %v", err)
	}
	if child.Status != agentic.TaskStatusProposed || child.ParentID != parent.ID || child.GoalID != parent.GoalID || child.QueueTaskID != 0 {
		t.Fatalf("unexpected proposed task: %+v", child)
	}
	if len(result.CreatedTaskIDs) != 1 || result.CreatedTaskIDs[0] != child.ID {
		t.Fatalf("created task ids = %+v, want child %d", result.CreatedTaskIDs, child.ID)
	}
}

func TestFollowupScheduler_DueFollowupRecordsSignal(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	fu := createDueFollowup(t, ctx, store, parent, true)

	if _, err := NewScheduler(store).RunOnce(ctx, 10); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	signal := getOnlyFollowupSignal(t, ctx, db, store)
	if signal.Source != SourceFollowup || signal.Type != TypeFollowupDue || signal.DedupeKey != fu.DedupeKey {
		t.Fatalf("unexpected followup signal: %+v followup=%+v", signal, fu)
	}
}

func TestFollowupScheduler_RerunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	createDueFollowup(t, ctx, store, parent, true)
	s := NewScheduler(store)

	first, err := s.RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce first: %v", err)
	}
	second, err := s.RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce second: %v", err)
	}
	if first.Created != 1 || second.Processed != 0 || second.Created != 0 {
		t.Fatalf("unexpected results: first=%+v second=%+v", first, second)
	}
	if got := followupTableCount(t, db, "goal_signals"); got != 1 {
		t.Fatalf("goal_signals rows = %d, want 1", got)
	}
	if got := followupTableCount(t, db, "agentic_tasks"); got != 2 {
		t.Fatalf("agentic_tasks rows = %d, want parent+child", got)
	}
}

func TestFollowupScheduler_DoesNotEnqueueDaemonWork(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	if _, err := daemon.NewQueue(db); err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	parent := createFollowupTestTask(t, ctx, store)
	createDueFollowup(t, ctx, store, parent, true)
	before := followupTableCount(t, db, "task_queue")

	if _, err := NewScheduler(store).RunOnce(ctx, 10); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if after := followupTableCount(t, db, "task_queue"); after != before {
		t.Fatalf("task_queue rows changed: before=%d after=%d", before, after)
	}
}

func TestFollowupScheduler_WakeAgentFalseSkipsExecutableWork(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	fu := createDueFollowup(t, ctx, store, parent, false)

	result, err := NewScheduler(store).RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := store.GetFollowup(ctx, fu.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	if result.Skipped != 1 || got.Status != agentic.FollowupStatusSkipped || got.CreatedTaskID != 0 {
		t.Fatalf("unexpected skipped followup: result=%+v followup=%+v", result, got)
	}
	if tasks := followupTableCount(t, db, "agentic_tasks"); tasks != 1 {
		t.Fatalf("agentic_tasks rows = %d, want only parent", tasks)
	}
}

func TestFollowupScheduler_FailureObservable(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	fu := createDueFollowup(t, ctx, store, parent, true)
	if _, err := db.Exec(`
		CREATE TRIGGER fail_followup_task_insert
		BEFORE INSERT ON agentic_tasks
		WHEN NEW.parent_id IS NOT NULL
		BEGIN
			SELECT RAISE(ABORT, 'forced followup task failure');
		END;
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	result, err := NewScheduler(store).RunOnce(ctx, 10)
	if err == nil {
		t.Fatal("RunOnce unexpectedly succeeded")
	}
	got, getErr := store.GetFollowup(ctx, fu.ID)
	if getErr != nil {
		t.Fatalf("GetFollowup: %v", getErr)
	}
	if result.Failed != 1 || got.Status != agentic.FollowupStatusFailed || got.FailureReason == "" {
		t.Fatalf("failure was not observable: result=%+v followup=%+v err=%v", result, got, err)
	}
}

func TestFollowupScheduler_ReconcilesStaleProcessingFollowup(t *testing.T) {
	ctx := context.Background()
	_, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	fu := createDueFollowup(t, ctx, store, parent, true)
	if _, err := store.MarkFollowupProcessing(ctx, fu.ID); err != nil {
		t.Fatalf("MarkFollowupProcessing: %v", err)
	}
	signal := createFollowupDueSignal(t, ctx, store, fu)
	child, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		GoalID:             fu.GoalID,
		SignalID:           signal.ID,
		ParentID:           fu.TaskID,
		Title:              "Existing followup task",
		Prompt:             "Existing followup task",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask child: %v", err)
	}

	result, err := NewScheduler(store).RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := store.GetFollowup(ctx, fu.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	if result.Created != 1 || got.Status != agentic.FollowupStatusCreated || got.CreatedTaskID != child.ID {
		t.Fatalf("stale followup not reconciled: result=%+v followup=%+v child=%+v", result, got, child)
	}
}

func TestFollowupScheduler_ConcurrentSignalTaskRaceReconcilesToCreated(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	fu := createDueFollowup(t, ctx, store, parent, true)
	if _, err := db.Exec(`
		CREATE TRIGGER create_racing_followup_task
		BEFORE INSERT ON agentic_tasks
		WHEN NEW.signal_id IS NOT NULL AND NEW.parent_id IS NOT NULL
		BEGIN
			INSERT INTO agentic_tasks(goal_id, signal_id, parent_id, title, prompt, status, priority, risk_level, autonomy_decision, verification_status, created_at, updated_at)
			VALUES (NEW.goal_id, NEW.signal_id, NEW.parent_id, 'Racing followup task', 'Racing followup task', 'proposed', 0, 'low', 'observe', 'pending', 1, 1);
			SELECT RAISE(FAIL, 'forced racing insert');
		END;
	`); err != nil {
		t.Fatalf("create racing trigger: %v", err)
	}

	result, err := NewScheduler(store).RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := store.GetFollowup(ctx, fu.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	if result.Created != 1 || got.Status != agentic.FollowupStatusCreated || got.CreatedTaskID == 0 {
		t.Fatalf("race did not reconcile to created: result=%+v followup=%+v", result, got)
	}
	if tasks := followupTableCount(t, db, "agentic_tasks"); tasks != 2 {
		t.Fatalf("agentic_tasks rows = %d, want parent+racing child", tasks)
	}
}

func TestFollowupScheduler_RedactsReasonInProposedTaskPrompt(t *testing.T) {
	ctx := context.Background()
	_, store := newFollowupTestStore(t)
	parent := createFollowupTestTask(t, ctx, store)
	fu, err := store.CreateFollowup(ctx, agentic.Followup{
		TaskID:    parent.ID,
		GoalID:    parent.GoalID,
		Reason:    `rerun with password="supersecretpassword"`,
		Status:    agentic.FollowupStatusPending,
		TriggerAt: time.Unix(10, 0).UTC(),
		WakeAgent: true,
	})
	if err != nil {
		t.Fatalf("CreateFollowup: %v", err)
	}

	if _, err := NewScheduler(store).RunOnce(ctx, 10); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := store.GetFollowup(ctx, fu.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	task, err := store.GetAgenticTask(ctx, got.CreatedTaskID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if strings.Contains(task.Prompt, "supersecretpassword") || !strings.Contains(task.Prompt, "[REDACTED:generic-password]") {
		t.Fatalf("prompt did not redact reason: %q", task.Prompt)
	}
}

func TestFollowupScheduler_DoesNotCreatePolicyApprovalsReceiptsVerifierOrMemory(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	if _, err := daemon.NewApprovalStore(db); err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	parent := createFollowupTestTask(t, ctx, store)
	createDueFollowup(t, ctx, store, parent, true)

	if _, err := NewScheduler(store).RunOnce(ctx, 10); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	for _, table := range []string{"policy_decisions", "approval_requests", "tool_action_receipts", "verification_runs", "memory_updates"} {
		if got := followupTableCount(t, db, table); got != 0 {
			t.Fatalf("%s rows = %d, want 0", table, got)
		}
	}
}

func TestFollowupScheduler_DoesNotChangeQueueMarkDone(t *testing.T) {
	ctx := context.Background()
	db, store := newFollowupTestStore(t)
	q, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	queueID, existed, err := q.Enqueue(ctx, "existing queue task", "")
	if err != nil || existed {
		t.Fatalf("Enqueue: id=%d existed=%v err=%v", queueID, existed, err)
	}
	queueTask, err := q.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	parent := createFollowupTestTask(t, ctx, store)
	createDueFollowup(t, ctx, store, parent, true)

	if _, err := NewScheduler(store).RunOnce(ctx, 10); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if err := q.MarkDone(ctx, queueTask.ID, "ok", "queue completion unchanged"); err != nil {
		t.Fatalf("MarkDone after followup scheduler: %v", err)
	}
}

func TestStaticSchedulerBehaviorUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	enq := &cancelingEnqueuer{cancel: cancel}
	s := scheduler.New([]scheduler.ScheduledTask{{
		Name:       "startup",
		Type:       "agent",
		Prompt:     "run startup task",
		Interval:   time.Hour,
		RunOnStart: true,
	}}, enq, nil)

	if err := s.Run(ctx); err != nil {
		t.Fatalf("scheduler Run: %v", err)
	}
	if enq.calls != 1 {
		t.Fatalf("scheduler enqueue calls = %d, want 1", enq.calls)
	}
}

func TestAmbientSchedulerBehaviorUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 1)
	s := ambient.NewScheduler(ambient.Config{
		Tasks: []ambient.BootTask{{
			Title:    "startup",
			Prompt:   "run startup task",
			Schedule: ambient.Schedule{Type: ambient.ScheduleStartup},
			Silent:   true,
		}},
		Runner: func(context.Context, string, event.Sink) (daemon.TaskResult, error) {
			done <- struct{}{}
			return daemon.TaskResult{Result: "ok", Summary: "ok"}, nil
		},
	})

	s.Start(ctx)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ambient startup task did not run")
	}
	s.Stop()
}

type cancelingEnqueuer struct {
	cancel func()
	calls  int
}

func (e *cancelingEnqueuer) Enqueue(context.Context, string, string) (int64, bool, error) {
	e.calls++
	if e.cancel != nil {
		e.cancel()
	}
	return int64(e.calls), false, nil
}

func newFollowupTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return db, agentic.NewStore(db)
}

func createFollowupTestTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	goal, err := store.CreateStandingGoal(ctx, agentic.StandingGoal{
		Title:         "Followup goal",
		Status:        agentic.GoalStatusActive,
		AutonomyLevel: agentic.AutonomyLevelObserve,
	})
	if err != nil {
		t.Fatalf("CreateStandingGoal: %v", err)
	}
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		GoalID:             goal.ID,
		Title:              "Parent task",
		Prompt:             "Parent task prompt",
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

func createFollowupVerification(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, verdict string) *agentic.VerificationRun {
	t.Helper()
	run, err := store.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           taskID,
		CriteriaJSON:     `{"kind":"followup"}`,
		EvidenceRefsJSON: `[]`,
		Verdict:          verdict,
		Reason:           "test verifier",
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}
	return run
}

func createDueFollowup(t *testing.T, ctx context.Context, store *agentic.Store, parent *agentic.AgenticTask, wakeAgent bool) *agentic.Followup {
	t.Helper()
	fu, err := store.CreateFollowup(ctx, agentic.Followup{
		TaskID:    parent.ID,
		GoalID:    parent.GoalID,
		Reason:    "check followup status",
		Status:    agentic.FollowupStatusPending,
		TriggerAt: time.Unix(10, 0).UTC(),
		WakeAgent: wakeAgent,
	})
	if err != nil {
		t.Fatalf("CreateFollowup: %v", err)
	}
	return fu
}

func createFollowupDueSignal(t *testing.T, ctx context.Context, store *agentic.Store, fu *agentic.Followup) *agentic.GoalSignal {
	t.Helper()
	signal, _, err := store.CreateOrGetGoalSignal(ctx, agentic.GoalSignal{
		GoalID:      fu.GoalID,
		Source:      SourceFollowup,
		Type:        TypeFollowupDue,
		PayloadJSON: `{"followup_id":1}`,
		Fingerprint: "followup-fingerprint",
		Status:      agentic.SignalStatusNew,
		DedupeKey:   fu.DedupeKey,
		ObservedAt:  time.Unix(20, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("CreateOrGetGoalSignal: %v", err)
	}
	return signal
}

func getOnlyFollowupSignal(t *testing.T, ctx context.Context, db *sql.DB, store *agentic.Store) *agentic.GoalSignal {
	t.Helper()
	if got := followupTableCount(t, db, "goal_signals"); got != 1 {
		t.Fatalf("goal_signals rows = %d, want 1", got)
	}
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM goal_signals`).Scan(&id); err != nil {
		t.Fatalf("select goal signal: %v", err)
	}
	signal, err := store.GetGoalSignal(ctx, id)
	if err != nil {
		t.Fatalf("GetGoalSignal: %v", err)
	}
	return signal
}

func followupTableCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
