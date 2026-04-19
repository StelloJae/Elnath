package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/wiki"
)

// TestOpenWikiStoreWithIndex_SyncsWrites proves the helper wires Store and
// Index so a Store.Create is immediately visible via FTS search — the
// whole point of FU-WikiWriteSync.
func TestOpenWikiStoreWithIndex_SyncsWrites(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		WikiDir: filepath.Join(dir, "wiki"),
		DataDir: dir,
	}

	store, db, err := openWikiStoreWithIndex(cfg)
	if err != nil {
		t.Fatalf("openWikiStoreWithIndex: %v", err)
	}
	if store == nil || db == nil {
		t.Fatalf("expected non-nil store and db, got store=%v db=%v", store, db)
	}
	t.Cleanup(func() { db.Close() })

	page := &wiki.Page{
		Path:    "concepts/hello.md",
		Title:   "Hello",
		Type:    wiki.PageTypeConcept,
		Content: "greetings from the helper test",
	}
	if err := store.Create(page); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	idx, err := wiki.NewIndex(db.Wiki)
	if err != nil {
		t.Fatalf("wiki.NewIndex: %v", err)
	}
	results, err := idx.Search(context.Background(), wiki.SearchOpts{Query: "greetings", Limit: 10})
	if err != nil {
		t.Fatalf("idx.Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("store.Create succeeded but FTS search returned nothing — helper failed to wire Index")
	}
}

// TestOpenWikiStoreWithIndex_EmptyWikiDir keeps callers who pass a blank
// WikiDir working: they see (nil, nil, nil) and skip wiki setup.
func TestOpenWikiStoreWithIndex_EmptyWikiDir(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}

	store, db, err := openWikiStoreWithIndex(cfg)
	if err != nil {
		t.Fatalf("expected nil error for empty WikiDir, got %v", err)
	}
	if store != nil || db != nil {
		t.Errorf("expected (nil,nil) for empty WikiDir, got store=%v db=%v", store, db)
	}
}
