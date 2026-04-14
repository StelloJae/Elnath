package learning

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreAppendSetsDefaultsAndWritesJSONL(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "lessons.jsonl")
	store := NewStore(path)

	lesson := Lesson{Text: "Prefer tighter experiments.", Source: "topic-a", Confidence: "high"}
	if err := store.Append(lesson); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1", len(lines))
	}

	var stored Lesson
	if err := json.Unmarshal([]byte(lines[0]), &stored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if stored.ID == "" {
		t.Fatal("ID = empty, want derived ID")
	}
	if stored.ID != deriveID(lesson.Text) {
		t.Fatalf("ID = %q, want %q", stored.ID, deriveID(lesson.Text))
	}
	if stored.Created.IsZero() {
		t.Fatal("Created = zero, want auto timestamp")
	}
	if stored.Created.Location() != time.UTC {
		t.Fatalf("Created location = %v, want UTC", stored.Created.Location())
	}

	if err := store.Append(lesson); err != nil {
		t.Fatalf("second Append() error = %v", err)
	}
	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 2 {
		t.Fatalf("List length = %d, want 2", len(lessons))
	}
	if lessons[0].ID != lessons[1].ID {
		t.Fatalf("IDs = %q and %q, want identical hashes for identical text", lessons[0].ID, lessons[1].ID)
	}
}

func TestStoreListPreservesAppendOrder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "lessons.jsonl")
	store := NewStore(path)
	base := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

	for i, text := range []string{"first", "second", "third"} {
		if err := store.Append(Lesson{
			Text:       text,
			Source:     "topic-a",
			Confidence: "medium",
			Created:    base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("Append(%q) error = %v", text, err)
		}
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 3 {
		t.Fatalf("List length = %d, want 3", len(lessons))
	}
	for i, want := range []string{"first", "second", "third"} {
		if lessons[i].Text != want {
			t.Fatalf("lessons[%d].Text = %q, want %q", i, lessons[i].Text, want)
		}
	}
}

func TestStoreRecentReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "lessons.jsonl")
	store := NewStore(path)
	base := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)

	for i, text := range []string{"oldest", "middle", "newest"} {
		if err := store.Append(Lesson{
			Text:       text,
			Source:     "topic-a",
			Confidence: "high",
			Created:    base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("Append(%q) error = %v", text, err)
		}
	}

	recent, err := store.Recent(2)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("Recent length = %d, want 2", len(recent))
	}
	if recent[0].Text != "newest" || recent[1].Text != "middle" {
		t.Fatalf("Recent order = [%q, %q], want [newest, middle]", recent[0].Text, recent[1].Text)
	}
}

func TestStoreListHandlesMissingAndEmptyFile(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		store := NewStore(filepath.Join(t.TempDir(), "missing.jsonl"))
		lessons, err := store.List()
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if lessons != nil {
			t.Fatalf("List() = %#v, want nil slice", lessons)
		}
	})

	t.Run("empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.jsonl")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		store := NewStore(path)
		lessons, err := store.List()
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if lessons != nil {
			t.Fatalf("List() = %#v, want nil slice", lessons)
		}
	})
}

func TestStoreListHandlesLargeEntry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "large.jsonl")
	store := NewStore(path)
	lesson := Lesson{
		Text:       strings.Repeat("payload-", 200000),
		Topic:      "large",
		Source:     "src",
		Confidence: "high",
		Created:    time.Date(2026, 4, 13, 12, 30, 0, 0, time.UTC),
	}
	if err := store.Append(lesson); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 1 {
		t.Fatalf("List length = %d, want 1", len(lessons))
	}
	if lessons[0].Text != lesson.Text {
		t.Fatalf("stored text length = %d, want %d", len(lessons[0].Text), len(lesson.Text))
	}
}

func TestStoreConcurrentAppend(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "lessons.jsonl")
	store := NewStore(path)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		for j := 0; j < 5; j++ {
			wg.Add(1)
			go func(i, j int) {
				defer wg.Done()
				lesson := Lesson{
					Text:       strings.Join([]string{"lesson", string(rune('A' + i)), string(rune('a' + j))}, "-"),
					Source:     "concurrent",
					Confidence: "medium",
				}
				if err := store.Append(lesson); err != nil {
					t.Errorf("Append() error = %v", err)
				}
			}(i, j)
		}
	}
	wg.Wait()

	lessons, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(lessons) != 50 {
		t.Fatalf("List length = %d, want 50", len(lessons))
	}
}

