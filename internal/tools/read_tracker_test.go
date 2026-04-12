package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadTrackerDedup(t *testing.T) {
	dir := t.TempDir()
	path := writeTrackedFile(t, dir, "file.txt", "alpha\nbeta\n")
	tracker := NewReadTracker()

	if got := tracker.CheckRead(path, 2, 1); got != "" {
		t.Fatalf("first read = %q, want empty", got)
	}
	got := tracker.CheckRead(path, 2, 1)
	if !strings.Contains(got, "File unchanged since last read at line 2-2") {
		t.Fatalf("second read = %q", got)
	}
}

func TestReadTrackerConsecutiveBlock(t *testing.T) {
	dir := t.TempDir()
	path := writeTrackedFile(t, dir, "file.txt", "alpha\n")
	tracker := NewReadTracker()

	if got := tracker.CheckRead(path, 1, 1); got != "" {
		t.Fatalf("first read = %q, want empty", got)
	}
	if got := tracker.CheckRead(path, 1, 1); !strings.Contains(got, "File unchanged") {
		t.Fatalf("second read = %q", got)
	}
	if got := tracker.CheckRead(path, 1, 1); !strings.Contains(got, "WARNING") {
		t.Fatalf("third read = %q, want warning", got)
	}
	if got := tracker.CheckRead(path, 1, 1); !strings.Contains(got, "BLOCKED") {
		t.Fatalf("fourth read = %q, want blocked", got)
	}
}

func TestReadTrackerResetOnOtherTool(t *testing.T) {
	dir := t.TempDir()
	path := writeTrackedFile(t, dir, "file.txt", "alpha\n")
	tracker := NewReadTracker()

	_ = tracker.CheckRead(path, 1, 1)
	_ = tracker.CheckRead(path, 1, 1)
	tracker.NotifyTool("edit_file")
	got := tracker.CheckRead(path, 1, 1)
	if strings.Contains(got, "WARNING") || strings.Contains(got, "BLOCKED") {
		t.Fatalf("read after reset = %q, want plain unchanged stub", got)
	}
}

func TestReadTrackerAllowsAfterModification(t *testing.T) {
	dir := t.TempDir()
	path := writeTrackedFile(t, dir, "file.txt", "alpha\n")
	tracker := NewReadTracker()

	_ = tracker.CheckRead(path, 1, 1)
	if got := tracker.CheckRead(path, 1, 1); !strings.Contains(got, "File unchanged") {
		t.Fatalf("dedup read = %q", got)
	}
	writeTrackedFile(t, dir, "file.txt", "beta\n")
	if got := tracker.CheckRead(path, 1, 1); got != "" {
		t.Fatalf("read after modification = %q, want empty", got)
	}
}

func TestReadTrackerGrepDedup(t *testing.T) {
	dir := t.TempDir()
	path := writeTrackedFile(t, dir, "file.txt", "alpha\n")
	tracker := NewReadTracker()

	if got := tracker.CheckGrep(path, "alpha"); got != "" {
		t.Fatalf("first grep = %q, want empty", got)
	}
	got := tracker.CheckGrep(path, "alpha")
	if !strings.Contains(got, "Search unchanged since last grep") {
		t.Fatalf("second grep = %q", got)
	}
}

func TestReadTrackerGrepConsecutiveBlock(t *testing.T) {
	dir := t.TempDir()
	path := writeTrackedFile(t, dir, "file.txt", "alpha\n")
	tracker := NewReadTracker()

	_ = tracker.CheckGrep(path, "alpha")
	_ = tracker.CheckGrep(path, "alpha")
	if got := tracker.CheckGrep(path, "alpha"); !strings.Contains(got, "WARNING") {
		t.Fatalf("third grep = %q, want warning", got)
	}
	if got := tracker.CheckGrep(path, "alpha"); !strings.Contains(got, "BLOCKED") {
		t.Fatalf("fourth grep = %q, want blocked", got)
	}
}

func TestReadTrackerResetDedup(t *testing.T) {
	dir := t.TempDir()
	path := writeTrackedFile(t, dir, "file.txt", "alpha\n")
	tracker := NewReadTracker()

	_ = tracker.CheckRead(path, 1, 1)
	_ = tracker.CheckRead(path, 1, 1)
	tracker.ResetDedup()
	if got := tracker.CheckRead(path, 1, 1); got != "" {
		t.Fatalf("read after ResetDedup = %q, want empty", got)
	}
}

func TestReadTrackerRefreshAfterWrite(t *testing.T) {
	dir := t.TempDir()
	guard := NewPathGuard(dir, nil)
	tracker := NewReadTracker()
	readTool := NewReadTool(guard, tracker)
	writeTool := NewWriteTool(guard, tracker)

	if _, err := writeTool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "file.txt",
		"content":   "alpha\n",
	})); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	if _, err := readTool.Execute(context.Background(), mustMarshal(t, map[string]any{"file_path": "file.txt"})); err != nil {
		t.Fatalf("first read: %v", err)
	}
	res, err := readTool.Execute(context.Background(), mustMarshal(t, map[string]any{"file_path": "file.txt"}))
	if err != nil {
		t.Fatalf("dedup read: %v", err)
	}
	if !strings.Contains(res.Output, "File unchanged") {
		t.Fatalf("dedup output = %q", res.Output)
	}
	if _, err := writeTool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "file.txt",
		"content":   "beta\n",
	})); err != nil {
		t.Fatalf("refreshing write: %v", err)
	}
	res, err = readTool.Execute(context.Background(), mustMarshal(t, map[string]any{"file_path": "file.txt"}))
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	if strings.Contains(res.Output, "File unchanged") {
		t.Fatalf("read after write returned stale stub: %q", res.Output)
	}
}

func TestReadTrackerRefreshAfterEdit(t *testing.T) {
	dir := t.TempDir()
	guard := NewPathGuard(dir, nil)
	tracker := NewReadTracker()
	readTool := NewReadTool(guard, tracker)
	writeTool := NewWriteTool(guard, tracker)
	editTool := NewEditTool(guard, tracker)

	if _, err := writeTool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path": "file.txt",
		"content":   "alpha\n",
	})); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	if _, err := readTool.Execute(context.Background(), mustMarshal(t, map[string]any{"file_path": "file.txt"})); err != nil {
		t.Fatalf("first read: %v", err)
	}
	if _, err := editTool.Execute(context.Background(), mustMarshal(t, map[string]any{
		"file_path":  "file.txt",
		"old_string": "alpha",
		"new_string": "beta",
	})); err != nil {
		t.Fatalf("edit: %v", err)
	}
	res, err := readTool.Execute(context.Background(), mustMarshal(t, map[string]any{"file_path": "file.txt"}))
	if err != nil {
		t.Fatalf("read after edit: %v", err)
	}
	if strings.Contains(res.Output, "File unchanged") {
		t.Fatalf("read after edit returned stale stub: %q", res.Output)
	}
	if !strings.Contains(res.Output, "beta") {
		t.Fatalf("read after edit = %q, want updated content", res.Output)
	}
}

func writeTrackedFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	time.Sleep(5 * time.Millisecond)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
