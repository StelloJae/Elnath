package magicdocs

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

func TestParseExtractionResult_ValidJSON(t *testing.T) {
	raw := `{"pages": [{"action": "create", "path": "analyses/test.md", "title": "Test", "type": "analysis", "content": "Body", "confidence": "medium", "tags": ["go"]}]}`
	result, err := parseExtractionResult(raw)
	if err != nil {
		t.Fatalf("parseExtractionResult: %v", err)
	}
	if len(result.Pages) != 1 {
		t.Fatalf("Pages count = %d, want 1", len(result.Pages))
	}
	if result.Pages[0].Title != "Test" {
		t.Errorf("Title = %q, want %q", result.Pages[0].Title, "Test")
	}
}

func TestParseExtractionResult_MarkdownFenced(t *testing.T) {
	raw := "```json\n{\"pages\": [{\"action\": \"create\", \"path\": \"a/b.md\", \"title\": \"T\", \"type\": \"analysis\", \"content\": \"C\", \"confidence\": \"low\", \"tags\": []}]}\n```"
	result, err := parseExtractionResult(raw)
	if err != nil {
		t.Fatalf("parseExtractionResult: %v", err)
	}
	if len(result.Pages) != 1 {
		t.Fatalf("Pages count = %d, want 1", len(result.Pages))
	}
}

func TestParseExtractionResult_EmptyPages(t *testing.T) {
	raw := `{"pages": []}`
	result, err := parseExtractionResult(raw)
	if err != nil {
		t.Fatalf("parseExtractionResult: %v", err)
	}
	if len(result.Pages) != 0 {
		t.Errorf("Pages count = %d, want 0", len(result.Pages))
	}
}

func TestParseExtractionResult_InvalidJSON(t *testing.T) {
	_, err := parseExtractionResult("not json at all")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidatePageAction(t *testing.T) {
	tests := []struct {
		name    string
		action  PageAction
		wantErr bool
	}{
		{"valid create", PageAction{Action: "create", Path: "analyses/x.md", Type: "analysis", Confidence: "medium"}, false},
		{"valid update", PageAction{Action: "update", Path: "concepts/y.md", Type: "concept", Confidence: "high"}, false},
		{"bad action", PageAction{Action: "delete", Path: "a/b.md", Type: "analysis", Confidence: "low"}, true},
		{"bad type", PageAction{Action: "create", Path: "a/b.md", Type: "unknown", Confidence: "low"}, true},
		{"bad confidence", PageAction{Action: "create", Path: "a/b.md", Type: "analysis", Confidence: "ultra"}, true},
		{"path traversal", PageAction{Action: "create", Path: "../../etc/passwd", Type: "analysis", Confidence: "low"}, true},
		{"empty path", PageAction{Action: "create", Path: "", Type: "analysis", Confidence: "low"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePageAction(tt.action)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePageAction() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

type mockProvider struct {
	response string
	err      error
}

func (m *mockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.response}, nil
}
func (m *mockProvider) Stream(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
	return nil
}
func (m *mockProvider) Name() string            { return "mock" }
func (m *mockProvider) Models() []llm.ModelInfo { return nil }

func TestExtractor_ExtractAndWrite(t *testing.T) {
	store, err := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	provider := &mockProvider{
		response: `{"pages": [{"action": "create", "path": "analyses/discovery.md", "title": "Discovery", "type": "analysis", "content": "Found something", "confidence": "medium", "tags": ["test"]}]}`,
	}

	logger := slog.Default()
	writer := NewWikiWriter(store, logger)
	ext := NewExtractor(provider, "test-model", writer, logger)

	ch := make(chan ExtractionRequest, 1)
	base := event.NewBaseWith(time.Now(), "test-session")
	ch <- ExtractionRequest{
		Events: []event.Event{
			event.ResearchProgressEvent{Base: base, Phase: "conclusion", Round: 3, Message: "found it"},
			event.AgentFinishEvent{Base: base, FinishReason: "end_turn"},
		},
		SessionID: "test-session",
		Trigger:   "agent_finish",
		Timestamp: time.Now(),
	}
	close(ch)

	ctx := context.Background()
	ext.Run(ctx, ch)

	page, err := store.Read("analyses/discovery.md")
	if err != nil {
		t.Fatalf("wiki page not created: %v", err)
	}
	if page.Title != "Discovery" {
		t.Errorf("Title = %q, want %q", page.Title, "Discovery")
	}
}

func TestExtractor_SkipsWhenNoSignal(t *testing.T) {
	store, _ := wiki.NewStore(filepath.Join(t.TempDir(), "wiki"))
	provider := &mockProvider{response: `should not be called`}
	logger := slog.Default()
	writer := NewWikiWriter(store, logger)
	ext := NewExtractor(provider, "test-model", writer, logger)

	ch := make(chan ExtractionRequest, 1)
	base := event.NewBaseWith(time.Now(), "test-session")
	ch <- ExtractionRequest{
		Events: []event.Event{
			event.TextDeltaEvent{Base: base, Content: "just text"},
		},
		SessionID: "test-session",
		Trigger:   "agent_finish",
		Timestamp: time.Now(),
	}
	close(ch)

	ext.Run(context.Background(), ch)
	pages, _ := store.List()
	if len(pages) != 0 {
		t.Errorf("expected no pages, got %d", len(pages))
	}
}
