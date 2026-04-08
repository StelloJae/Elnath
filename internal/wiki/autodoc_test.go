package wiki

import (
	"context"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

// TestAutoDocumenterEmptyMessages verifies that IngestSession with empty messages is a no-op.
func TestAutoDocumenterEmptyMessages(t *testing.T) {
	store := newTestStore(t)
	ad := NewAutoDocumenter(store, nil, nil)

	ctx := context.Background()
	if err := ad.IngestSession(ctx, "sess-empty", nil); err != nil {
		t.Fatalf("IngestSession with nil messages: %v", err)
	}

	pages, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	if len(pages) != 0 {
		t.Errorf("expected no pages for empty messages, got %d", len(pages))
	}
}

// TestAutoDocumenterCreatesWikiPage verifies that IngestSession creates a wiki page for the session.
func TestAutoDocumenterCreatesWikiPage(t *testing.T) {
	store := newTestStore(t)
	ad := NewAutoDocumenter(store, nil, nil)

	sessionID := "sess-autodoc-001"
	messages := []llm.Message{
		llm.NewUserMessage("Hello from auto-doc test."),
		llm.NewUserMessage("Another turn."),
	}

	ctx := context.Background()
	if err := ad.IngestSession(ctx, sessionID, messages); err != nil {
		t.Fatalf("IngestSession: %v", err)
	}

	wantPath := "sources/conversations/" + sessionID + ".md"
	page, err := store.Read(wantPath)
	if err != nil {
		t.Fatalf("store.Read(%q): %v", wantPath, err)
	}

	if !strings.Contains(page.Content, "Hello from auto-doc test.") {
		t.Errorf("first message missing from wiki page content")
	}
	if !strings.Contains(page.Content, "Another turn.") {
		t.Errorf("second message missing from wiki page content")
	}
}

// TestAutoDocumenterNilProvider verifies that IngestSession works without an LLM provider.
func TestAutoDocumenterNilProvider(t *testing.T) {
	store := newTestStore(t)
	ad := NewAutoDocumenter(store, nil, nil)

	sessionID := "sess-autodoc-noprovider"
	messages := []llm.Message{
		llm.NewUserMessage("What is Go?"),
	}

	ctx := context.Background()
	if err := ad.IngestSession(ctx, sessionID, messages); err != nil {
		t.Fatalf("IngestSession with nil provider: %v", err)
	}

	wantPath := "sources/conversations/" + sessionID + ".md"
	page, err := store.Read(wantPath)
	if err != nil {
		t.Fatalf("store.Read(%q): %v", wantPath, err)
	}

	if strings.Contains(page.Content, "## Summary") {
		t.Errorf("unexpected ## Summary section when provider is nil")
	}
	if !strings.Contains(page.Content, "What is Go?") {
		t.Errorf("message content missing from wiki page")
	}
}
