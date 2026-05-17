package triage

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"

	_ "modernc.org/sqlite"
)

func TestTriage_SignalCreatesProposedAgenticTask(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	signal := createSignal(t, ctx, store, agentic.GoalSignal{
		Source:      "ambient",
		Type:        "ambient_boot_task",
		PayloadJSON: `{"prompt_hash":"abc","prompt_len":12}`,
		Fingerprint: "ambient-fp",
		Status:      agentic.SignalStatusNew,
		DedupeKey:   "ambient:abc",
	})

	task, err := NewTriager(store).TriageSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("TriageSignal: %v", err)
	}

	if task.SignalID != signal.ID || task.GoalID != 0 || task.QueueTaskID != 0 || task.Status != agentic.TaskStatusProposed {
		t.Fatalf("unexpected proposed task: %+v", task)
	}
	if task.Title == "" || task.Prompt == "" {
		t.Fatalf("task should have synthetic title and prompt: %+v", task)
	}
	assertSignalStatus(t, ctx, store, signal.ID, agentic.SignalStatusTriaged)
	assertNoDaemonQueueRows(t, db)
}

func TestTriage_QueueBackedSignalLinksExistingQueueTask(t *testing.T) {
	ctx := context.Background()
	db, store, queue := newTriageTestStore(t)
	queueID := enqueueTask(t, ctx, queue, "fix the current issue")
	existing, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		QueueTaskID:        queueID,
		Title:              "Existing daemon envelope",
		Prompt:             "fix the current issue",
		Status:             agentic.TaskStatusRunning,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask existing envelope: %v", err)
	}
	signal := createSignal(t, ctx, store, queueBackedSignal(queueID))

	task, err := NewTriager(store).TriageSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("TriageSignal: %v", err)
	}

	if task.ID != existing.ID || task.QueueTaskID != queueID || task.SignalID != signal.ID || task.Status != agentic.TaskStatusRunning {
		t.Fatalf("unexpected linked task: got %+v existing %+v", task, existing)
	}
	assertSignalStatus(t, ctx, store, signal.ID, agentic.SignalStatusTriaged)
	assertAgenticTaskCount(t, db, 1)
}

func TestTriage_QueueBackedSignalCreatesEnvelopeWithoutEnqueue(t *testing.T) {
	ctx := context.Background()
	db, store, queue := newTriageTestStore(t)
	queueID := enqueueTask(t, ctx, queue, "queue-backed signal")
	before := countRows(t, db, "task_queue")
	signal := createSignal(t, ctx, store, queueBackedSignal(queueID))

	task, err := NewTriager(store).TriageSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("TriageSignal: %v", err)
	}

	if task.SignalID != signal.ID || task.QueueTaskID != queueID || task.Status != agentic.TaskStatusPending {
		t.Fatalf("unexpected queue-backed task: %+v", task)
	}
	if after := countRows(t, db, "task_queue"); after != before {
		t.Fatalf("task_queue rows changed: before=%d after=%d", before, after)
	}
}

func TestTriage_QueueBackedRepeatedSignalAbsorbsExistingTask(t *testing.T) {
	ctx := context.Background()
	db, store, queue := newTriageTestStore(t)
	queueID := enqueueTask(t, ctx, queue, "reobserved queue task")
	firstSignal := createSignal(t, ctx, store, queueBackedSignal(queueID))
	secondSignal := createSignal(t, ctx, store, agentic.GoalSignal{
		Source:      "manual",
		Type:        "daemon_submit",
		PayloadJSON: firstSignal.PayloadJSON,
		Fingerprint: "manual:queue:second",
		Status:      agentic.SignalStatusNew,
		DedupeKey:   "manual:queue:2",
	})
	triager := NewTriager(store)

	first, err := triager.TriageSignal(ctx, firstSignal.ID)
	if err != nil {
		t.Fatalf("TriageSignal first: %v", err)
	}
	second, err := triager.TriageSignal(ctx, secondSignal.ID)
	if err != nil {
		t.Fatalf("TriageSignal second: %v", err)
	}

	if second.ID != first.ID || second.SignalID != firstSignal.ID {
		t.Fatalf("repeated queue signal should absorb into existing task without relinking: first=%+v second=%+v", first, second)
	}
	again, err := triager.TriageSignal(ctx, secondSignal.ID)
	if err != nil {
		t.Fatalf("TriageSignal second retry: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("repeated queue signal retry returned a different task: got %+v want %+v", again, first)
	}
	assertSignalStatus(t, ctx, store, firstSignal.ID, agentic.SignalStatusTriaged)
	assertSignalStatus(t, ctx, store, secondSignal.ID, agentic.SignalStatusTriaged)
	assertAgenticTaskCount(t, db, 1)
}

func TestTriage_DuplicateSignalDoesNotCreateDuplicateTask(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	signal := createSignal(t, ctx, store, nonQueueSignal("manual", "daemon_submit"))
	triager := NewTriager(store)

	first, err := triager.TriageSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("TriageSignal first: %v", err)
	}
	second, err := triager.TriageSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("TriageSignal second: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("duplicate triage created a different task: first=%+v second=%+v", first, second)
	}
	assertAgenticTaskCount(t, db, 1)
}