func TestStoreAppendWithRedactor(t *testing.T) {
	t.Parallel()

	t.Run("topic text source all redacted", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "lessons.jsonl")
		store := NewStore(path, WithRedactor(func(s string) string {
			return strings.ReplaceAll(s, "SECRET", "[X]")
		}))

		err := store.Append(Lesson{
			Text:   "contains SECRET token",
			Topic:  "SECRET-topic",
			Source: "SECRET-source",
		})
		if err != nil {
			t.Fatalf("Append() error = %v", err)
		}

		got, err := store.List()
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List length = %d, want 1", len(got))
		}
		if strings.Contains(got[0].Text, "SECRET") {
			t.Fatalf("Text not redacted: %q", got[0].Text)
		}
		if strings.Contains(got[0].Topic, "SECRET") {
			t.Fatalf("Topic not redacted: %q", got[0].Topic)
		}
		if strings.Contains(got[0].Source, "SECRET") {
			t.Fatalf("Source not redacted: %q", got[0].Source)
		}
		if !strings.Contains(got[0].Text, "[X]") || !strings.Contains(got[0].Topic, "[X]") || !strings.Contains(got[0].Source, "[X]") {
			t.Fatalf("redacted lesson = %#v, want [X] markers in text/topic/source", got[0])
		}
	})

	t.Run("id derived from redacted text", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "lessons.jsonl")
		store := NewStore(path, WithRedactor(func(s string) string {
			return strings.ReplaceAll(s, "SECRET", "[X]")
		}))

		original := Lesson{Text: "contains SECRET token"}
		if err := store.Append(original); err != nil {
			t.Fatalf("Append() error = %v", err)
		}

		got, err := store.List()
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List length = %d, want 1", len(got))
		}
		if got[0].ID != deriveID(got[0].Text) {
			t.Fatalf("ID = %q, want deriveID(redacted text) = %q", got[0].ID, deriveID(got[0].Text))
		}
		if got[0].ID == deriveID(original.Text) {
			t.Fatalf("ID = %q, want redacted-text-derived hash instead of original", got[0].ID)
		}
	})
}

func TestStoreAppendNilRedactor(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "lessons.jsonl")
	store := NewStore(path)

	if err := store.Append(Lesson{Text: "keeps SECRET literal"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	got, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List length = %d, want 1", len(got))
	}
	if !strings.Contains(got[0].Text, "SECRET") {
		t.Fatalf("stored text = %q, want literal retained with no redactor", got[0].Text)
	}
}

func TestStoreRotateArchiveDoesNotReRedact(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "lessons.jsonl")
	calls := 0
	store := NewStore(path, WithRedactor(func(s string) string {
		calls++
		return strings.ReplaceAll(s, "Z", "z")
	}))

	for i := 0; i < 3; i++ {
		if err := store.Append(Lesson{
			Text:    fmt.Sprintf("entry %d with Z", i),
			Created: time.Now().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	callsAfterAppend := calls

	moved, err := store.Rotate(RotateOpts{KeepLast: 1})
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if moved != 2 {
		t.Fatalf("Rotate() moved = %d, want 2", moved)
	}
	if calls != callsAfterAppend {
		t.Fatalf("redactor called during rotate: before=%d after=%d", callsAfterAppend, calls)
	}
}

func TestStoreAppendWithRedactorConcurrent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "lessons.jsonl")
	store := NewStore(path, WithRedactor(func(s string) string {
		return strings.ReplaceAll(s, "S", "s")
	}))

	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				if err := store.Append(Lesson{Text: fmt.Sprintf("g=%d i=%d S", gID, i)}); err != nil {
					t.Errorf("Append() error = %v", err)
				}
			}
		}(g)
	}
	wg.Wait()

	got, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 50 {
		t.Fatalf("List length = %d, want 50", len(got))
	}
	for _, lesson := range got {
		if strings.ContainsRune(lesson.Text, 'S') {
			t.Fatalf("stored text = %q, want redacted lowercase s", lesson.Text)
		}
	}
}

