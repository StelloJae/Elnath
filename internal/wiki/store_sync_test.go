package wiki

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeIndexSync captures Upsert/Remove calls so tests can assert that Store
// syncs the index alongside file writes without wiring a real SQLite DB.
type fakeIndexSync struct {
	upserts   map[string]*Page
	upsertLog []string
	removed   []string
	upsertErr error
}

func newFakeIndexSync() *fakeIndexSync {
	return &fakeIndexSync{upserts: map[string]*Page{}}
}

func (f *fakeIndexSync) Upsert(page *Page) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	copied := *page
	f.upserts[page.Path] = &copied
	f.upsertLog = append(f.upsertLog, page.Path)
	return nil
}

func (f *fakeIndexSync) Remove(path string) error {
	f.removed = append(f.removed, path)
	return nil
}

func TestStore_Create_SyncsIndex(t *testing.T) {
	fake := newFakeIndexSync()
	s, err := NewStore(t.TempDir(), WithIndex(fake))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	page := samplePage("sync/create.md")
	if err := s.Create(page); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, ok := fake.upserts[page.Path]
	if !ok {
		t.Fatalf("index.Upsert not called for %q; upserts=%v", page.Path, fake.upserts)
	}
	if got.Title != page.Title {
		t.Errorf("upserted title = %q, want %q", got.Title, page.Title)
	}
	if got.Updated.IsZero() {
		t.Errorf("upserted page Updated should be set")
	}
}

func TestStore_Update_SyncsIndex(t *testing.T) {
	fake := newFakeIndexSync()
	s, err := NewStore(t.TempDir(), WithIndex(fake))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	page := samplePage("sync/update.md")
	if err := s.Create(page); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reread, err := s.Read(page.Path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	reread.Content = "updated content"
	if err := s.Update(reread); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(fake.upsertLog) != 2 {
		t.Fatalf("upsertLog = %v, want 2 calls (create + update)", fake.upsertLog)
	}
	got := fake.upserts[page.Path]
	if !strings.Contains(got.Content, "updated content") {
		t.Errorf("upserted content = %q, want to contain %q", got.Content, "updated content")
	}
}

func TestStore_Delete_SyncsIndex(t *testing.T) {
	fake := newFakeIndexSync()
	s, err := NewStore(t.TempDir(), WithIndex(fake))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	page := samplePage("sync/delete.md")
	if err := s.Create(page); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Delete(page.Path); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if len(fake.removed) != 1 || fake.removed[0] != page.Path {
		t.Errorf("removed = %v, want [%q]", fake.removed, page.Path)
	}
}

func TestStore_Upsert_SyncsIndex_NewAndExisting(t *testing.T) {
	fake := newFakeIndexSync()
	s, err := NewStore(t.TempDir(), WithIndex(fake))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	page := samplePage("sync/upsert.md")
	if err := s.Upsert(page); err != nil {
		t.Fatalf("Upsert new: %v", err)
	}

	reread, err := s.Read(page.Path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	reread.Content = "upserted body"
	if err := s.Upsert(reread); err != nil {
		t.Fatalf("Upsert existing: %v", err)
	}

	if len(fake.upsertLog) != 2 {
		t.Fatalf("upsertLog = %v, want 2 calls (new + existing)", fake.upsertLog)
	}
	got := fake.upserts[page.Path]
	if !strings.Contains(got.Content, "upserted body") {
		t.Errorf("re-upserted content = %q, want to contain %q", got.Content, "upserted body")
	}
}

func TestStore_WithoutIndex_SkipsSync(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	page := samplePage("sync/nosync.md")
	if err := s.Create(page); err != nil {
		t.Fatalf("Create without index: %v", err)
	}

	reread, err := s.Read(page.Path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	reread.Content = "body"
	if err := s.Update(reread); err != nil {
		t.Fatalf("Update without index: %v", err)
	}
	if err := s.Delete(page.Path); err != nil {
		t.Fatalf("Delete without index: %v", err)
	}
}

func TestStore_Create_IndexError_ReturnsError_KeepsFile(t *testing.T) {
	fake := newFakeIndexSync()
	fake.upsertErr = errors.New("fts down")

	dir := t.TempDir()
	s, err := NewStore(dir, WithIndex(fake))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	page := samplePage("sync/error.md")
	err = s.Create(page)
	if err == nil {
		t.Fatal("expected Create to return error when index sync fails")
	}
	if !strings.Contains(err.Error(), "fts down") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "fts down")
	}

	if _, err := os.Stat(filepath.Join(dir, page.Path)); err != nil {
		t.Errorf("file missing after failed sync: %v (integrity tool should resync, not silent delete)", err)
	}
}
