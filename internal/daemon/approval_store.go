package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const createApprovalTable = `
CREATE TABLE IF NOT EXISTS approval_requests (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	tool_name  TEXT NOT NULL,
	input      TEXT NOT NULL DEFAULT '',
	decision   TEXT NOT NULL DEFAULT 'pending',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS approval_requests_decision ON approval_requests(decision);
`

type ApprovalDecision string

const (
	ApprovalDecisionPending  ApprovalDecision = "pending"
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionDenied   ApprovalDecision = "denied"
)

type ApprovalRequest struct {
	ID        int64
	ToolName  string
	Input     string
	Decision  ApprovalDecision
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ApprovalStore struct {
	db *sql.DB
}

func NewApprovalStore(db *sql.DB) (*ApprovalStore, error) {
	if _, err := db.Exec(createApprovalTable); err != nil {
		return nil, fmt.Errorf("approval store: init schema: %w", err)
	}
	return &ApprovalStore{db: db}, nil
}

func (s *ApprovalStore) Create(ctx context.Context, toolName string, input json.RawMessage) (*ApprovalRequest, error) {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO approval_requests (tool_name, input, decision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		toolName, string(input), string(ApprovalDecisionPending), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("approval store: create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("approval store: create last id: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *ApprovalStore) Get(ctx context.Context, id int64) (*ApprovalRequest, error) {
	var req ApprovalRequest
	var decision string
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tool_name, input, decision, created_at, updated_at
		FROM approval_requests
		WHERE id = ?`,
		id,
	).Scan(&req.ID, &req.ToolName, &req.Input, &decision, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("approval store: get %d: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("approval store: get %d: %w", id, err)
	}
	req.Decision = ApprovalDecision(decision)
	req.CreatedAt = time.UnixMilli(createdAt)
	req.UpdatedAt = time.UnixMilli(updatedAt)
	return &req, nil
}

func (s *ApprovalStore) ListPending(ctx context.Context) ([]ApprovalRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tool_name, input, decision, created_at, updated_at
		FROM approval_requests
		WHERE decision = ?
		ORDER BY created_at ASC`,
		string(ApprovalDecisionPending),
	)
	if err != nil {
		return nil, fmt.Errorf("approval store: list pending: %w", err)
	}
	defer rows.Close()

	var out []ApprovalRequest
	for rows.Next() {
		var req ApprovalRequest
		var decision string
		var createdAt, updatedAt int64
		if err := rows.Scan(&req.ID, &req.ToolName, &req.Input, &decision, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("approval store: list pending scan: %w", err)
		}
		req.Decision = ApprovalDecision(decision)
		req.CreatedAt = time.UnixMilli(createdAt)
		req.UpdatedAt = time.UnixMilli(updatedAt)
		out = append(out, req)
	}
	return out, rows.Err()
}

func (s *ApprovalStore) Decide(ctx context.Context, id int64, approved bool) error {
	decision := ApprovalDecisionDenied
	if approved {
		decision = ApprovalDecisionApproved
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE approval_requests
		SET decision = ?, updated_at = ?
		WHERE id = ? AND decision = ?`,
		string(decision), time.Now().UnixMilli(), id, string(ApprovalDecisionPending),
	)
	if err != nil {
		return fmt.Errorf("approval store: decide: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("approval store: decide %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func (s *ApprovalStore) Wait(ctx context.Context, id int64, pollInterval time.Duration) (bool, error) {
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		req, err := s.Get(ctx, id)
		if err != nil {
			return false, err
		}
		switch req.Decision {
		case ApprovalDecisionApproved:
			return true, nil
		case ApprovalDecisionDenied:
			return false, nil
		}

		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
		}
	}
}

type ApprovalPrompter struct {
	store        *ApprovalStore
	pollInterval time.Duration
}

func NewApprovalPrompter(store *ApprovalStore, pollInterval time.Duration) *ApprovalPrompter {
	return &ApprovalPrompter{store: store, pollInterval: pollInterval}
}

func (p *ApprovalPrompter) Prompt(ctx context.Context, toolName string, input json.RawMessage) (bool, error) {
	req, err := p.store.Create(ctx, toolName, input)
	if err != nil {
		return false, err
	}
	return p.store.Wait(ctx, req.ID, p.pollInterval)
}