func TestStoreListFiltered(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	lessons := []Lesson{
		{Text: "alpha-high", Topic: "alpha", Source: "src", Confidence: "high", Created: base},
		{Text: "bravo-medium", Topic: "bravo", Source: "src", Confidence: "medium", Created: base.Add(1 * time.Minute)},
		{Text: "charlie-low", Topic: "charlie", Source: "src", Confidence: "low", Created: base.Add(2 * time.Minute)},
		{Text: "alpha-ops-high", Topic: "alpha-ops", Source: "src", Confidence: "high", Created: base.Add(3 * time.Minute)},
		{Text: "delta-medium", Topic: "delta", Source: "src", Confidence: "medium", Created: base.Add(4 * time.Minute)},
	}
	targetPrefix := deriveID(lessons[4].Text)[:4]

	tests := []struct {
		name      string
		filter    Filter
		wantTexts []string
		wantErr   string
	}{
		{
			name:      "topic",
			filter:    Filter{Topic: "alpha"},
			wantTexts: []string{"alpha-high", "alpha-ops-high"},
		},
		{
			name:      "confidence",
			filter:    Filter{Confidence: "high"},
			wantTexts: []string{"alpha-high", "alpha-ops-high"},
		},
		{
			name:      "since inclusive",
			filter:    Filter{Since: base.Add(3 * time.Minute)},
			wantTexts: []string{"alpha-ops-high", "delta-medium"},
		},
		{
			name:      "before exclusive",
			filter:    Filter{Before: base.Add(3 * time.Minute)},
			wantTexts: []string{"alpha-high", "bravo-medium", "charlie-low"},
		},
		{
			name:      "limit",
			filter:    Filter{Limit: 2},
			wantTexts: []string{"alpha-high", "bravo-medium"},
		},
		{
			name:      "reverse newest first",
			filter:    Filter{Reverse: true},
			wantTexts: []string{"delta-medium", "alpha-ops-high", "charlie-low", "bravo-medium", "alpha-high"},
		},
		{
			name:      "id prefix",
			filter:    Filter{IDs: []string{targetPrefix}},
			wantTexts: []string{"delta-medium"},
		},
		{
			name:    "short id prefix",
			filter:  Filter{IDs: []string{"abc"}},
			wantErr: "learning store: id prefix must be at least 4 chars",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lessons.jsonl")
			store := NewStore(path)
			appendLessons(t, store, lessons)

			got, err := store.ListFiltered(tt.filter)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ListFiltered() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ListFiltered() error = %v", err)
			}
			assertLessonTexts(t, got, tt.wantTexts)
		})
	}
}

func TestStoreDelete(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 13, 0, 0, 0, time.UTC)
	seed := []Lesson{
		{Text: "delete-one", Topic: "alpha", Source: "src", Confidence: "high", Created: base},
		{Text: "delete-two", Topic: "bravo", Source: "src", Confidence: "medium", Created: base.Add(1 * time.Minute)},
		{Text: "keep-three", Topic: "charlie", Source: "src", Confidence: "low", Created: base.Add(2 * time.Minute)},
	}

	tests := []struct {
		name          string
		prefixes      []string
		wantRemoved   int
		wantRemaining []string
		wantUnchanged bool
		wantErr       string
	}{
		{
			name:          "matching prefixes",
			prefixes:      []string{deriveID(seed[0].Text)[:4], deriveID(seed[1].Text)[:4]},
			wantRemoved:   2,
			wantRemaining: []string{"keep-three"},
		},
		{
			name:          "missing prefix",
			prefixes:      []string{"deadbeef"},
			wantRemoved:   0,
			wantRemaining: []string{"delete-one", "delete-two", "keep-three"},
			wantUnchanged: true,
		},
		{
			name:          "blank prefixes",
			prefixes:      []string{"", "   "},
			wantRemoved:   0,
			wantRemaining: []string{"delete-one", "delete-two", "keep-three"},
			wantUnchanged: true,
		},
		{
			name:          "no prefixes",
			prefixes:      nil,
			wantRemoved:   0,
			wantRemaining: []string{"delete-one", "delete-two", "keep-three"},
			wantUnchanged: true,
		},
		{
			name:          "short prefix",
			prefixes:      []string{"abc"},
			wantErr:       "learning store: id prefix must be at least 4 chars",
			wantRemaining: []string{"delete-one", "delete-two", "keep-three"},
			wantUnchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lessons.jsonl")
			store := NewStore(path)
			appendLessons(t, store, seed)

			before := readFileSnapshot(t, path)
			removed, err := store.Delete(tt.prefixes...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Delete() error = %v, want containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if removed != tt.wantRemoved {
				t.Fatalf("Delete() removed = %d, want %d", removed, tt.wantRemoved)
			}

			remaining, err := store.List()
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			assertLessonTexts(t, remaining, tt.wantRemaining)

			if tt.wantUnchanged {
				after := readFileSnapshot(t, path)
				if before != after {
					t.Fatalf("file changed for no-op delete")
				}
			}
		})
	}
}

