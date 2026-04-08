package conversation

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stello/elnath/internal/llm"
	_ "modernc.org/sqlite"
)

func openTestMainDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestHistoryStore(t *testing.T) *DBHistoryStore {
	t.Helper()
	db := openTestMainDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return NewHistoryStore(db)
}

func saveTestMessages(t *testing.T, store *DBHistoryStore, sessionID string, msgs ...string) {
	t.Helper()
	var messages []llm.Message
	for _, m := range msgs {
		messages = append(messages, llm.NewUserMessage(m))
	}
	if err := store.Save(context.Background(), sessionID, messages); err != nil {
		t.Fatalf("save messages: %v", err)
	}
}

func TestCrossProjectConversationSearcher_Empty(t *testing.T) {
	s := NewCrossProjectConversationSearcher()
	results, err := s.Search(context.Background(), "anything", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestCrossProjectConversationSearcher_SingleProject(t *testing.T) {
	store := newTestHistoryStore(t)
	saveTestMessages(t, store, "sess-1", "searching for golang patterns")

	s := NewCrossProjectConversationSearcher()
	s.AddProject("proj-a", store)

	results, err := s.Search(context.Background(), "golang", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Project != "proj-a" {
		t.Errorf("expected project %q, got %q", "proj-a", results[0].Project)
	}
}

func TestCrossProjectConversationSearcher_MultipleProjectsCombinedAndSortedByTime(t *testing.T) {
	storeA := newTestHistoryStore(t)
	saveTestMessages(t, storeA, "sess-a1", "common topic discussed here")

	storeB := newTestHistoryStore(t)
	saveTestMessages(t, storeB, "sess-b1", "common topic in project b")
	saveTestMessages(t, storeB, "sess-b2", "another common topic message")

	s := NewCrossProjectConversationSearcher()
	s.AddProject("proj-a", storeA)
	s.AddProject("proj-b", storeB)

	results, err := s.Search(context.Background(), "common topic", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("expected at least 2 results across projects, got %d", len(results))
	}

	// Verify sorted by time descending.
	for i := 1; i < len(results); i++ {
		if results[i].CreatedAt.After(results[i-1].CreatedAt) {
			t.Errorf("results not sorted by time at index %d: %v after %v",
				i, results[i].CreatedAt, results[i-1].CreatedAt)
		}
	}
}

func TestCrossProjectConversationSearcher_LimitRespected(t *testing.T) {
	storeA := newTestHistoryStore(t)
	for i := 0; i < 5; i++ {
		saveTestMessages(t, storeA, "sess-a-"+string(rune('0'+i)), "keyword present in message")
	}

	storeB := newTestHistoryStore(t)
	for i := 0; i < 5; i++ {
		saveTestMessages(t, storeB, "sess-b-"+string(rune('0'+i)), "keyword present in message")
	}

	s := NewCrossProjectConversationSearcher()
	s.AddProject("proj-a", storeA)
	s.AddProject("proj-b", storeB)

	limit := 3
	results, err := s.Search(context.Background(), "keyword", limit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) > limit {
		t.Errorf("expected at most %d results, got %d", limit, len(results))
	}
}

func TestCrossProjectConversationSearcher_FailedProjectSkipped(t *testing.T) {
	storeA := newTestHistoryStore(t)
	saveTestMessages(t, storeA, "sess-healthy", "healthy project message content")

	// Create a store backed by a closed DB to simulate failure.
	dbB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db b: %v", err)
	}
	if err := InitSchema(dbB); err != nil {
		t.Fatalf("init schema b: %v", err)
	}
	storeB := NewHistoryStore(dbB)
	dbB.Close() // close to force search errors

	s := NewCrossProjectConversationSearcher()
	s.AddProject("proj-a", storeA)
	s.AddProject("proj-b", storeB)

	results, err := s.Search(context.Background(), "healthy", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from healthy project, got none")
	}
	for _, r := range results {
		if r.Project == "proj-b" {
			t.Errorf("broken project proj-b should have been skipped")
		}
	}
}

func TestCrossProjectConversationResult_HasTimeField(t *testing.T) {
	// Ensure CrossProjectConversationResult embeds HistoryResult with CreatedAt.
	r := CrossProjectConversationResult{
		Project: "test",
		HistoryResult: HistoryResult{
			SessionID: "s1",
			MessageID: 1,
			Role:      "user",
			Snippet:   "hello",
			CreatedAt: time.Now(),
		},
	}
	if r.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}
