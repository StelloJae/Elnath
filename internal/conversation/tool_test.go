package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stello/elnath/internal/llm"
	_ "modernc.org/sqlite"
)

func openToolTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestConversationSearchTool_Name(t *testing.T) {
	db := openToolTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	tool := NewConversationSearchTool(store)

	if got := tool.Name(); got != "conversation_search" {
		t.Errorf("Name() = %q, want %q", got, "conversation_search")
	}
}

func TestConversationSearchTool_Schema(t *testing.T) {
	db := openToolTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	tool := NewConversationSearchTool(store)

	schema := tool.Schema()
	if len(schema) == 0 {
		t.Fatal("Schema() returned empty JSON")
	}

	var obj map[string]any
	if err := json.Unmarshal(schema, &obj); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}

	props, ok := obj["properties"].(map[string]any)
	if !ok {
		t.Fatal("Schema() missing 'properties'")
	}
	if _, ok := props["query"]; !ok {
		t.Error("Schema() missing 'query' property")
	}
	if _, ok := props["limit"]; !ok {
		t.Error("Schema() missing 'limit' property")
	}
}

func TestConversationSearchTool_Execute_Found(t *testing.T) {
	db := openToolTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	if err := store.Save(ctx, "sess-1", []llm.Message{
		llm.NewUserMessage("the quick brown fox"),
		llm.NewAssistantMessage("jumps over the lazy dog"),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tool := NewConversationSearchTool(store)

	params, _ := json.Marshal(map[string]any{"query": "quick brown"})
	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	if result.Output == "" {
		t.Fatal("Execute returned empty output")
	}
}

func TestConversationSearchTool_Execute_NoResults(t *testing.T) {
	db := openToolTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	tool := NewConversationSearchTool(store)

	params, _ := json.Marshal(map[string]any{"query": "xyzzynonexistentterm"})
	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	if result.Output == "" {
		t.Fatal("Execute returned empty output for no-results case")
	}
}

func TestConversationSearchTool_Execute_DefaultLimit(t *testing.T) {
	db := openToolTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	for i := 0; i < 15; i++ {
		if err := store.Save(ctx, "sess-many", []llm.Message{
			llm.NewUserMessage("findme message content"),
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	tool := NewConversationSearchTool(store)

	// No limit specified — should default to 10.
	params, _ := json.Marshal(map[string]any{"query": "findme"})
	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
}

func TestConversationSearchTool_Execute_MissingQuery(t *testing.T) {
	db := openToolTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	tool := NewConversationSearchTool(store)

	params, _ := json.Marshal(map[string]any{})
	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatal("Execute should return an error result when query is missing")
	}
}

func TestConversationSearchTool_Execute_InvalidParams(t *testing.T) {
	db := openToolTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	tool := NewConversationSearchTool(store)

	result, err := tool.Execute(ctx, json.RawMessage(`not-valid-json`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.IsError {
		t.Fatal("Execute should return an error result for invalid JSON params")
	}
}
