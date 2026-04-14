package learning

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCursorStoreGetMissingFile(t *testing.T) {
	t.Parallel()

	store := NewCursorStore(filepath.Join(t.TempDir(), "cursor.jsonl"))
	got, err := store.Get("session-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("Get() = %d, want 0", got)
	}
}

func TestCursorStoreUpdateThenGet(t *testing.T) {
	t.Parallel()

	store := NewCursorStore(filepath.Join(t.TempDir(), "cursor.jsonl"))
	if err := store.Update("session-1", 7); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got, err := store.Get("session-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != 7 {
		t.Fatalf("Get() = %d, want 7", got)
	}
}

func TestCursorStoreGetReturnsLargestLinePerSession(t *testing.T) {
	t.Parallel()

	store := NewCursorStore(filepath.Join(t.TempDir(), "cursor.jsonl"))
	for _, update := range []struct {
		session string
		line    int
	}{
		{session: "session-1", line: 3},
		{session: "session-2", line: 10},
		{session: "session-1", line: 9},
		{session: "session-2", line: 6},
	} {
		if err := store.Update(update.session, update.line); err != nil {
			t.Fatalf("Update(%q, %d) error = %v", update.session, update.line, err)
		}
	}

	got1, err := store.Get("session-1")
	if err != nil {
		t.Fatalf("Get(session-1) error = %v", err)
	}
	if got1 != 9 {
		t.Fatalf("Get(session-1) = %d, want 9", got1)
	}
	got2, err := store.Get("session-2")
	if err != nil {
		t.Fatalf("Get(session-2) error = %v", err)
	}
	if got2 != 10 {
		t.Fatalf("Get(session-2) = %d, want 10", got2)
	}
}

func TestCursorStoreSkipsMalformedLines(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cursor.jsonl")
	data := "{\"session_id\":\"session-1\",\"last_line\":3,\"updated_at\":\"2026-04-14T00:00:00Z\"}\nnot-json\n{\"session_id\":\"session-1\",\"last_line\":8,\"updated_at\":\"2026-04-14T00:00:01Z\"}\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := NewCursorStore(path).Get("session-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != 8 {
		t.Fatalf("Get() = %d, want 8", got)
	}
}

func TestCursorStoreConcurrentUpdate(t *testing.T) {
	t.Parallel()

	store := NewCursorStore(filepath.Join(t.TempDir(), "cursor.jsonl"))
	const goroutines = 10
	const perGoroutine = 100

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				line := g*perGoroutine + i + 1
				if err := store.Update("session-1", line); err != nil {
					t.Errorf("Update() error = %v", err)
				}
			}
		}()
	}
	wg.Wait()

	got, err := store.Get("session-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	want := goroutines * perGoroutine
	if got != want {
		t.Fatalf("Get() = %d, want %d", got, want)
	}
}

func TestCursorStoreEmptySessionIDNoop(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cursor.jsonl")
	store := NewCursorStore(path)
	got, err := store.Get("")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("Get() = %d, want 0", got)
	}
	if err := store.Update("", 5); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cursor file stat err = %v, want not exists", err)
	}
}

func TestCursorStoreCreatesParentDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "dir", "cursor.jsonl")
	store := NewCursorStore(path)
	if err := store.Update("session-1", 4); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got, err := store.Get("session-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != 4 {
		t.Fatalf("Get() = %d, want 4", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
}

func TestCursorStoreInterleavedSessions(t *testing.T) {
	t.Parallel()

	store := NewCursorStore(filepath.Join(t.TempDir(), "cursor.jsonl"))
	for i := 1; i <= 5; i++ {
		if err := store.Update("alpha", i); err != nil {
			t.Fatalf("Update(alpha, %d) error = %v", i, err)
		}
		if err := store.Update("beta", i*10); err != nil {
			t.Fatalf("Update(beta, %d) error = %v", i*10, err)
		}
	}

	for session, want := range map[string]int{"alpha": 5, "beta": 50} {
		got, err := store.Get(session)
		if err != nil {
			t.Fatalf("Get(%q) error = %v", session, err)
		}
		if got != want {
			t.Fatalf("Get(%q) = %d, want %d", session, got, want)
		}
	}
}

func BenchmarkCursorStoreGet(b *testing.B) {
	path := filepath.Join(b.TempDir(), "cursor.jsonl")
	store := NewCursorStore(path)
	for i := 0; i < 1000; i++ {
		if err := store.Update("session-bench", i); err != nil {
			b.Fatalf("Update() error = %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.Get("session-bench"); err != nil {
			b.Fatalf("Get() error = %v", err)
		}
	}
}

func ExampleCursorStore() {
	path := filepath.Join(os.TempDir(), "elnath-cursor-example.jsonl")
	store := NewCursorStore(path)
	_ = store.Update("session-1", 12)
	line, _ := store.Get("session-1")
	fmt.Println(line)
	_ = os.Remove(path)
	// Output: 12
}
