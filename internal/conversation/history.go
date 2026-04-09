package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/llm"
)

// SessionInfo holds metadata for a conversation session.
type SessionInfo struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	// MessageCount is the number of messages in the session.
	MessageCount int
}

// HistoryResult is a single search result from the history store.
type HistoryResult struct {
	SessionID string
	MessageID int64
	Role      string
	Snippet   string
	CreatedAt time.Time
}

// DBHistoryStore implements HistoryStore using SQLite.
// It is a secondary index over conversation transcripts rather than the
// canonical source used for resume.
type DBHistoryStore struct {
	db     *sql.DB
	hasFTS bool
	logger *slog.Logger
}

// NewHistoryStore creates a DBHistoryStore backed by the given database.
// Call InitSchema before using the store.
func NewHistoryStore(db *sql.DB) *DBHistoryStore {
	return &DBHistoryStore{
		db:     db,
		hasFTS: core.HasFTS5(db),
		logger: slog.Default(),
	}
}

// InitSchema creates the conversations and conversation_messages tables,
// and the FTS5 virtual table if available.
func InitSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			id         TEXT PRIMARY KEY,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,
		`CREATE TABLE IF NOT EXISTS conversation_messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES conversations(id),
			role       TEXT NOT NULL,
			content    TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conv_msgs_session ON conversation_messages(session_id)`,
	}

	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("history: init schema: %w", err)
		}
	}

	if core.HasFTS5(db) {
		fts := `CREATE VIRTUAL TABLE IF NOT EXISTS conversation_messages_fts
			USING fts5(content, content='conversation_messages', content_rowid='id')`
		if _, err := db.Exec(fts); err != nil {
			// FTS5 failure is non-fatal; fall back to LIKE search.
			slog.Warn("history: FTS5 table creation failed, falling back to LIKE", "error", err)
		}
	}

	return nil
}

// Save persists the full message array for a session, replacing any prior messages.
// It upserts the conversation row and replaces all messages in a single transaction.
func (s *DBHistoryStore) Save(ctx context.Context, sessionID string, messages []llm.Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Upsert the conversation row.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO conversations(id, created_at, updated_at)
		VALUES (?, strftime('%Y-%m-%dT%H:%M:%fZ','now'), strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(id) DO UPDATE SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	`, sessionID)
	if err != nil {
		return fmt.Errorf("history: upsert conversation: %w", err)
	}

	// Delete existing messages so we can replace with the full array.
	_, err = tx.ExecContext(ctx, `DELETE FROM conversation_messages WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("history: delete old messages: %w", err)
	}

	// Insert all messages.
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO conversation_messages(session_id, role, content)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("history: prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, m := range messages {
		data, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("history: marshal message: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, sessionID, m.Role, string(data)); err != nil {
			return fmt.Errorf("history: insert message: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history: commit: %w", err)
	}

	// Rebuild FTS index for this session if available.
	if s.hasFTS {
		if err := s.rebuildFTS(ctx, sessionID); err != nil {
			s.logger.Warn("history: FTS rebuild failed", "session_id", sessionID, "error", err)
		}
	}

	return nil
}

// Load retrieves all messages for a session in insertion order.
func (s *DBHistoryStore) Load(ctx context.Context, sessionID string) ([]llm.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT content FROM conversation_messages
		WHERE session_id = ?
		ORDER BY id ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("history: load %s: %w", sessionID, err)
	}
	defer rows.Close()

	var messages []llm.Message
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("history: scan message: %w", err)
		}
		var m llm.Message
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("history: unmarshal message: %w", err)
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: rows error: %w", err)
	}
	return messages, nil
}

// Search finds messages matching the query string.
// Uses FTS5 if available, falls back to LIKE otherwise.
func (s *DBHistoryStore) Search(ctx context.Context, query string, limit int) ([]HistoryResult, error) {
	if limit <= 0 {
		limit = 20
	}

	if s.hasFTS {
		return s.searchFTS(ctx, query, limit)
	}
	return s.searchLike(ctx, query, limit)
}

// ListSessions returns metadata for all known sessions, most recently updated first.
func (s *DBHistoryStore) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.created_at, c.updated_at, COUNT(m.id)
		FROM conversations c
		LEFT JOIN conversation_messages m ON m.session_id = c.id
		GROUP BY c.id
		ORDER BY c.updated_at DESC, c.created_at DESC, c.id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("history: list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionInfo
	for rows.Next() {
		var info SessionInfo
		var createdStr, updatedStr string
		if err := rows.Scan(&info.ID, &createdStr, &updatedStr, &info.MessageCount); err != nil {
			return nil, fmt.Errorf("history: scan session: %w", err)
		}
		info.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		info.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)
		sessions = append(sessions, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: rows error: %w", err)
	}
	return sessions, nil
}

// searchFTS queries the FTS5 virtual table.
func (s *DBHistoryStore) searchFTS(ctx context.Context, query string, limit int) ([]HistoryResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.session_id, m.id, m.role, m.content, m.created_at
		FROM conversation_messages_fts fts
		JOIN conversation_messages m ON m.id = fts.rowid
		WHERE fts.content MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		// Fall back to LIKE on FTS error.
		s.logger.Warn("history: FTS search failed, falling back to LIKE", "error", err)
		return s.searchLike(ctx, query, limit)
	}
	defer rows.Close()
	return s.scanHistoryResults(rows)
}

// searchLike performs a simple LIKE search on the content column.
func (s *DBHistoryStore) searchLike(ctx context.Context, query string, limit int) ([]HistoryResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, id, role, content, created_at
		FROM conversation_messages
		WHERE content LIKE ?
		ORDER BY id DESC
		LIMIT ?
	`, "%"+query+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("history: like search: %w", err)
	}
	defer rows.Close()
	return s.scanHistoryResults(rows)
}

func (s *DBHistoryStore) scanHistoryResults(rows *sql.Rows) ([]HistoryResult, error) {
	var results []HistoryResult
	for rows.Next() {
		var r HistoryResult
		var createdStr string
		if err := rows.Scan(&r.SessionID, &r.MessageID, &r.Role, &r.Snippet, &createdStr); err != nil {
			return nil, fmt.Errorf("history: scan result: %w", err)
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		// Truncate snippet for display.
		if len(r.Snippet) > 200 {
			r.Snippet = r.Snippet[:200] + "..."
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: rows error: %w", err)
	}
	return results, nil
}

// rebuildFTS rebuilds the FTS index for messages in a given session.
// Rows are fully consumed and closed before the write transaction begins to
// avoid holding a read cursor open concurrently with a write (which deadlocks
// on single-connection drivers such as modernc.org/sqlite in :memory: mode).
func (s *DBHistoryStore) rebuildFTS(ctx context.Context, sessionID string) error {
	type ftsRow struct {
		id      int64
		content string
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, content FROM conversation_messages WHERE session_id = ?
	`, sessionID)
	if err != nil {
		return fmt.Errorf("history: fts rebuild query: %w", err)
	}

	var entries []ftsRow
	for rows.Next() {
		var r ftsRow
		if err := rows.Scan(&r.id, &r.content); err != nil {
			rows.Close()
			return fmt.Errorf("history: fts rebuild scan: %w", err)
		}
		entries = append(entries, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("history: fts rebuild rows: %w", err)
	}
	rows.Close()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history: fts rebuild begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for _, e := range entries {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO conversation_messages_fts(rowid, content) VALUES (?, ?)`,
			e.id, e.content,
		); err != nil {
			return fmt.Errorf("history: fts rebuild insert: %w", err)
		}
	}

	return tx.Commit()
}