func TestTriage_RerunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	createSignal(t, ctx, store, nonQueueSignal("ambient", "ambient_boot_task"))
	triager := NewTriager(store)

	first, err := triager.RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce first: %v", err)
	}
	second, err := triager.RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce second: %v", err)
	}

	if first.Created != 1 || second.Created != 0 || second.Linked != 0 {
		t.Fatalf("unexpected run results: first=%+v second=%+v", first, second)
	}
	if len(first.CreatedTaskIDs) != 1 {
		t.Fatalf("first created task ids = %+v, want one created task", first.CreatedTaskIDs)
	}
	if len(second.CreatedTaskIDs) != 0 || len(second.LinkedTaskIDs) != 0 {
		t.Fatalf("second task ids = created %+v linked %+v, want none", second.CreatedTaskIDs, second.LinkedTaskIDs)
	}
	assertAgenticTaskCount(t, db, 1)
}

func TestTriage_UpdatesSignalStatusAfterTaskCreation(t *testing.T) {
	ctx := context.Background()
	_, store, _ := newTriageTestStore(t)
	signal := createSignal(t, ctx, store, nonQueueSignal("scheduler", "scheduled_task"))

	if _, err := NewTriager(store).TriageSignal(ctx, signal.ID); err != nil {
		t.Fatalf("TriageSignal: %v", err)
	}

	assertSignalStatus(t, ctx, store, signal.ID, agentic.SignalStatusTriaged)
}

func TestTriage_NilGoalSignalIsExplicit(t *testing.T) {
	ctx := context.Background()
	_, store, _ := newTriageTestStore(t)
	signal := createSignal(t, ctx, store, nonQueueSignal("ambient", "ambient_boot_task"))

	task, err := NewTriager(store).TriageSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("TriageSignal nil goal: %v", err)
	}

	if task.GoalID != 0 {
		t.Fatalf("nil-goal signal should create nil-goal task, got %+v", task)
	}
}

