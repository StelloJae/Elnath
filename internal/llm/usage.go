package llm

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const createUsageTable = `
CREATE TABLE IF NOT EXISTS llm_usage (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	ts            INTEGER NOT NULL,
	provider      TEXT    NOT NULL,
	model         TEXT    NOT NULL,
	session_id    TEXT    NOT NULL DEFAULT '',
	input_tokens  INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read    INTEGER NOT NULL DEFAULT 0,
	cache_write   INTEGER NOT NULL DEFAULT 0,
	cost_usd      REAL    NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS llm_usage_ts ON llm_usage(ts);
CREATE INDEX IF NOT EXISTS llm_usage_session ON llm_usage(session_id);
`

// UsageRecord captures a single LLM call's token and cost data.
type UsageRecord struct {
	ID           int64
	Timestamp    time.Time
	Provider     string
	Model        string
	SessionID    string
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
	CostUSD      float64
}

// UsageTracker persists token usage records to SQLite.
type UsageTracker struct {
	db *sql.DB
}

// NewUsageTracker initialises the usage table and returns a tracker.
func NewUsageTracker(db *sql.DB) (*UsageTracker, error) {
	if _, err := db.Exec(createUsageTable); err != nil {
		return nil, fmt.Errorf("usage tracker: init schema: %w", err)
	}
	return &UsageTracker{db: db}, nil
}

// Record inserts a usage record. stats must not be nil.
func (t *UsageTracker) Record(ctx context.Context, provider, model, sessionID string, stats UsageStats) error {
	cost := estimateCost(provider, model, stats)

	_, err := t.db.ExecContext(ctx, `
		INSERT INTO llm_usage
			(ts, provider, model, session_id, input_tokens, output_tokens, cache_read, cache_write, cost_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UnixMilli(),
		provider, model, sessionID,
		stats.InputTokens, stats.OutputTokens,
		stats.CacheRead, stats.CacheWrite,
		cost,
	)
	if err != nil {
		return fmt.Errorf("usage tracker: record: %w", err)
	}
	return nil
}

// TotalCost returns the sum of cost_usd across all records, optionally
// filtered by sessionID (empty string = all sessions).
func (t *UsageTracker) TotalCost(ctx context.Context, sessionID string) (float64, error) {
	var query string
	var args []any

	if sessionID == "" {
		query = "SELECT COALESCE(SUM(cost_usd), 0) FROM llm_usage"
	} else {
		query = "SELECT COALESCE(SUM(cost_usd), 0) FROM llm_usage WHERE session_id = ?"
		args = append(args, sessionID)
	}

	var total float64
	if err := t.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("usage tracker: total cost: %w", err)
	}
	return total, nil
}

// RecentRecords returns the most recent n records, newest first.
func (t *UsageTracker) RecentRecords(ctx context.Context, n int) ([]UsageRecord, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT id, ts, provider, model, session_id,
		       input_tokens, output_tokens, cache_read, cache_write, cost_usd
		FROM llm_usage
		ORDER BY ts DESC
		LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("usage tracker: recent: %w", err)
	}
	defer rows.Close()

	var records []UsageRecord
	for rows.Next() {
		var r UsageRecord
		var tsMs int64
		if err := rows.Scan(&r.ID, &tsMs, &r.Provider, &r.Model, &r.SessionID,
			&r.InputTokens, &r.OutputTokens, &r.CacheRead, &r.CacheWrite, &r.CostUSD); err != nil {
			return nil, fmt.Errorf("usage tracker: scan: %w", err)
		}
		r.Timestamp = time.UnixMilli(tsMs)
		records = append(records, r)
	}
	return records, rows.Err()
}

// estimateCost returns a rough USD cost estimate for a completed request.
// Prices are approximate and should be updated when Anthropic/OpenAI change rates.
func estimateCost(provider, model string, stats UsageStats) float64 {
	// Prices per million tokens (input / output).
	type pricing struct{ in, out float64 }

	prices := map[string]pricing{
		// Anthropic Claude 4 / Sonnet 4
		"claude-sonnet-4-20250514": {3.0, 15.0},
		"claude-opus-4-20250514":   {15.0, 75.0},
		"claude-haiku-4-20251001":  {0.8, 4.0},
		// OpenAI GPT-4o
		"gpt-4o":      {2.5, 10.0},
		"gpt-4o-mini": {0.15, 0.60},
	}

	p, ok := prices[model]
	if !ok {
		// Unknown model: use a conservative estimate.
		p = pricing{in: 5.0, out: 20.0}
	}

	inputCost := float64(stats.InputTokens) / 1_000_000 * p.in
	outputCost := float64(stats.OutputTokens) / 1_000_000 * p.out
	// Cache reads are cheaper; cache writes are priced as input.
	cacheReadCost := float64(stats.CacheRead) / 1_000_000 * p.in * 0.1
	cacheWriteCost := float64(stats.CacheWrite) / 1_000_000 * p.in

	return inputCost + outputCost + cacheReadCost + cacheWriteCost
}