func TestStoreDeleteMatching(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 14, 0, 0, 0, time.UTC)
	seed := []Lesson{
		{Text: "alpha-one", Topic: "alpha", Source: "src", Confidence: "high", Created: base},
		{Text: "alpha-two", Topic: "alpha-team", Source: "src", Confidence: "medium", Created: base.Add(1 * time.Minute)},
		{Text: "bravo-three", Topic: "bravo", Source: "src", Confidence: "low", Created: base.Add(2 * time.Minute)},
	}

	tests := []struct {
		name          string
		filter        Filter
		wantRemoved   int
		wantRemaining []string
		wantErr       string
		wantUnchanged bool
	}{
		{
			name:          "topic filter",
			filter:        Filter{Topic: "alpha"},
			wantRemoved:   2,
			wantRemaining: []string{"bravo-three"},
		},
		{
			name:          "zero filter",
			filter:        Filter{},
			wantErr:       "DeleteMatching requires at least one filter",
			wantRemaining: []string{"alpha-one", "alpha-two", "bravo-three"},
			wantUnchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lessons.jsonl")
			store := NewStore(path)
			appendLessons(t, store, seed)
			before := readFileSnapshot(t, path)

			removed, err := store.DeleteMatching(tt.filter)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("DeleteMatching() error = %v, want containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("DeleteMatching() error = %v", err)
			}
			if removed != tt.wantRemoved {
				t.Fatalf("DeleteMatching() removed = %d, want %d", removed, tt.wantRemoved)
			}

			remaining, listErr := store.List()
			if listErr != nil {
				t.Fatalf("List() error = %v", listErr)
			}
			assertLessonTexts(t, remaining, tt.wantRemaining)

			if tt.wantUnchanged {
				after := readFileSnapshot(t, path)
				if before != after {
					t.Fatalf("file changed for rejected delete matching")
				}
			}
		})
	}
}

func TestStoreClear(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 15, 0, 0, 0, time.UTC)
	seed := []Lesson{
		{Text: "clear-one", Topic: "alpha", Source: "src", Confidence: "high", Created: base},
		{Text: "clear-two", Topic: "bravo", Source: "src", Confidence: "medium", Created: base.Add(1 * time.Minute)},
		{Text: "clear-three", Topic: "charlie", Source: "src", Confidence: "low", Created: base.Add(2 * time.Minute)},
	}

	tests := []struct {
		name              string
		precreateEmpty    bool
		precreateArchive  bool
		wantRemoved       int
		wantRemainingSize int
	}{
		{
			name:              "clears active and keeps archive",
			precreateArchive:  true,
			wantRemoved:       3,
			wantRemainingSize: 0,
		},
		{
			name:              "empty file noop",
			precreateEmpty:    true,
			wantRemoved:       0,
			wantRemainingSize: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lessons.jsonl")
			store := NewStore(path)
			if !tt.precreateEmpty {
				appendLessons(t, store, seed)
			} else if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			archiveSize := int64(-1)
			if tt.precreateArchive {
				archivePath := store.archivePath()
				if err := os.WriteFile(archivePath, []byte("archived\n"), 0o600); err != nil {
					t.Fatalf("WriteFile(archive) error = %v", err)
				}
				archiveSize = fileSize(t, archivePath)
			}

			removed, err := store.Clear()
			if err != nil {
				t.Fatalf("Clear() error = %v", err)
			}
			if removed != tt.wantRemoved {
				t.Fatalf("Clear() removed = %d, want %d", removed, tt.wantRemoved)
			}

			lessons, err := store.List()
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(lessons) != tt.wantRemainingSize {
				t.Fatalf("List length = %d, want %d", len(lessons), tt.wantRemainingSize)
			}

			if tt.precreateArchive {
				if got := fileSize(t, store.archivePath()); got != archiveSize {
					t.Fatalf("archive size = %d, want %d", got, archiveSize)
				}
			}
		})
	}
}

