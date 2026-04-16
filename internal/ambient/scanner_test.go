package ambient

import (
	"log/slog"
	"testing"

	"github.com/stello/elnath/internal/wiki"
)

func newTestScanner(t *testing.T) (*Scanner, *wiki.Store) {
	t.Helper()
	store, err := wiki.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	logger := slog.Default()
	return NewScanner(store, logger), store
}

func createBootPage(t *testing.T, store *wiki.Store, path, title, schedule string, silent bool) {
	t.Helper()
	extra := map[string]any{
		"schedule": schedule,
	}
	if silent {
		extra["silent"] = true
	}
	page := &wiki.Page{
		Path:    path,
		Title:   title,
		Type:    wiki.PageTypeBootTask,
		Content: "Do the thing",
		Extra:   extra,
	}
	if err := store.Create(page); err != nil {
		t.Fatalf("Create %s: %v", path, err)
	}
}

func TestScansBootTasks(t *testing.T) {
	scanner, store := newTestScanner(t)

	// Valid boot task.
	createBootPage(t, store, "boot/morning.md", "Morning Briefing", "startup", false)

	// Non-boot-task type in boot/ dir — should be skipped.
	if err := store.Create(&wiki.Page{
		Path:    "boot/notes.md",
		Title:   "Notes",
		Type:    wiki.PageTypeConcept,
		Content: "just notes",
	}); err != nil {
		t.Fatalf("Create notes: %v", err)
	}

	// Valid boot task outside boot/ dir — should be skipped.
	if err := store.Create(&wiki.Page{
		Path:  "other/task.md",
		Title: "Other Task",
		Type:  wiki.PageTypeBootTask,
		Content: "other content",
		Extra: map[string]any{"schedule": "startup"},
	}); err != nil {
		t.Fatalf("Create other/task.md: %v", err)
	}

	tasks, err := scanner.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Scan returned %d tasks, want 1", len(tasks))
	}
	if tasks[0].Path != "boot/morning.md" {
		t.Errorf("task path = %q, want %q", tasks[0].Path, "boot/morning.md")
	}
	if tasks[0].Schedule.Type != ScheduleStartup {
		t.Errorf("schedule type = %v, want ScheduleStartup", tasks[0].Schedule.Type)
	}
}

func TestEmptyBootDir(t *testing.T) {
	scanner, _ := newTestScanner(t)

	tasks, err := scanner.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("Scan returned %d tasks, want 0", len(tasks))
	}
}

func TestInvalidScheduleSkipped(t *testing.T) {
	scanner, store := newTestScanner(t)

	// Bad schedule — should be skipped.
	createBootPage(t, store, "boot/bad.md", "Bad Task", "invalid-format", false)

	// Good schedule — should be returned.
	createBootPage(t, store, "boot/good.md", "Good Task", "every 1h", false)

	tasks, err := scanner.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Scan returned %d tasks, want 1", len(tasks))
	}
	if tasks[0].Path != "boot/good.md" {
		t.Errorf("task path = %q, want %q", tasks[0].Path, "boot/good.md")
	}
}
