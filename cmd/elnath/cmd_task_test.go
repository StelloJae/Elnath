package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/daemon"

	_ "modernc.org/sqlite"
)

func openTestQueueDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("exec pragma %q: %v", p, err)
		}
	}
	return db
}

func zeroTime() time.Time { return time.Time{} }

func TestCmdTaskUsage(t *testing.T) {
	stdout, _ := captureOutput(t, func() {
		if err := cmdTask(context.Background(), nil); err != nil {
			t.Fatalf("cmdTask usage: %v", err)
		}
	})
	if !strings.Contains(stdout, "Usage: elnath task") {
		t.Fatalf("stdout = %q, want task usage", stdout)
	}
	for _, want := range []string{"monitor <id>", "output <id>", "stop <id>"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want usage to contain %q", stdout, want)
		}
	}
}

func TestCmdTaskUnknownSubcommand(t *testing.T) {
	err := cmdTask(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown task subcommand: bogus") {
		t.Fatalf("cmdTask(bogus) err = %v, want unknown subcommand", err)
	}
}

func TestCmdTaskShowMissingArgs(t *testing.T) {
	err := cmdTaskShow(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("cmdTaskShow() err = %v, want usage error", err)
	}
}

func TestCmdTaskShowInvalidID(t *testing.T) {
	err := cmdTaskShow(context.Background(), []string{"abc"})
	if err == nil || !strings.Contains(err.Error(), "invalid task ID") {
		t.Fatalf("cmdTaskShow(abc) err = %v, want invalid task ID", err)
	}
}

func TestCmdTaskMonitorWithQueueShowsSnapshot(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "monitor me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if _, err := queue.UpdateAnnotation(ctx, task.ID, "working", "halfway"); err != nil {
		t.Fatalf("UpdateAnnotation: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskMonitorWithQueue(ctx, queue, []string{fmt.Sprint(id)}); err != nil {
			t.Fatalf("cmdTaskMonitorWithQueue: %v", err)
		}
	})
	for _, want := range []string{"ID:", "Status:       running", "Retrieval:    snapshot", "Progress:     working", "Summary:      halfway"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskMonitorWithQueueJSONWaitsForUpdate(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "wait me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	initial, err := queue.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get initial: %v", err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = queue.UpdateAnnotation(ctx, task.ID, "changed progress", "changed summary")
	}()

	stdout, _ := captureOutput(t, func() {
		err := cmdTaskMonitorWithQueue(ctx, queue, []string{
			fmt.Sprint(id),
			"--json",
			"--wait",
			"--since-updated-at", initial.UpdatedAt.Format(time.RFC3339Nano),
			"--timeout-ms", "500",
		})
		if err != nil {
			t.Fatalf("cmdTaskMonitorWithQueue: %v", err)
		}
	})
	for _, want := range []string{`"retrieval_status":"changed"`, `"progress":"changed progress"`, `"summary":"changed summary"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskOutputWithQueueReturnsTail(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "output me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.MarkDone(ctx, task.ID, "abcdef", "done summary"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskOutputWithQueue(ctx, queue, []string{fmt.Sprint(id), "--max-chars", "3"}); err != nil {
			t.Fatalf("cmdTaskOutputWithQueue: %v", err)
		}
	})
	for _, want := range []string{"Field:        result", "Truncated:    true", "Content:\ndef"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskStopWithQueueCancelsPendingTask(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "stop me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskStopWithQueue(ctx, queue, []string{fmt.Sprint(id), "--reason", "operator stop", "--json"}); err != nil {
			t.Fatalf("cmdTaskStopWithQueue: %v", err)
		}
	})
	for _, want := range []string{`"accepted":true`, `"terminal":true`, `"status":"failed"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
	task, err := queue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get stopped task: %v", err)
	}
	if task.Status != daemon.StatusFailed || !strings.Contains(task.Result, "operator stop") {
		t.Fatalf("task = %+v, want failed with operator reason", task)
	}
}

func TestCmdTaskStopWithQueuePlainTextShowsAcceptedState(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "stop plain", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskStopWithQueue(ctx, queue, []string{fmt.Sprint(id), "--reason", "operator stop"}); err != nil {
			t.Fatalf("cmdTaskStopWithQueue: %v", err)
		}
	})
	for _, want := range []string{"Accepted:        true", "Terminal:        true", "Status:          failed", "Reason:          operator stop"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestCmdTaskStopWithQueueRejectsRunningTask(t *testing.T) {
	ctx := context.Background()
	queue := newCmdTaskTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "running stop", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if task, err := queue.Next(ctx); err != nil {
		t.Fatalf("Next: %v", err)
	} else if task == nil {
		t.Fatal("Next returned nil")
	}

	err = cmdTaskStopWithQueue(ctx, queue, []string{fmt.Sprint(id)})
	if err == nil {
		t.Fatal("cmdTaskStopWithQueue running task err = nil, want pending-only error")
	}
	for _, want := range []string{"pending tasks only", "daemon runtime support", "elnath task monitor"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want %q", err.Error(), want)
		}
	}
}

func TestCmdTaskResumeMissingArgs(t *testing.T) {
	err := cmdTaskResume(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("cmdTaskResume() err = %v, want usage error", err)
	}
}

func TestCmdTaskResumeInvalidID(t *testing.T) {
	err := cmdTaskResume(context.Background(), []string{"xyz"})
	if err == nil || !strings.Contains(err.Error(), "invalid task ID") {
		t.Fatalf("cmdTaskResume(xyz) err = %v, want invalid task ID", err)
	}
}

func TestResolveTaskSession(t *testing.T) {
	db := openTestQueueDB(t)
	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	ctx := context.Background()

	id, _, err := queue.Enqueue(ctx, "test task", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// No session bound yet.
	_, err = resolveTaskSession(db, id)
	if err == nil || !strings.Contains(err.Error(), "no session bound") {
		t.Fatalf("resolveTaskSession (no session) err = %v, want no session", err)
	}

	// Bind a session and resolve.
	if err := queue.BindSession(ctx, id, "sess-abc-123"); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	sid, err := resolveTaskSession(db, id)
	if err != nil {
		t.Fatalf("resolveTaskSession: %v", err)
	}
	if sid != "sess-abc-123" {
		t.Fatalf("resolveTaskSession = %q, want %q", sid, "sess-abc-123")
	}
}

func TestResolveTaskSessionNotFound(t *testing.T) {
	db := openTestQueueDB(t)
	_, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	_, err = resolveTaskSession(db, 999)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("resolveTaskSession(999) err = %v, want not found", err)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"this is a long string", 10, "this is..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func TestFormatTimestamp(t *testing.T) {
	got := formatTimestamp(zeroTime())
	if got != "-" {
		t.Errorf("formatTimestamp(zero) = %q, want %q", got, "-")
	}
}

func newCmdTaskTestQueue(t *testing.T) *daemon.Queue {
	t.Helper()
	queue, err := daemon.NewQueue(openTestQueueDB(t))
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	return queue
}