func TestTriage_FailureDoesNotLeavePartialState(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	signal := createSignal(t, ctx, store, nonQueueSignal("ambient", "ambient_boot_task"))
	if _, err := db.Exec(`
		CREATE TRIGGER fail_signal_triage_update
		BEFORE UPDATE OF status ON goal_signals
		WHEN NEW.status = 'triaged'
		BEGIN
			SELECT RAISE(ABORT, 'forced status update failure');
		END;
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if _, err := NewTriager(store).TriageSignal(ctx, signal.ID); err == nil {
		t.Fatal("TriageSignal unexpectedly succeeded")
	}

	assertAgenticTaskCount(t, db, 0)
	assertSignalStatus(t, ctx, store, signal.ID, agentic.SignalStatusNew)
}

func TestTriage_MalformedPayloadMarkedFailedAndBatchContinues(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	bad := createSignal(t, ctx, store, agentic.GoalSignal{
		Source:      "ambient",
		Type:        "ambient_boot_task",
		PayloadJSON: `{"queue_task_id":`,
		Fingerprint: "bad-payload",
		Status:      agentic.SignalStatusNew,
		DedupeKey:   "ambient:bad-payload",
	})
	good := createSignal(t, ctx, store, nonQueueSignal("ambient", "ambient_boot_task"))

	result, err := NewTriager(store).RunOnce(ctx, 10)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if result.Processed != 2 || result.Failed != 1 || result.Created != 1 {
		t.Fatalf("unexpected run result: %+v", result)
	}
	assertSignalStatus(t, ctx, store, bad.ID, agentic.SignalStatusFailed)
	assertSignalStatus(t, ctx, store, good.ID, agentic.SignalStatusTriaged)
	assertAgenticTaskCount(t, db, 1)
}

func TestTriage_ReconcilesTaskCreatedSignalStillNew(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	signal := createSignal(t, ctx, store, nonQueueSignal("ambient", "ambient_boot_task"))
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		SignalID:           signal.ID,
		Title:              "Existing proposed task",
		Prompt:             "Existing proposed task",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask existing signal task: %v", err)
	}

	got, err := NewTriager(store).TriageSignal(ctx, signal.ID)
	if err != nil {
		t.Fatalf("TriageSignal reconcile: %v", err)
	}

	if got.ID != task.ID {
		t.Fatalf("triage did not return existing task: got %+v want %+v", got, task)
	}
	assertAgenticTaskCount(t, db, 1)
	assertSignalStatus(t, ctx, store, signal.ID, agentic.SignalStatusTriaged)
}

func TestTriage_DoesNotEnqueueDaemonWork(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	createSignal(t, ctx, store, nonQueueSignal("ambient", "ambient_boot_task"))

	if _, err := NewTriager(store).RunOnce(ctx, 10); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	assertNoDaemonQueueRows(t, db)
}

func TestTriage_NoAutonomousSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	createSignal(t, ctx, store, nonQueueSignal("ambient", "ambient_boot_task"))

	if _, err := NewTriager(store).RunOnce(ctx, 10); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	for _, table := range []string{
		"policy_decisions",
		"tool_action_receipts",
		"verification_runs",
		"memory_updates",
		"followups",
	} {
		if got := countRows(t, db, table); got != 0 {
			t.Fatalf("%s rows = %d, want 0", table, got)
		}
	}
}

func TestTriage_DoesNotCreateTaskEdgesWithoutRelationship(t *testing.T) {
	ctx := context.Background()
	db, store, _ := newTriageTestStore(t)
	createSignal(t, ctx, store, nonQueueSignal("ambient", "ambient_boot_task"))

	if _, err := NewTriager(store).RunOnce(ctx, 10); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := countRows(t, db, "task_edges"); got != 0 {
		t.Fatalf("task_edges rows = %d, want 0", got)
	}
}

func newTriageTestStore(t *testing.T) (*sql.DB, *agentic.Store, *daemon.Queue) {
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
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	return db, agentic.NewStore(db), queue
}

func createSignal(t *testing.T, ctx context.Context, store *agentic.Store, signal agentic.GoalSignal) *agentic.GoalSignal {
	t.Helper()
	if signal.PayloadJSON == "" {
		signal.PayloadJSON = `{}`
	}
	if signal.Fingerprint == "" {
		signal.Fingerprint = signal.Source + ":" + signal.Type + ":" + signal.DedupeKey
	}
	if signal.Status == "" {
		signal.Status = agentic.SignalStatusNew
	}
	created, err := store.CreateGoalSignal(ctx, signal)
	if err != nil {
		t.Fatalf("CreateGoalSignal: %v", err)
	}
	return created
}

func nonQueueSignal(source, signalType string) agentic.GoalSignal {
	return agentic.GoalSignal{
		Source:      source,
		Type:        signalType,
		PayloadJSON: `{"prompt_hash":"abc","prompt_len":12}`,
		Fingerprint: source + ":" + signalType,
		Status:      agentic.SignalStatusNew,
		DedupeKey:   source + ":" + signalType + ":1",
	}
}

func queueBackedSignal(queueID int64) agentic.GoalSignal {
	payload, _ := json.Marshal(map[string]any{
		"queue_task_id": queueID,
		"prompt_hash":   "queue-backed",
		"prompt_len":    18,
	})
	return agentic.GoalSignal{
		Source:      "manual",
		Type:        "daemon_submit",
		PayloadJSON: string(payload),
		Fingerprint: "manual:queue",
		Status:      agentic.SignalStatusNew,
		DedupeKey:   "manual:queue:1",
	}
}

func enqueueTask(t *testing.T, ctx context.Context, queue *daemon.Queue, prompt string) int64 {
	t.Helper()
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{Prompt: prompt})
	id, _, err := queue.Enqueue(ctx, payload, "idem:"+prompt)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

func assertSignalStatus(t *testing.T, ctx context.Context, store *agentic.Store, signalID int64, want string) {
	t.Helper()
	signal, err := store.GetGoalSignal(ctx, signalID)
	if err != nil {
		t.Fatalf("GetGoalSignal: %v", err)
	}
	if signal.Status != want {
		t.Fatalf("signal status = %q, want %q", signal.Status, want)
	}
}

func assertAgenticTaskCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	if got := countRows(t, db, "agentic_tasks"); got != want {
		t.Fatalf("agentic_tasks rows = %d, want %d", got, want)
	}
}

func assertNoDaemonQueueRows(t *testing.T, db *sql.DB) {
	t.Helper()
	if got := countRows(t, db, "task_queue"); got != 0 {
		t.Fatalf("task_queue rows = %d, want 0", got)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