func TestStoreRotate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		assert func(t *testing.T, store *Store, path string)
	}{
		{
			name: "keep last",
			assert: func(t *testing.T, store *Store, path string) {
				lessons := makeSequentialLessons("keep", 5, time.Date(2026, 4, 13, 16, 0, 0, 0, time.UTC))
				appendLessons(t, store, lessons)

				moved, err := store.Rotate(RotateOpts{KeepLast: 2})
				if err != nil {
					t.Fatalf("Rotate() error = %v", err)
				}
				if moved != 3 {
					t.Fatalf("Rotate() moved = %d, want 3", moved)
				}

				active, err := store.List()
				if err != nil {
					t.Fatalf("List() error = %v", err)
				}
				assertLessonTexts(t, active, []string{"keep-3", "keep-4"})
				if got := countJSONLLines(t, store.archivePath()); got != 3 {
					t.Fatalf("archive entries = %d, want 3", got)
				}
			},
		},
		{
			name: "no op with large keep",
			assert: func(t *testing.T, store *Store, path string) {
				appendLessons(t, store, makeSequentialLessons("noop", 5, time.Date(2026, 4, 13, 17, 0, 0, 0, time.UTC)))

				moved, err := store.Rotate(RotateOpts{KeepLast: 10})
				if err != nil {
					t.Fatalf("Rotate() error = %v", err)
				}
				if moved != 0 {
					t.Fatalf("Rotate() moved = %d, want 0", moved)
				}

				active, err := store.List()
				if err != nil {
					t.Fatalf("List() error = %v", err)
				}
				if len(active) != 5 {
					t.Fatalf("List length = %d, want 5", len(active))
				}
				if got := countJSONLLines(t, store.archivePath()); got != 0 {
					t.Fatalf("archive entries = %d, want 0", got)
				}
			},
		},
		{
			name: "max bytes keeps active within limit",
			assert: func(t *testing.T, store *Store, path string) {
				lessons := makeSkewedLessons(time.Date(2026, 4, 13, 18, 0, 0, 0, time.UTC))
				appendLessons(t, store, lessons)
				maxBytes, err := encodedLessonSize(lessons[len(lessons)-1])
				if err != nil {
					t.Fatalf("encodedLessonSize() error = %v", err)
				}
				maxBytes += 128

				moved, err := store.Rotate(RotateOpts{MaxBytes: maxBytes})
				if err != nil {
					t.Fatalf("Rotate() error = %v", err)
				}
				if moved <= 0 {
					t.Fatalf("Rotate() moved = %d, want > 0", moved)
				}

				after := fileSize(t, path)
				if after > maxBytes {
					t.Fatalf("active size = %d, want <= %d", after, maxBytes)
				}

				active, err := store.List()
				if err != nil {
					t.Fatalf("List() error = %v", err)
				}
				if got := len(active) + countJSONLLines(t, store.archivePath()); got != 8 {
					t.Fatalf("active+archive count = %d, want 8", got)
				}
			},
		},
		{
			name: "requires bound",
			assert: func(t *testing.T, store *Store, path string) {
				moved, err := store.Rotate(RotateOpts{})
				if err == nil || !strings.Contains(err.Error(), "Rotate requires KeepLast or MaxBytes") {
					t.Fatalf("Rotate() error = %v, want requires bound", err)
				}
				if moved != 0 {
					t.Fatalf("Rotate() moved = %d, want 0", moved)
				}
			},
		},
		{
			name: "archive appends on second rotate",
			assert: func(t *testing.T, store *Store, path string) {
				appendLessons(t, store, makeSequentialLessons("archive", 5, time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)))

				first, err := store.Rotate(RotateOpts{KeepLast: 4})
				if err != nil {
					t.Fatalf("first Rotate() error = %v", err)
				}
				if first != 1 {
					t.Fatalf("first Rotate() moved = %d, want 1", first)
				}

				second, err := store.Rotate(RotateOpts{KeepLast: 2})
				if err != nil {
					t.Fatalf("second Rotate() error = %v", err)
				}
				if second != 2 {
					t.Fatalf("second Rotate() moved = %d, want 2", second)
				}

				if got := countJSONLLines(t, store.archivePath()); got != 3 {
					t.Fatalf("archive entries = %d, want 3", got)
				}
			},
		},
		{
			name: "keeps newest lesson when single entry exceeds max bytes",
			assert: func(t *testing.T, store *Store, path string) {
				appendLessons(t, store, []Lesson{{
					Text:       strings.Repeat("payload-", 400),
					Topic:      "oversized",
					Source:     "src",
					Confidence: "high",
					Created:    time.Date(2026, 4, 13, 20, 0, 0, 0, time.UTC),
				}})

				moved, err := store.Rotate(RotateOpts{MaxBytes: 64})
				if err != nil {
					t.Fatalf("Rotate() error = %v", err)
				}
				if moved != 0 {
					t.Fatalf("Rotate() moved = %d, want 0", moved)
				}

				active, err := store.List()
				if err != nil {
					t.Fatalf("List() error = %v", err)
				}
				if len(active) != 1 {
					t.Fatalf("active count = %d, want 1", len(active))
				}
				if got := countJSONLLines(t, store.archivePath()); got != 0 {
					t.Fatalf("archive entries = %d, want 0", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lessons.jsonl")
			store := NewStore(path)
			tt.assert(t, store, path)
		})
	}
}

