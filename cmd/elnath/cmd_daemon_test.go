package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/learning"
)

type stubSchedulerQueue struct{}

func (stubSchedulerQueue) Enqueue(context.Context, string, string) (int64, bool, error) {
	return 0, false, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLoadScheduler(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "without configured path", run: runLoadSchedulerWithoutConfiguredPath},
		{name: "resolves relative path", run: runLoadSchedulerResolvesRelativePath},
		{name: "disabled tasks", run: runLoadSchedulerReturnsNilForDisabledTasks},
		{name: "missing configured file", run: runLoadSchedulerMissingConfiguredFileIsNoOp},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func runLoadSchedulerWithoutConfiguredPath(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Daemon.ScheduledTasksPath = ""

	sch, path, taskCount, err := loadScheduler(cfg, stubSchedulerQueue{}, testLogger())
	if err != nil {
		t.Fatalf("loadScheduler() error = %v", err)
	}
	if sch != nil {
		t.Fatal("loadScheduler() scheduler != nil, want nil")
	}
	if path != "" {
		t.Fatalf("path = %q, want empty", path)
	}
	if taskCount != 0 {
		t.Fatalf("taskCount = %d, want 0", taskCount)
	}
}

func runLoadSchedulerResolvesRelativePath(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "scheduled_tasks.yaml")
	if err := os.WriteFile(configPath, []byte(`scheduled_tasks:
  - name: task1
    prompt: hello
    interval: 1h
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.DataDir = dataDir
	cfg.Daemon.ScheduledTasksPath = "scheduled_tasks.yaml"

	sch, path, taskCount, err := loadScheduler(cfg, stubSchedulerQueue{}, testLogger())
	if err != nil {
		t.Fatalf("loadScheduler() error = %v", err)
	}
	if sch == nil {
		t.Fatal("loadScheduler() scheduler = nil, want non-nil")
	}
	if path != configPath {
		t.Fatalf("path = %q, want %q", path, configPath)
	}
	if taskCount != 1 {
		t.Fatalf("taskCount = %d, want 1", taskCount)
	}
}

func runLoadSchedulerReturnsNilForDisabledTasks(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "scheduled_tasks.yaml")
	if err := os.WriteFile(configPath, []byte(`scheduled_tasks:
  - name: task1
    prompt: hello
    interval: 1h
    enabled: false
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.DataDir = dataDir
	cfg.Daemon.ScheduledTasksPath = "scheduled_tasks.yaml"

	sch, path, taskCount, err := loadScheduler(cfg, stubSchedulerQueue{}, testLogger())
	if err != nil {
		t.Fatalf("loadScheduler() error = %v", err)
	}
	if sch != nil {
		t.Fatal("loadScheduler() scheduler != nil, want nil")
	}
	if path != configPath {
		t.Fatalf("path = %q, want %q", path, configPath)
	}
	if taskCount != 0 {
		t.Fatalf("taskCount = %d, want 0", taskCount)
	}
}

func runLoadSchedulerMissingConfiguredFileIsNoOp(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "missing.yaml")

	cfg := config.DefaultConfig()
	cfg.DataDir = dataDir
	cfg.Daemon.ScheduledTasksPath = "missing.yaml"

	sch, path, taskCount, err := loadScheduler(cfg, stubSchedulerQueue{}, testLogger())
	if err != nil {
		t.Fatalf("loadScheduler() error = %v", err)
	}
	if sch != nil {
		t.Fatal("loadScheduler() scheduler != nil, want nil")
	}
	if path != configPath {
		t.Fatalf("path = %q, want %q", path, configPath)
	}
	if taskCount != 0 {
		t.Fatalf("taskCount = %d, want 0", taskCount)
	}
}

func TestAutoRotateLessons(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "rotates and logs", run: runAutoRotateLessonsRotatesAndLogs},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func runAutoRotateLessonsRotatesAndLogs(t *testing.T) {
	dataDir := t.TempDir()
	store := learning.NewStore(filepath.Join(dataDir, "lessons.jsonl"))
	for i := 0; i < 5; i++ {
		if err := store.Append(learning.Lesson{
			Text:       strings.Repeat("lesson-", 32) + string(rune('A'+i)),
			Topic:      "daemon",
			Source:     "test",
			Confidence: "medium",
		}); err != nil {
			t.Fatalf("Append(%d) error = %v", i, err)
		}
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	autoRotateLessons(logger, store, learning.RotateOpts{KeepLast: 2})

	active, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active lessons = %d, want 2", len(active))
	}
	archivePath := filepath.Join(dataDir, "lessons.archive.jsonl")
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", archivePath, err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 3 {
		t.Fatalf("archive lines = %d, want 3", lines)
	}
	if !strings.Contains(logs.String(), "learning: auto-rotated lessons") {
		t.Fatalf("logs = %q, want auto-rotate message", logs.String())
	}
}
