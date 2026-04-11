package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/llm"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInitSchema(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_ = ctx

	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Verify expected tables exist.
	wantTables := []string{"conversations", "conversation_messages"}
	for _, tbl := range wantTables {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", tbl, err)
		}
	}

	// Idempotent: second call must not error.
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema (second call): %v", err)
	}
}

func TestHistoryStoreSaveAndLoad(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	messages := []llm.Message{
		llm.NewUserMessage("first user message"),
		llm.NewAssistantMessage("first assistant reply"),
		llm.NewUserMessage("second user message"),
	}

	if err := store.Save(ctx, "session-1", messages); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(ctx, "session-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got) != len(messages) {
		t.Fatalf("Load returned %d messages, want %d", len(got), len(messages))
	}
	for i, m := range messages {
		if got[i].Role != m.Role {
			t.Errorf("message[%d] role = %q, want %q", i, got[i].Role, m.Role)
		}
		if got[i].Text() != m.Text() {
			t.Errorf("message[%d] text = %q, want %q", i, got[i].Text(), m.Text())
		}
	}
}

func TestSaveUpsertsMessages(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	messages := []llm.Message{
		llm.NewUserMessage("message 1"),
		llm.NewAssistantMessage("reply 1"),
	}
	if err := store.Save(ctx, "s1", messages); err != nil {
		t.Fatalf("Save(first): %v", err)
	}
	if err := store.Save(ctx, "s1", messages); err != nil {
		t.Fatalf("Save(second): %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_messages WHERE session_id = ?`, "s1").Scan(&count); err != nil {
		t.Fatalf("count conversation_messages: %v", err)
	}
	if count != len(messages) {
		t.Fatalf("row count = %d, want %d", count, len(messages))
	}

	got, err := store.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(messages) {
		t.Fatalf("Load returned %d messages, want %d", len(got), len(messages))
	}
	for i, m := range messages {
		if got[i].Text() != m.Text() {
			t.Errorf("message[%d] text = %q, want %q", i, got[i].Text(), m.Text())
		}
	}
}

func TestSaveAppendsNewMessages(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	first := []llm.Message{
		llm.NewUserMessage("message 1"),
		llm.NewAssistantMessage("reply 1"),
	}
	if err := store.Save(ctx, "s1", first); err != nil {
		t.Fatalf("Save(first): %v", err)
	}

	second := []llm.Message{
		llm.NewUserMessage("message 1"),
		llm.NewAssistantMessage("reply 1"),
		llm.NewUserMessage("message 2"),
	}
	if err := store.Save(ctx, "s1", second); err != nil {
		t.Fatalf("Save(second): %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_messages WHERE session_id = ?`, "s1").Scan(&count); err != nil {
		t.Fatalf("count conversation_messages: %v", err)
	}
	if count != 3 {
		t.Fatalf("row count = %d, want 3", count)
	}

	got, err := store.Load(ctx, "s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(second) {
		t.Fatalf("Load returned %d messages, want %d", len(got), len(second))
	}
	for i, m := range second {
		if got[i].Text() != m.Text() {
			t.Errorf("message[%d] text = %q, want %q", i, got[i].Text(), m.Text())
		}
	}
}

func TestBackfillContentHash(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE conversations (
		id TEXT PRIMARY KEY,
		created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`); err != nil {
		t.Fatalf("create conversations: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE conversation_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL REFERENCES conversations(id),
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`); err != nil {
		t.Fatalf("create legacy conversation_messages: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversations(id) VALUES (?)`, "legacy-session"); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	data, err := json.Marshal(llm.NewUserMessage("hello from legacy"))
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO conversation_messages(session_id, role, content) VALUES (?, ?, ?)`, "legacy-session", "user", string(data)); err != nil {
		t.Fatalf("insert legacy message: %v", err)
	}

	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema migrate: %v", err)
	}

	var hash string
	if err := db.QueryRow(`SELECT content_hash FROM conversation_messages WHERE session_id = ?`, "legacy-session").Scan(&hash); err != nil {
		t.Fatalf("select content_hash: %v", err)
	}
	if hash == "" {
		t.Fatal("content_hash should be backfilled")
	}
}

func TestHistoryStoreLoadNonexistent(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	got, err := store.Load(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("Load of nonexistent session returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load of nonexistent session returned %d messages, want 0", len(got))
	}
}

func TestHistoryStoreSearch(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	if err := store.Save(ctx, "sess-alpha", []llm.Message{
		llm.NewUserMessage("the quick brown fox"),
		llm.NewAssistantMessage("jumps over the lazy dog"),
	}); err != nil {
		t.Fatalf("Save sess-alpha: %v", err)
	}

	if err := store.Save(ctx, "sess-beta", []llm.Message{
		llm.NewUserMessage("completely unrelated content"),
		llm.NewAssistantMessage("nothing matching here"),
	}); err != nil {
		t.Fatalf("Save sess-beta: %v", err)
	}

	results, err := store.Search(ctx, "quick brown", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search returned no results, want at least 1")
	}

	found := false
	for _, r := range results {
		if r.SessionID == "sess-alpha" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected sess-alpha in search results, got %+v", results)
	}
}

func TestHistoryStoreSearchEmptyResult(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	if err := store.Save(ctx, "sess-1", []llm.Message{
		llm.NewUserMessage("hello world"),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	results, err := store.Search(ctx, "xyzzynonexistentterm", 10)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search returned %d results, want 0", len(results))
	}
}

func TestHistoryStoreListSessions(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	sessionIDs := []string{"sess-a", "sess-b", "sess-c"}
	msgCounts := []int{1, 2, 3}

	for i, id := range sessionIDs {
		msgs := make([]llm.Message, msgCounts[i])
		for j := range msgs {
			if j%2 == 0 {
				msgs[j] = llm.NewUserMessage(strings.Repeat("x", j+1))
			} else {
				msgs[j] = llm.NewAssistantMessage(strings.Repeat("y", j+1))
			}
		}
		// Small sleep not viable in unit tests; order is determined by DB insertion time.
		// The updated_at uses SQLite's strftime so same-second saves may tie.
		if err := store.Save(ctx, id, msgs); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if len(sessions) != len(sessionIDs) {
		t.Fatalf("ListSessions returned %d sessions, want %d", len(sessions), len(sessionIDs))
	}

	// Build a lookup by ID so order doesn't matter for count checks.
	byID := make(map[string]SessionInfo, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}

	for i, id := range sessionIDs {
		s, ok := byID[id]
		if !ok {
			t.Errorf("session %q not found in ListSessions", id)
			continue
		}
		if s.MessageCount != msgCounts[i] {
			t.Errorf("session %q: MessageCount = %d, want %d", id, s.MessageCount, msgCounts[i])
		}
		if s.UpdatedAt.IsZero() {
			t.Errorf("session %q: UpdatedAt is zero", id)
		}
	}

	// Verify ordering: most recently updated first.
	for i := 1; i < len(sessions); i++ {
		if sessions[i].UpdatedAt.After(sessions[i-1].UpdatedAt) {
			t.Errorf("sessions not sorted by UpdatedAt DESC: sessions[%d].UpdatedAt (%v) > sessions[%d].UpdatedAt (%v)",
				i, sessions[i].UpdatedAt, i-1, sessions[i-1].UpdatedAt)
		}
	}
}

func TestHistoryStoreListSessionsEmpty(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions on empty DB: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("ListSessions on empty DB returned %d sessions, want 0", len(sessions))
	}
}

func TestHistoryStoreSaveEmptyMessages(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	if err := store.Save(ctx, "empty-session", []llm.Message{}); err != nil {
		t.Fatalf("Save empty messages: %v", err)
	}

	got, err := store.Load(ctx, "empty-session")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load after empty Save returned %d messages, want 0", len(got))
	}
}

func TestHistoryStoreSnippetTruncation(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	// Build a message whose JSON representation exceeds 200 chars.
	// Use a unique word so the search term is an exact FTS5 token.
	// The JSON content field will be long due to the repeated filler.
	longText := "uniqueword " + strings.Repeat("filler ", 50)
	if err := store.Save(ctx, "long-session", []llm.Message{
		llm.NewUserMessage(longText),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	results, err := store.Search(ctx, "uniqueword", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}

	// Snippet is raw JSON of the message — truncated at 200 chars + "...".
	for _, r := range results {
		if len(r.Snippet) > 203 { // 200 + len("...")
			t.Errorf("snippet length %d exceeds 203 chars", len(r.Snippet))
		}
		if len(r.Snippet) == 203 && !strings.HasSuffix(r.Snippet, "...") {
			t.Errorf("truncated snippet does not end with '...': %q", r.Snippet[195:])
		}
	}
}

func TestHistoryStoreSessionInfo_Times(t *testing.T) {
	db := openTestDB(t)
	if err := InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	store := NewHistoryStore(db)
	ctx := context.Background()

	before := time.Now().UTC().Add(-time.Second)

	if err := store.Save(ctx, "time-session", []llm.Message{
		llm.NewUserMessage("checking timestamps"),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	after := time.Now().UTC().Add(time.Second)

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	if s.CreatedAt.Before(before) || s.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not in expected range [%v, %v]", s.CreatedAt, before, after)
	}
	if s.UpdatedAt.Before(before) || s.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt %v not in expected range [%v, %v]", s.UpdatedAt, before, after)
	}
}