func TestStoreAutoRotateIfNeeded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(t *testing.T, store *Store)
		opts      RotateOpts
		wantMoved int
		wantTotal int
	}{
		{
			name:      "empty store",
			prepare:   func(t *testing.T, store *Store) {},
			opts:      RotateOpts{KeepLast: 10},
			wantMoved: 0,
			wantTotal: 0,
		},
		{
			name: "keep last above active count",
			prepare: func(t *testing.T, store *Store) {
				appendLessons(t, store, makeSequentialLessons("keepmany", 5, time.Date(2026, 4, 13, 20, 0, 0, 0, time.UTC)))
			},
			opts:      RotateOpts{KeepLast: 10},
			wantMoved: 0,
			wantTotal: 5,
		},
		{
			name: "rotates when active exceeds keep last",
			prepare: func(t *testing.T, store *Store) {
				appendLessons(t, store, makeSequentialLessons("autorotate", 5, time.Date(2026, 4, 13, 21, 0, 0, 0, time.UTC)))
			},
			opts:      RotateOpts{KeepLast: 2},
			wantMoved: 3,
			wantTotal: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lessons.jsonl")
			store := NewStore(path)
			tt.prepare(t, store)

			moved, err := store.AutoRotateIfNeeded(tt.opts)
			if err != nil {
				t.Fatalf("AutoRotateIfNeeded() error = %v", err)
			}
			if moved != tt.wantMoved {
				t.Fatalf("AutoRotateIfNeeded() moved = %d, want %d", moved, tt.wantMoved)
			}

			active, err := store.List()
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if got := len(active) + countJSONLLines(t, store.archivePath()); got != tt.wantTotal {
				t.Fatalf("active+archive count = %d, want %d", got, tt.wantTotal)
			}
		})
	}
}

