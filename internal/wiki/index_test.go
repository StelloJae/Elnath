package wiki

import (
	"context"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestStoreForIndex creates a Store backed by a fresh temp directory.
// Reuses newTestIndex from search_test.go for the DB side.
func newTestStoreForIndex(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// createStorePages writes n pages to the store with predictable, searchable bodies.
func createStorePages(t *testing.T, store *Store, n int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 1; i <= n; i++ {
		page := &Page{
			Path:    fmt.Sprintf("p%d.md", i),
			Title:   fmt.Sprintf("Page %d", i),
			Type:    PageTypeEntity,
			Content: fmt.Sprintf("Content body %d keyword lorem ipsum.", i),
			Created: now,
			Updated: now,
		}
		if err := store.Create(page); err != nil {
			t.Fatalf("Create %q: %v", page.Path, err)
		}
	}
}

func TestRebuild_InvokesProgressCallback(t *testing.T) {
	idx := newTestIndex(t)
	store := newTestStoreForIndex(t)
	createStorePages(t, store, 3)

	type progressEvent struct{ done, total int }
	var events []progressEvent

	err := idx.Rebuild(store, WithRebuildProgress(func(done, total int) {
		events = append(events, progressEvent{done, total})
	}))
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d progress events, want 3", len(events))
	}
	for i, ev := range events {
		if ev.total != 3 {
			t.Errorf("event %d: total=%d, want 3", i, ev.total)
		}
		if ev.done != i+1 {
			t.Errorf("event %d: done=%d, want %d", i, ev.done, i+1)
		}
	}
}

func TestRebuild_ReinsertsAllPagesSoSearchFinds(t *testing.T) {
	idx := newTestIndex(t)
	store := newTestStoreForIndex(t)
	createStorePages(t, store, 5)

	if err := idx.Rebuild(store); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	results, err := idx.Search(context.Background(), SearchOpts{Query: "keyword", Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("got %d results, want 5", len(results))
	}
}

func TestCheckIntegrity_FreshDBMatchesStore(t *testing.T) {
	idx := newTestIndex(t)
	store := newTestStoreForIndex(t)
	createStorePages(t, store, 4)
	if err := idx.Rebuild(store); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	report, err := idx.CheckIntegrity(store)
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if report.PagesInStore != 4 {
		t.Errorf("PagesInStore=%d, want 4", report.PagesInStore)
	}
	if report.PagesInDB != 4 {
		t.Errorf("PagesInDB=%d, want 4", report.PagesInDB)
	}
	if report.Drift() {
		t.Errorf("Drift()=true on fresh synced DB, want false")
	}
	if idx.HasFTS5() && report.FTS5Check != "ok" {
		t.Errorf("FTS5Check=%q, want ok", report.FTS5Check)
	}
}

func TestCheckIntegrity_DetectsDriftWhenDBBehindStore(t *testing.T) {
	idx := newTestIndex(t)
	store := newTestStoreForIndex(t)
	createStorePages(t, store, 5)

	// Index only 3 pages: simulate drift (store-only writes without sync).
	for i := 1; i <= 3; i++ {
		page, err := store.Read(fmt.Sprintf("p%d.md", i))
		if err != nil {
			t.Fatalf("Read %d: %v", i, err)
		}
		if err := idx.Upsert(page); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
	}
	report, err := idx.CheckIntegrity(store)
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if report.PagesInStore != 5 {
		t.Errorf("PagesInStore=%d, want 5", report.PagesInStore)
	}
	if report.PagesInDB != 3 {
		t.Errorf("PagesInDB=%d, want 3", report.PagesInDB)
	}
	if !report.Drift() {
		t.Errorf("Drift()=false, want true (2-page gap)")
	}
}

func TestCheckIntegrity_TriggersInstalled(t *testing.T) {
	idx := newTestIndex(t)
	if !idx.HasFTS5() {
		t.Skip("FTS5 not available in this SQLite build")
	}
	store := newTestStoreForIndex(t)
	report, err := idx.CheckIntegrity(store)
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	want := map[string]bool{
		"wiki_pages_ai": false,
		"wiki_pages_ad": false,
		"wiki_pages_au": false,
	}
	for _, name := range report.Triggers {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("trigger %q missing", name)
		}
	}
	if !report.TriggersOK {
		t.Errorf("TriggersOK=false, want true")
	}
}

func TestCheckIntegrity_EmptyStoreEmptyDB(t *testing.T) {
	idx := newTestIndex(t)
	store := newTestStoreForIndex(t)
	report, err := idx.CheckIntegrity(store)
	if err != nil {
		t.Fatalf("CheckIntegrity: %v", err)
	}
	if report.PagesInStore != 0 {
		t.Errorf("PagesInStore=%d, want 0", report.PagesInStore)
	}
	if report.PagesInDB != 0 {
		t.Errorf("PagesInDB=%d, want 0", report.PagesInDB)
	}
	if report.Drift() {
		t.Errorf("Drift()=true on empty store/DB, want false")
	}
}
