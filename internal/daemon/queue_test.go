package daemon

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("exec pragma %q: %v", p, err)
		}
	}
	return db
}

func TestNewQueue(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	if q == nil {
		t.Fatal("NewQueue returned nil")
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM task_queue").Scan(&count)
	if err != nil {
		t.Fatalf("query task_queue: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 tasks, got %d", count)
	}
}

func TestEnqueue(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	tests := []struct {
		payload string
		wantID  int64
	}{
		{"task one", 1},
		{"task two", 2},
		{"task three", 3},
	}

	for _, tt := range tests {
		id, err := q.Enqueue(ctx, tt.payload)
		if err != nil {
			t.Fatalf("Enqueue(%q): %v", tt.payload, err)
		}
		if id != tt.wantID {
			t.Errorf("Enqueue(%q) = %d, want %d", tt.payload, id, tt.wantID)
		}
	}

	tasks, err := q.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("List: got %d tasks, want 3", len(tasks))
	}

	for _, task := range tasks {
		if task.Status != StatusPending {
			t.Errorf("task %d: status = %q, want %q", task.ID, task.Status, StatusPending)
		}
	}
}

func TestNext(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	q.Enqueue(ctx, "first")
	q.Enqueue(ctx, "second")
	q.Enqueue(ctx, "third")

	task, err := q.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil, expected task")
	}
	if task.Payload != "first" {
		t.Errorf("Next: payload = %q, want %q", task.Payload, "first")
	}
	if task.Status != StatusRunning {
		t.Errorf("Next: status = %q, want %q", task.Status, StatusRunning)
	}
	if task.StartedAt.IsZero() {
		t.Error("Next: started_at should be set")
	}

	task2, err := q.Next(ctx)
	if err != nil {
		t.Fatalf("Next(2): %v", err)
	}
	if task2.Payload != "second" {
		t.Errorf("Next(2): payload = %q, want %q", task2.Payload, "second")
	}

	got, err := q.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusRunning {
		t.Errorf("Get: status = %q, want %q", got.Status, StatusRunning)
	}
}

func TestNext_Empty(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	task, err := q.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task != nil {
		t.Fatalf("Next on empty queue: got task %+v, want nil", task)
	}
}

func TestMarkDone(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	q.Enqueue(ctx, "do something")
	task, _ := q.Next(ctx)

	if err := q.MarkDone(ctx, task.ID, "all good"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	got, err := q.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusDone {
		t.Errorf("status = %q, want %q", got.Status, StatusDone)
	}
	if got.Result != "all good" {
		t.Errorf("result = %q, want %q", got.Result, "all good")
	}
	if got.CompletedAt.UnixMilli() == 0 {
		t.Error("completed_at should be set")
	}
}

func TestMarkFailed(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	q.Enqueue(ctx, "will fail")
	task, _ := q.Next(ctx)

	if err := q.MarkFailed(ctx, task.ID, "something broke"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	got, err := q.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("status = %q, want %q", got.Status, StatusFailed)
	}
	if got.Result != "something broke" {
		t.Errorf("result = %q, want %q", got.Result, "something broke")
	}
	if got.CompletedAt.UnixMilli() == 0 {
		t.Error("completed_at should be set")
	}
}

func TestRecoverStale(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	q.Enqueue(ctx, "stale task")
	task, _ := q.Next(ctx)

	staleTime := time.Now().Add(-10 * time.Minute).UnixMilli()
	_, err = db.Exec("UPDATE task_queue SET started_at = ? WHERE id = ?", staleTime, task.ID)
	if err != nil {
		t.Fatalf("force stale: %v", err)
	}

	recovered, err := q.RecoverStale(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if recovered != 1 {
		t.Errorf("recovered = %d, want 1", recovered)
	}

	got, err := q.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("status = %q, want %q", got.Status, StatusPending)
	}

	q.Enqueue(ctx, "fresh task")
	fresh, _ := q.Next(ctx)
	recovered2, err := q.RecoverStale(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("RecoverStale(2): %v", err)
	}
	if recovered2 != 0 {
		t.Errorf("recovered fresh task unexpectedly: %d", recovered2)
	}
	_ = fresh
}

func TestList(t *testing.T) {
	db := openTestDB(t)
	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	q.Enqueue(ctx, "alpha")
	q.Enqueue(ctx, "beta")
	q.Enqueue(ctx, "gamma")

	q.Next(ctx)
	task2, _ := q.Next(ctx)
	q.MarkDone(ctx, task2.ID, "done")

	tasks, err := q.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("List: got %d tasks, want 3", len(tasks))
	}

	// List returns newest first (created_at DESC).
	if tasks[0].Payload != "gamma" {
		t.Errorf("tasks[0].Payload = %q, want %q", tasks[0].Payload, "gamma")
	}
	if tasks[1].Payload != "beta" {
		t.Errorf("tasks[1].Payload = %q, want %q", tasks[1].Payload, "beta")
	}
	if tasks[2].Payload != "alpha" {
		t.Errorf("tasks[2].Payload = %q, want %q", tasks[2].Payload, "alpha")
	}

	statusMap := map[string]TaskStatus{
		"gamma": StatusPending,
		"beta":  StatusDone,
		"alpha": StatusRunning,
	}
	for _, task := range tasks {
		want := statusMap[task.Payload]
		if task.Status != want {
			t.Errorf("task %q: status = %q, want %q", task.Payload, task.Status, want)
		}
	}
}
