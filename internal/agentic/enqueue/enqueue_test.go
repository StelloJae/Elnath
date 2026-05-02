package enqueue

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/daemon"

	_ "modernc.org/sqlite"
)

type fakeQueue struct {
	id    int64
	err   error
	calls int
}

func (q *fakeQueue) Enqueue(context.Context, string, string) (int64, bool, error) {
	panic("fakeQueue.Enqueue should not be used by enqueue service")
}

func (q *fakeQueue) EnqueueTx(context.Context, *sql.Tx, string, string) (int64, bool, error) {
	q.calls++
	if q.err != nil {
		return 0, false, q.err
	}
	return q.id, false, nil
}

func newEnqueueTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("foreign keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return db, agentic.NewStore(db)
}

func createProposedTask(t *testing.T, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	task, err := store.CreateAgenticTask(context.Background(), agentic.AgenticTask{
		Title:              "Proposed enqueue",
		Prompt:             "execute proposed work",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func TestService_QueueEnqueueFailureObservable(t *testing.T) {
	ctx := context.Background()
	_, store := newEnqueueTestStore(t)
	task := createProposedTask(t, store)
	queueErr := errors.New("queue unavailable")
	service := NewService(store, &fakeQueue{err: queueErr}, Options{})

	_, err := service.Enqueue(ctx, Request{TaskID: task.ID, OperatorID: "cli"})
	if err == nil || !errors.Is(err, queueErr) {
		t.Fatalf("Enqueue err = %v, want queue error", err)
	}
	updated, err := store.GetAgenticTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if updated.QueueTaskID != 0 || updated.Status != agentic.TaskStatusProposed {
		t.Fatalf("task after queue failure = %+v, want proposed without queue link", updated)
	}
	decisions, err := store.ListTaskEnqueueDecisionsByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListTaskEnqueueDecisionsByTask: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Status != agentic.TaskEnqueueStatusFailed || decisions[0].FailureReason == "" {
		t.Fatalf("decisions = %+v, want failed decision with reason", decisions)
	}
}

func TestService_RerunReturnsExistingEnqueuedDecisionWithoutQueueCall(t *testing.T) {
	ctx := context.Background()
	_, store := newEnqueueTestStore(t)
	task := createProposedTask(t, store)
	firstQueue := &fakeQueue{id: 91}
	service := NewService(store, firstQueue, Options{})
	first, err := service.Enqueue(ctx, Request{TaskID: task.ID, OperatorID: "cli"})
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	secondQueue := &fakeQueue{id: 92}
	service = NewService(store, secondQueue, Options{})
	second, err := service.Enqueue(ctx, Request{TaskID: task.ID, OperatorID: "cli"})
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	if !second.Existed || second.QueueTaskID != first.QueueTaskID {
		t.Fatalf("second result = %+v, want existing queue task %d", second, first.QueueTaskID)
	}
	if secondQueue.calls != 0 {
		t.Fatalf("queue calls on rerun = %d, want 0", secondQueue.calls)
	}
}

func TestService_ConfigObserveRejectsRequestedModes(t *testing.T) {
	ctx := context.Background()
	_, store := newEnqueueTestStore(t)
	task := createProposedTask(t, store)
	queue := &fakeQueue{id: 1}
	service := NewService(store, queue, Options{})

	if _, err := service.Enqueue(ctx, Request{TaskID: task.ID, RequestedEnforcement: config.AgenticEnforcementModeGateway}); err == nil || !errors.Is(err, ErrConfigDisallows) {
		t.Fatalf("gateway err = %v, want ErrConfigDisallows", err)
	}
	if _, err := service.Enqueue(ctx, Request{TaskID: task.ID, RequestedCompletionGate: config.AgenticCompletionGateModeVerification}); err == nil || !errors.Is(err, ErrConfigDisallows) {
		t.Fatalf("completion err = %v, want ErrConfigDisallows", err)
	}
	if queue.calls != 0 {
		t.Fatalf("queue calls after disallowed modes = %d, want 0", queue.calls)
	}
}

func TestService_RedactsSecretReason(t *testing.T) {
	ctx := context.Background()
	_, store := newEnqueueTestStore(t)
	task := createProposedTask(t, store)
	service := NewService(store, &fakeQueue{id: 11}, Options{})
	result, err := service.Enqueue(ctx, Request{
		TaskID:     task.ID,
		OperatorID: "cli",
		Reason:     "operator key AKIAIOSFODNN7EXAMPLE approved",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if result.Decision == nil {
		t.Fatal("decision missing")
	}
	if strings.Contains(result.Decision.Reason, "AKIAIOSFODNN7EXAMPLE") || !strings.Contains(result.Decision.Reason, "[REDACTED:aws-access-key]") {
		t.Fatalf("reason = %q, want redacted secret marker", result.Decision.Reason)
	}
}

func TestService_DifferentModeRerunDoesNotCreateSecondQueueTask(t *testing.T) {
	ctx := context.Background()
	db, store := newEnqueueTestStore(t)
	queue, err := daemon.NewQueueNoRecover(db)
	if err != nil {
		t.Fatalf("NewQueueNoRecover: %v", err)
	}
	task := createProposedTask(t, store)
	service := NewService(store, queue, Options{
		EnforcementMode:    config.AgenticEnforcementModeGateway,
		CompletionGateMode: config.AgenticCompletionGateModeVerification,
	})
	first, err := service.Enqueue(ctx, Request{TaskID: task.ID})
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	second, err := service.Enqueue(ctx, Request{
		TaskID:                  task.ID,
		RequestedEnforcement:    config.AgenticEnforcementModeGateway,
		RequestedCompletionGate: config.AgenticCompletionGateModeVerification,
	})
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	if !second.Existed || second.QueueTaskID != first.QueueTaskID {
		t.Fatalf("second result = %+v, want existing queue task %d", second, first.QueueTaskID)
	}
	if got := countQueueTasks(t, db); got != 1 {
		t.Fatalf("queue rows = %d, want 1", got)
	}
}

func TestService_ConcurrentDifferentModesCreateOneQueueTask(t *testing.T) {
	ctx := context.Background()
	db, store := newConcurrentEnqueueTestStore(t)
	queue, err := daemon.NewQueueNoRecover(db)
	if err != nil {
		t.Fatalf("NewQueueNoRecover: %v", err)
	}
	task := createProposedTask(t, store)
	service := NewService(store, queue, Options{
		EnforcementMode:    config.AgenticEnforcementModeGateway,
		CompletionGateMode: config.AgenticCompletionGateModeVerification,
	})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, req := range []Request{
		{TaskID: task.ID},
		{TaskID: task.ID, RequestedEnforcement: config.AgenticEnforcementModeGateway, RequestedCompletionGate: config.AgenticCompletionGateModeVerification},
	} {
		wg.Add(1)
		go func(req Request) {
			defer wg.Done()
			_, err := service.Enqueue(ctx, req)
			errs <- err
		}(req)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "unique") || strings.Contains(lower, "constraint") || strings.Contains(lower, "idx_task_enqueue") {
			t.Fatalf("concurrent enqueue exposed raw db error: %v", err)
		}
	}
	if successes == 0 {
		t.Fatal("concurrent enqueue had no successful caller")
	}
	if got := countQueueTasks(t, db); got != 1 {
		t.Fatalf("queue rows = %d, want 1", got)
	}
	updated, err := store.GetAgenticTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if updated.QueueTaskID == 0 || updated.Status != agentic.TaskStatusPending {
		t.Fatalf("updated task = %+v, want one durable queue link", updated)
	}
}

func newConcurrentEnqueueTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "agentic-enqueue.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(4)
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

func countQueueTasks(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_queue`).Scan(&count); err != nil {
		t.Fatalf("count task_queue: %v", err)
	}
	return count
}