func TestStoreSummary(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 13, 22, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		lessons []Lesson
		assert  func(t *testing.T, stats Stats)
	}{
		{
			name:    "empty store",
			lessons: nil,
			assert: func(t *testing.T, stats Stats) {
				if stats.Total != 0 {
					t.Fatalf("Total = %d, want 0", stats.Total)
				}
				if len(stats.ByTopic) != 0 {
					t.Fatalf("ByTopic length = %d, want 0", len(stats.ByTopic))
				}
				if len(stats.ByConfidence) != 0 {
					t.Fatalf("ByConfidence length = %d, want 0", len(stats.ByConfidence))
				}
			},
		},
		{
			name: "populated store",
			lessons: []Lesson{
				{Text: "summary-a", Topic: "alpha", Source: "src", Confidence: "high", Created: base},
				{Text: "summary-b", Topic: "bravo", Source: "src", Confidence: "medium", Created: base.Add(1 * time.Minute)},
				{Text: "summary-c", Topic: "bravo", Source: "src", Confidence: "high", Created: base.Add(2 * time.Minute)},
			},
			assert: func(t *testing.T, stats Stats) {
				if stats.Total != 3 {
					t.Fatalf("Total = %d, want 3", stats.Total)
				}
				if stats.ByTopic["alpha"] != 1 || stats.ByTopic["bravo"] != 2 {
					t.Fatalf("ByTopic = %#v, want alpha=1 bravo=2", stats.ByTopic)
				}
				if stats.ByConfidence["high"] != 2 || stats.ByConfidence["medium"] != 1 {
					t.Fatalf("ByConfidence = %#v, want high=2 medium=1", stats.ByConfidence)
				}
				if !stats.OldestAt.Equal(base) {
					t.Fatalf("OldestAt = %v, want %v", stats.OldestAt, base)
				}
				if !stats.NewestAt.Equal(base.Add(2 * time.Minute)) {
					t.Fatalf("NewestAt = %v, want %v", stats.NewestAt, base.Add(2*time.Minute))
				}
				if stats.FileBytes <= 0 {
					t.Fatalf("FileBytes = %d, want > 0", stats.FileBytes)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lessons.jsonl")
			store := NewStore(path)
			appendLessons(t, store, tt.lessons)

			stats, err := store.Summary()
			if err != nil {
				t.Fatalf("Summary() error = %v", err)
			}
			tt.assert(t, stats)
		})
	}
}

func TestStoreNilSafeOperations(t *testing.T) {
	t.Parallel()

	var store *Store
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "list filtered",
			run: func(t *testing.T) {
				lessons, err := store.ListFiltered(Filter{Topic: "alpha"})
				if err != nil {
					t.Fatalf("ListFiltered() error = %v", err)
				}
				if lessons != nil {
					t.Fatalf("ListFiltered() = %#v, want nil slice", lessons)
				}
			},
		},
		{
			name: "delete",
			run: func(t *testing.T) {
				removed, err := store.Delete("abcd")
				if err != nil {
					t.Fatalf("Delete() error = %v", err)
				}
				if removed != 0 {
					t.Fatalf("Delete() removed = %d, want 0", removed)
				}
			},
		},
		{
			name: "delete matching",
			run: func(t *testing.T) {
				removed, err := store.DeleteMatching(Filter{Topic: "alpha"})
				if err != nil {
					t.Fatalf("DeleteMatching() error = %v", err)
				}
				if removed != 0 {
					t.Fatalf("DeleteMatching() removed = %d, want 0", removed)
				}
			},
		},
		{
			name: "clear",
			run: func(t *testing.T) {
				removed, err := store.Clear()
				if err != nil {
					t.Fatalf("Clear() error = %v", err)
				}
				if removed != 0 {
					t.Fatalf("Clear() removed = %d, want 0", removed)
				}
			},
		},
		{
			name: "rotate",
			run: func(t *testing.T) {
				removed, err := store.Rotate(RotateOpts{KeepLast: 1})
				if err != nil {
					t.Fatalf("Rotate() error = %v", err)
				}
				if removed != 0 {
					t.Fatalf("Rotate() moved = %d, want 0", removed)
				}
			},
		},
		{
			name: "auto rotate",
			run: func(t *testing.T) {
				removed, err := store.AutoRotateIfNeeded(RotateOpts{KeepLast: 1})
				if err != nil {
					t.Fatalf("AutoRotateIfNeeded() error = %v", err)
				}
				if removed != 0 {
					t.Fatalf("AutoRotateIfNeeded() moved = %d, want 0", removed)
				}
			},
		},
		{
			name: "summary",
			run: func(t *testing.T) {
				stats, err := store.Summary()
				if err != nil {
					t.Fatalf("Summary() error = %v", err)
				}
				if stats.Total != 0 || len(stats.ByTopic) != 0 || len(stats.ByConfidence) != 0 {
					t.Fatalf("Summary() = %#v, want zero stats", stats)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic = %v", r)
				}
			}()
			tt.run(t)
		})
	}
}

