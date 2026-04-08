package wiki

import (
	"context"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ProjectName", "projectname"},
		{"My Cool Project", "my-cool-project"},
		{"hello_world!", "helloworld"},
		{"  spaces  ", "spaces"},
		{"Go 1.21", "go-121"},
		{"already-slug", "already-slug"},
		{"UPPER CASE", "upper-case"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseExtractionResult(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		raw := `{
			"entities": [
				{"name": "Elnath", "type": "project", "summary": "An AI assistant platform", "facts": ["Written in Go", "Uses SQLite"]}
			],
			"concepts": [
				{"name": "Agent Loop", "summary": "Core execution loop", "related": ["Elnath"]}
			]
		}`

		result, err := parseExtractionResult(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Entities) != 1 {
			t.Fatalf("want 1 entity, got %d", len(result.Entities))
		}
		if result.Entities[0].Name != "Elnath" {
			t.Errorf("entity name: want %q, got %q", "Elnath", result.Entities[0].Name)
		}
		if len(result.Entities[0].Facts) != 2 {
			t.Errorf("want 2 facts, got %d", len(result.Entities[0].Facts))
		}
		if len(result.Concepts) != 1 {
			t.Fatalf("want 1 concept, got %d", len(result.Concepts))
		}
		if result.Concepts[0].Name != "Agent Loop" {
			t.Errorf("concept name: want %q, got %q", "Agent Loop", result.Concepts[0].Name)
		}
	})

	t.Run("JSON wrapped in markdown fences", func(t *testing.T) {
		raw := "```json\n{\"entities\":[], \"concepts\":[]}\n```"

		result, err := parseExtractionResult(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Entities) != 0 {
			t.Errorf("want 0 entities, got %d", len(result.Entities))
		}
	})

	t.Run("empty result", func(t *testing.T) {
		raw := `{"entities":[],"concepts":[]}`

		result, err := parseExtractionResult(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Entities) != 0 || len(result.Concepts) != 0 {
			t.Errorf("expected empty result")
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, err := parseExtractionResult("not json at all")
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

// extractMockProvider returns a fixed extraction JSON response.
type extractMockProvider struct {
	response string
}

func (m *extractMockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: m.response}, nil
}

func (m *extractMockProvider) Stream(_ context.Context, _ llm.ChatRequest, _ func(llm.StreamEvent)) error {
	return nil
}

func (m *extractMockProvider) Name() string           { return "extract-mock" }
func (m *extractMockProvider) Models() []llm.ModelInfo { return nil }

func TestExtractFromConversation(t *testing.T) {
	t.Run("creates entity and concept pages", func(t *testing.T) {
		store := newTestStore(t)
		mockResp := `{
			"entities": [
				{"name": "Elnath", "type": "project", "summary": "AI assistant platform", "facts": ["Written in Go"]}
			],
			"concepts": [
				{"name": "Agent Loop", "summary": "Core execution pattern", "related": ["Elnath"]}
			]
		}`

		ke := NewKnowledgeExtractor(store, &extractMockProvider{response: mockResp}, nil)

		messages := []llm.Message{
			llm.NewUserMessage("Tell me about Elnath"),
			llm.NewAssistantMessage("Elnath is an AI assistant platform written in Go."),
		}

		err := ke.ExtractFromConversation(context.Background(), "test-session", messages)
		if err != nil {
			t.Fatalf("ExtractFromConversation: %v", err)
		}

		entityPage, err := store.Read("entities/elnath.md")
		if err != nil {
			t.Fatalf("read entity page: %v", err)
		}
		if entityPage.Type != PageTypeEntity {
			t.Errorf("entity page type: want %q, got %q", PageTypeEntity, entityPage.Type)
		}
		if entityPage.Title != "Elnath" {
			t.Errorf("entity title: want %q, got %q", "Elnath", entityPage.Title)
		}
		if !strings.Contains(entityPage.Content, "Written in Go") {
			t.Errorf("entity content missing fact, got:\n%s", entityPage.Content)
		}

		conceptPage, err := store.Read("concepts/agent-loop.md")
		if err != nil {
			t.Fatalf("read concept page: %v", err)
		}
		if conceptPage.Type != PageTypeConcept {
			t.Errorf("concept page type: want %q, got %q", PageTypeConcept, conceptPage.Type)
		}
		if !strings.Contains(conceptPage.Content, "Elnath") {
			t.Errorf("concept content missing related entity, got:\n%s", conceptPage.Content)
		}
	})

	t.Run("appends facts to existing entity", func(t *testing.T) {
		store := newTestStore(t)

		existingPage := &Page{
			Path:       "entities/elnath.md",
			Title:      "Elnath",
			Type:       PageTypeEntity,
			Content:    "## Elnath\n\n**Type:** project\n\nAI platform\n\n### Facts\n\n- Written in Go\n",
			Tags:       []string{"project"},
			Confidence: "medium",
		}
		if err := store.Create(existingPage); err != nil {
			t.Fatalf("create existing page: %v", err)
		}

		mockResp := `{
			"entities": [
				{"name": "Elnath", "type": "project", "summary": "AI platform", "facts": ["Written in Go", "Uses SQLite"]}
			],
			"concepts": []
		}`

		ke := NewKnowledgeExtractor(store, &extractMockProvider{response: mockResp}, nil)

		messages := []llm.Message{
			llm.NewUserMessage("What database does Elnath use?"),
			llm.NewAssistantMessage("Elnath uses SQLite."),
		}

		err := ke.ExtractFromConversation(context.Background(), "test-session-2", messages)
		if err != nil {
			t.Fatalf("ExtractFromConversation: %v", err)
		}

		page, err := store.Read("entities/elnath.md")
		if err != nil {
			t.Fatalf("read entity page: %v", err)
		}
		if !strings.Contains(page.Content, "Uses SQLite") {
			t.Errorf("expected new fact 'Uses SQLite' to be appended, got:\n%s", page.Content)
		}
		if strings.Count(page.Content, "Written in Go") != 1 {
			t.Errorf("duplicate fact 'Written in Go' should not be added again")
		}
	})

	t.Run("empty messages returns nil", func(t *testing.T) {
		store := newTestStore(t)
		ke := NewKnowledgeExtractor(store, &extractMockProvider{response: "{}"}, nil)

		err := ke.ExtractFromConversation(context.Background(), "empty", nil)
		if err != nil {
			t.Fatalf("expected nil for empty messages, got: %v", err)
		}
	})

	t.Run("invalid LLM response degrades gracefully", func(t *testing.T) {
		store := newTestStore(t)
		ke := NewKnowledgeExtractor(store, &extractMockProvider{response: "not json"}, nil)

		messages := []llm.Message{llm.NewUserMessage("hello")}

		err := ke.ExtractFromConversation(context.Background(), "bad-resp", messages)
		if err != nil {
			t.Fatalf("expected graceful degradation (nil error), got: %v", err)
		}

		pages, err := store.List()
		if err != nil {
			t.Fatalf("store.List: %v", err)
		}
		if len(pages) != 0 {
			t.Errorf("expected no pages created for invalid response, got %d", len(pages))
		}
	})
}
