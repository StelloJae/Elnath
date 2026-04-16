package wiki

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWikiWriteToolExecuteStampsAgentSource(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	tool := NewWikiWriteTool(store)

	params := json.RawMessage(`{"path":"concepts/note.md","title":"Note","content":"body","type":"concept"}`)
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute result.IsError = true, output = %q", result.Output)
	}

	page, err := store.Read("concepts/note.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := page.PageSource(); got != SourceAgent {
		t.Errorf("page source = %q, want %q", got, SourceAgent)
	}
}