func TestStoreConcurrentRotateAndAppend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "rotate and append"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lessons.jsonl")
			store := NewStore(path)
			appendLessons(t, store, makeSequentialLessons("seed", 5, time.Date(2026, 4, 13, 23, 0, 0, 0, time.UTC)))

			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					for j := 0; j < 5; j++ {
						lesson := Lesson{
							Text:       fmt.Sprintf("concurrent-%d-%d", i, j),
							Topic:      "concurrent",
							Source:     "src",
							Confidence: "medium",
							Created:    time.Date(2026, 4, 14, 0, i, j, 0, time.UTC),
						}
						if err := store.Append(lesson); err != nil {
							t.Errorf("Append() error = %v", err)
						}
					}
				}(i)
			}

			close(start)
			for i := 0; i < 3; i++ {
				if _, err := store.Rotate(RotateOpts{KeepLast: 3}); err != nil {
					t.Fatalf("Rotate() error = %v", err)
				}
				time.Sleep(5 * time.Millisecond)
			}
			wg.Wait()

			active, err := store.List()
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if got := len(active) + countJSONLLines(t, store.archivePath()); got != 55 {
				t.Fatalf("active+archive count = %d, want 55", got)
			}
		})
	}
}

func appendLessons(t *testing.T, store *Store, lessons []Lesson) {
	t.Helper()
	for _, lesson := range lessons {
		if err := store.Append(lesson); err != nil {
			t.Fatalf("Append(%q) error = %v", lesson.Text, err)
		}
	}
}

func assertLessonTexts(t *testing.T, lessons []Lesson, want []string) {
	t.Helper()
	if len(lessons) != len(want) {
		t.Fatalf("lesson count = %d, want %d", len(lessons), len(want))
	}
	for i, lesson := range lessons {
		if lesson.Text != want[i] {
			t.Fatalf("lessons[%d].Text = %q, want %q", i, lesson.Text, want[i])
		}
	}
}

func readFileSnapshot(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(data)
}

func countJSONLLines(t *testing.T, path string) int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Scanner(%q) error = %v", path, err)
	}
	return count
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	return fi.Size()
}

func makeSequentialLessons(prefix string, count int, base time.Time) []Lesson {
	lessons := make([]Lesson, 0, count)
	for i := 0; i < count; i++ {
		lessons = append(lessons, Lesson{
			Text:       fmt.Sprintf("%s-%d", prefix, i),
			Topic:      prefix,
			Source:     "src",
			Confidence: "medium",
			Created:    base.Add(time.Duration(i) * time.Minute),
		})
	}
	return lessons
}

func makeLargeLessons(count int, base time.Time) []Lesson {
	lessons := make([]Lesson, 0, count)
	for i := 0; i < count; i++ {
		lessons = append(lessons, Lesson{
			Text:       fmt.Sprintf("large-%02d-%s", i, strings.Repeat("payload-", 16)),
			Topic:      "large",
			Source:     "src",
			Confidence: "high",
			Created:    base.Add(time.Duration(i) * time.Minute),
		})
	}
	return lessons
}

func makeSkewedLessons(base time.Time) []Lesson {
	lessons := make([]Lesson, 0, 8)
	for i := 0; i < 6; i++ {
		lessons = append(lessons, Lesson{
			Text:       fmt.Sprintf("small-%02d", i),
			Topic:      "small",
			Source:     "src",
			Confidence: "medium",
			Created:    base.Add(time.Duration(i) * time.Minute),
		})
	}
	for i := 0; i < 2; i++ {
		lessons = append(lessons, Lesson{
			Text:       fmt.Sprintf("large-tail-%02d-%s", i, strings.Repeat("payload-", 8000)),
			Topic:      "large",
			Source:     "src",
			Confidence: "high",
			Created:    base.Add(time.Duration(6+i) * time.Minute),
		})
	}
	return lessons
}
