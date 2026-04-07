package wiki

import (
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func samplePage(path string) *Page {
	return &Page{
		Path:    path,
		Title:   "Test Page",
		Type:    PageTypeConcept,
		Content: "This is the body content.",
		Tags:    []string{"go", "test"},
	}
}

func TestStoreCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	page := samplePage("concepts/first.md")

	if err := s.Create(page); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Read("concepts/first.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got.Title != page.Title {
		t.Errorf("Title = %q, want %q", got.Title, page.Title)
	}
	if got.Type != page.Type {
		t.Errorf("Type = %q, want %q", got.Type, page.Type)
	}
	// RenderFrontmatter appends a trailing newline to content; trim before comparing.
	wantContent := strings.TrimRight(page.Content, "\n")
	gotContent := strings.TrimRight(got.Content, "\n")
	if gotContent != wantContent {
		t.Errorf("Content = %q, want %q", gotContent, wantContent)
	}
	if len(got.Tags) != len(page.Tags) {
		t.Errorf("Tags = %v, want %v", got.Tags, page.Tags)
	}
	if got.Created.IsZero() {
		t.Errorf("Created timestamp should not be zero")
	}
}

func TestStoreList(t *testing.T) {
	s := newTestStore(t)

	paths := []string{"p1.md", "p2.md", "p3.md"}
	for i, p := range paths {
		page := &Page{
			Path:    p,
			Title:   "Page " + string(rune('A'+i)),
			Type:    PageTypeConcept,
			Content: "body",
		}
		if err := s.Create(page); err != nil {
			t.Fatalf("Create %s: %v", p, err)
		}
	}

	pages, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pages) != len(paths) {
		t.Errorf("List returned %d pages, want %d", len(pages), len(paths))
	}
}

func TestStoreUpdate(t *testing.T) {
	s := newTestStore(t)
	page := samplePage("update_me.md")

	if err := s.Create(page); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Read it back so we have the Created timestamp set by Create.
	got, err := s.Read("update_me.md")
	if err != nil {
		t.Fatalf("Read before update: %v", err)
	}

	got.Content = "updated body content"
	// Ensure Updated will differ from Created.
	time.Sleep(time.Millisecond)

	if err := s.Update(got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	after, err := s.Read("update_me.md")
	if err != nil {
		t.Fatalf("Read after update: %v", err)
	}
	if strings.TrimRight(after.Content, "\n") != "updated body content" {
		t.Errorf("Content after update = %q, want %q", after.Content, "updated body content")
	}
}

func TestStoreDelete(t *testing.T) {
	s := newTestStore(t)
	page := samplePage("delete_me.md")

	if err := s.Create(page); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Delete("delete_me.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Read("delete_me.md")
	if err == nil {
		t.Errorf("Read after Delete should return error, got nil")
	}
}
