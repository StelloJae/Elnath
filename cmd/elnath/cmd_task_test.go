package main

import (
	"context"
	"database/sql"
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
