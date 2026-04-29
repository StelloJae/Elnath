package runtime

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"

	_ "modernc.org/sqlite"
)

func TestDaemonEnvelopeCreatesAndCompletesAgenticTask(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	envelope := NewDaemonEnvelope(store)

	run, err := envelope.Start(ctx, daemon.Task{
		ID:      42,
		Payload: daemon.EncodeTaskPayload(daemon.TaskPayload{Prompt: "fix request id middleware"}),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	task, err := store.GetAgenticTaskByQueueTaskID(ctx, 42)
	if err != nil {
		t.Fatalf("GetAgenticTaskByQueueTaskID: %v", err)
	}
	if task.QueueTaskID != 42 || task.Prompt != "fix request id middleware" || task.Status != agentic.TaskStatusRunning {
		t.Fatalf("unexpected running task: %+v", task)
	}
	if task.GoalID != 0 || task.SignalID != 0 {
		t.Fatalf("daemon-origin task should keep nullable goal/signal linkage: %+v", task)
	}

	if err := run.Succeed(ctx); err != nil {
		t.Fatalf("Succeed: %v", err)
	}
	task, err = store.GetAgenticTaskByQueueTaskID(ctx, 42)
	if err != nil {
		t.Fatalf("GetAgenticTaskByQueueTaskID after succeed: %v", err)
	}
	if task.Status != agentic.TaskStatusSucceeded {
		t.Fatalf("status = %q, want %q", task.Status, agentic.TaskStatusSucceeded)
	}
}

func TestDaemonEnvelopeIsIdempotentForQueueTask(t *testing.T) {
	ctx := context.Background()
	_, store := newTestStore(t)
	envelope := NewDaemonEnvelope(store)
	task := daemon.Task{ID: 7, Payload: "repeatable task"}

	first, err := envelope.Start(ctx, task)
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	second, err := envelope.Start(ctx, task)
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if first.AgenticTaskID() != second.AgenticTaskID() {
		t.Fatalf("AgenticTaskID first=%d second=%d", first.AgenticTaskID(), second.AgenticTaskID())
	}
}

func TestDaemonEnvelopeMarkRunningFailureReturnsError(t *testing.T) {
	ctx := context.Background()
	db, store := newTestStore(t)
	envelope := NewDaemonEnvelope(store)
	task := daemon.Task{ID: 8, Payload: "existing task"}

	if _, err := envelope.Start(ctx, task); err != nil {
		t.Fatalf("Start create: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_agentic_task_update
		BEFORE UPDATE ON agentic_tasks
		BEGIN
			SELECT RAISE(FAIL, 'mark running unavailable');
		END;
	`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	if _, err := envelope.Start(ctx, task); err == nil || !strings.Contains(err.Error(), "mark running unavailable") {
		t.Fatalf("Start existing error = %v, want mark running unavailable", err)
	}
}

func TestDaemonEnvelopeFailureAndNoAutonomousSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store := newTestStore(t)
	envelope := NewDaemonEnvelope(store)

	run, err := envelope.Start(ctx, daemon.Task{ID: 9, Payload: "will fail"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := run.Fail(ctx); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	task, err := store.GetAgenticTaskByQueueTaskID(ctx, 9)
	if err != nil {
		t.Fatalf("GetAgenticTaskByQueueTaskID: %v", err)
	}
	if task.Status != agentic.TaskStatusFailed {
		t.Fatalf("status = %q, want %q", task.Status, agentic.TaskStatusFailed)
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

func TestDaemonEnvelopeRecordsTaskWhenDaemonRuns(t *testing.T) {
	ctx := context.Background()
	db, store := newFileTestStore(t)
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	runner := func(context.Context, string, event.Sink) (daemon.TaskResult, error) {
		return daemon.TaskResult{Result: "done", Summary: "done", SessionID: "sess-pr2"}, nil
	}
	socketPath := filepath.Join("/tmp", "elnath-agentic-envelope-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".sock")
	d := daemon.New(queue, socketPath, 1, runner, nil)
	d.WithTaskEnvelope(NewDaemonEnvelope(store))

	daemonCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- d.Start(daemonCtx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("daemon did not stop within timeout")
		}
	})

	taskID, existed, err := queue.Enqueue(ctx, "daemon-backed envelope", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if existed {
		t.Fatal("Enqueue unexpectedly deduplicated")
	}
	task := pollTaskStatus(t, queue, taskID, daemon.StatusDone, 5*time.Second)
	if task.Result != "done" || task.Summary != "done" {
		t.Fatalf("unexpected daemon task result: %+v", task)
	}

	agenticTask := pollAgenticTaskStatus(t, store, taskID, agentic.TaskStatusSucceeded, 5*time.Second)
	if agenticTask.Prompt != "daemon-backed envelope" {
		t.Fatalf("unexpected agentic task: %+v", agenticTask)
	}
}

func TestDaemonEnvelopeReconcilesCompletedQueueTasks(t *testing.T) {
	ctx := context.Background()
	db, store := newFileTestStore(t)
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	envelope := NewDaemonEnvelope(store)

	doneID, existed, err := queue.Enqueue(ctx, "done task", "")
	if err != nil {
		t.Fatalf("Enqueue done: %v", err)
	}
	if existed {
		t.Fatal("Enqueue done unexpectedly deduplicated")
	}
	doneTask, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next done: %v", err)
	}
	if _, err := envelope.Start(ctx, *doneTask); err != nil {
		t.Fatalf("Start done envelope: %v", err)
	}
	if err := queue.MarkDone(ctx, doneID, "done", "done"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	failedID, existed, err := queue.Enqueue(ctx, "failed task", "")
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if existed {
		t.Fatal("Enqueue failed unexpectedly deduplicated")
	}
	failedTask, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if _, err := envelope.Start(ctx, *failedTask); err != nil {
		t.Fatalf("Start failed envelope: %v", err)
	}
	if err := queue.MarkFailed(ctx, failedID, "boom"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	beforeDone, err := store.GetAgenticTaskByQueueTaskID(ctx, doneID)
	if err != nil {
		t.Fatalf("Get done before reconcile: %v", err)
	}
	beforeFailed, err := store.GetAgenticTaskByQueueTaskID(ctx, failedID)
	if err != nil {
		t.Fatalf("Get failed before reconcile: %v", err)
	}
	if beforeDone.Status != agentic.TaskStatusRunning || beforeFailed.Status != agentic.TaskStatusRunning {
		t.Fatalf("precondition status done=%q failed=%q, want running", beforeDone.Status, beforeFailed.Status)
	}

	if err := envelope.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	afterDone, err := store.GetAgenticTaskByQueueTaskID(ctx, doneID)
	if err != nil {
		t.Fatalf("Get done after reconcile: %v", err)
	}
	afterFailed, err := store.GetAgenticTaskByQueueTaskID(ctx, failedID)
	if err != nil {
		t.Fatalf("Get failed after reconcile: %v", err)
	}
	if afterDone.Status != agentic.TaskStatusSucceeded || afterFailed.Status != agentic.TaskStatusFailed {
		t.Fatalf("reconciled status done=%q failed=%q", afterDone.Status, afterFailed.Status)
	}
}

func newTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
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

func newFileTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agentic.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
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

func pollTaskStatus(t *testing.T, queue *daemon.Queue, id int64, want daemon.TaskStatus, timeout time.Duration) *daemon.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := queue.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get task %d: %v", id, err)
		}
		if task.Status == want {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, _ := queue.Get(context.Background(), id)
	t.Fatalf("task %d: status = %q after %s, want %q", id, task.Status, timeout, want)
	return nil
}

func pollAgenticTaskStatus(t *testing.T, store *agentic.Store, queueTaskID int64, want string, timeout time.Duration) *agentic.AgenticTask {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := store.GetAgenticTaskByQueueTaskID(context.Background(), queueTaskID)
		if err != nil {
			t.Fatalf("GetAgenticTaskByQueueTaskID %d: %v", queueTaskID, err)
		}
		if task.Status == want {
			return task
		}
		time.Sleep(50 * time.Millisecond)
	}
	task, _ := store.GetAgenticTaskByQueueTaskID(context.Background(), queueTaskID)
	t.Fatalf("agentic task for queue %d: status = %q after %s, want %q", queueTaskID, task.Status, timeout, want)
	return nil
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
