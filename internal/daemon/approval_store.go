package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const createApprovalTable = `
CREATE TABLE IF NOT EXISTS approval_requests (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	tool_name  TEXT NOT NULL,
	input      TEXT NOT NULL DEFAULT '',
	decision   TEXT NOT NULL DEFAULT 'pending',
	consumed_at INTEGER NOT NULL DEFAULT 0,
	consumed_by_receipt_id INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS approval_requests_decision ON approval_requests(decision);
`

const approvalRequestColumns = `
	id, tool_name, input, decision, task_id, policy_decision_id, actor_id, action_kind,
	risk_level, reason, policy_version, expires_at, decided_by, consumed_at, consumed_by_receipt_id, created_at, updated_at
`

type ApprovalDecision string

const (
	ApprovalDecisionPending  ApprovalDecision = "pending"
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionDenied   ApprovalDecision = "denied"
)

type ApprovalRequest struct {
	ID                  int64
	ToolName            string
	Input               string
	Decision            ApprovalDecision
	TaskID              int64
	PolicyDecisionID    int64
	ActorID             int64
	ActionKind          string
	RiskLevel           string
	Reason              string
	PolicyVersion       string
	ExpiresAt           sql.NullTime
	DecidedBy           string
	ConsumedAt          sql.NullTime
	ConsumedByReceiptID int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (r ApprovalRequest) IDString() string {
	return strconv.FormatInt(r.ID, 10)
}

type ApprovalCreateRequest struct {
	ToolName         string
	Input            json.RawMessage
	TaskID           int64
	PolicyDecisionID int64
	ActorID          int64
	ActionKind       string
	RiskLevel        string
	Reason           string
	PolicyVersion    string
	ExpiresAt        sql.NullTime
}

type approvalContextKey struct{}

func WithApprovalContext(ctx context.Context, req ApprovalCreateRequest) context.Context {
	return context.WithValue(ctx, approvalContextKey{}, req)
}

func ApprovalContextFromContext(ctx context.Context) (ApprovalCreateRequest, bool) {
	req, ok := ctx.Value(approvalContextKey{}).(ApprovalCreateRequest)
	return req, ok
}

type ApprovalStore struct {
	db *sql.DB
}

func NewApprovalStore(db *sql.DB) (*ApprovalStore, error) {
	if _, err := db.Exec(createApprovalTable); err != nil {
		return nil, fmt.Errorf("approval store: init schema: %w", err)
	}
	if err := ensureApprovalProvenanceSchema(db); err != nil {
		return nil, err
	}
	return &ApprovalStore{db: db}, nil
}

func (s *ApprovalStore) Create(ctx context.Context, toolName string, input json.RawMessage) (*ApprovalRequest, error) {
	return s.CreateWithContext(ctx, ApprovalCreateRequest{ToolName: toolName, Input: input})
}

func (s *ApprovalStore) CreateWithContext(ctx context.Context, req ApprovalCreateRequest) (*ApprovalRequest, error) {
	return s.createWithContext(ctx, s.db, req)
}

func (s *ApprovalStore) CreateWithContextTx(ctx context.Context, tx *sql.Tx, req ApprovalCreateRequest) (*ApprovalRequest, error) {
	return s.createWithContext(ctx, tx, req)
}

func (s *ApprovalStore) createWithContext(ctx context.Context, runner approvalRunner, req ApprovalCreateRequest) (*ApprovalRequest, error) {
	now := time.Now().UnixMilli()
	res, err := runner.ExecContext(ctx, `
		INSERT INTO approval_requests (
			tool_name, input, decision, task_id, policy_decision_id, actor_id, action_kind,
			risk_level, reason, policy_version, expires_at, decided_by, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ToolName,
		string(req.Input),
		string(ApprovalDecisionPending),
		nullableApprovalInt(req.TaskID),
		nullableApprovalInt(req.PolicyDecisionID),
		nullableApprovalInt(req.ActorID),
		req.ActionKind,
		req.RiskLevel,
		req.Reason,
		req.PolicyVersion,
		nullableApprovalTime(req.ExpiresAt),
		"",
		now,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("approval store: create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("approval store: create last id: %w", err)
	}
	return s.get(ctx, runner, id)
}

func (s *ApprovalStore) Get(ctx context.Context, id int64) (*ApprovalRequest, error) {
	return s.get(ctx, s.db, id)
}

func (s *ApprovalStore) GetTx(ctx context.Context, tx *sql.Tx, id int64) (*ApprovalRequest, error) {
	return s.get(ctx, tx, id)
}

func (s *ApprovalStore) get(ctx context.Context, runner approvalRunner, id int64) (*ApprovalRequest, error) {
	req, err := scanApprovalRequest(runner.QueryRowContext(ctx, `
		SELECT `+approvalRequestColumns+`
		FROM approval_requests
		WHERE id = ?`,
		id,
	))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("approval store: get %d: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("approval store: get %d: %w", id, err)
	}
	return req, nil
}

func (s *ApprovalStore) ListPending(ctx context.Context) ([]ApprovalRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+approvalRequestColumns+`
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
		req, err := scanApprovalRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("approval store: list pending scan: %w", err)
		}
		out = append(out, *req)
	}
	return out, rows.Err()
}

func (s *ApprovalStore) GetPendingByPolicyDecision(ctx context.Context, policyDecisionID int64) (*ApprovalRequest, error) {
	return s.getPendingByPolicyDecision(ctx, s.db, policyDecisionID)
}

func (s *ApprovalStore) GetPendingByPolicyDecisionTx(ctx context.Context, tx *sql.Tx, policyDecisionID int64) (*ApprovalRequest, error) {
	return s.getPendingByPolicyDecision(ctx, tx, policyDecisionID)
}

func (s *ApprovalStore) getPendingByPolicyDecision(ctx context.Context, runner approvalRunner, policyDecisionID int64) (*ApprovalRequest, error) {
	if policyDecisionID == 0 {
		return nil, sql.ErrNoRows
	}
	row := runner.QueryRowContext(ctx, `
		SELECT `+approvalRequestColumns+`
		FROM approval_requests
		WHERE policy_decision_id = ? AND decision = ?
		ORDER BY id
		LIMIT 1`,
		policyDecisionID,
		string(ApprovalDecisionPending),
	)
	req, err := scanApprovalRequest(row)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func (s *ApprovalStore) Decide(ctx context.Context, id int64, approved bool) error {
	return s.DecideBy(ctx, id, approved, "")
}

func (s *ApprovalStore) DecideBy(ctx context.Context, id int64, approved bool, decidedBy string) error {
	decision := ApprovalDecisionDenied
	if approved {
		decision = ApprovalDecisionApproved
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE approval_requests
		SET decision = ?, decided_by = ?, updated_at = ?
		WHERE id = ? AND decision = ?`,
		string(decision), decidedBy, time.Now().UnixMilli(), id, string(ApprovalDecisionPending),
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

type approvalRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type approvalScanner interface {
	Scan(dest ...any) error
}

func ensureApprovalProvenanceSchema(db *sql.DB) error {
	columns := []struct {
		name string
		def  string
	}{
		{"task_id", "INTEGER"},
		{"policy_decision_id", "INTEGER"},
		{"actor_id", "INTEGER"},
		{"action_kind", "TEXT NOT NULL DEFAULT ''"},
		{"risk_level", "TEXT NOT NULL DEFAULT ''"},
		{"reason", "TEXT NOT NULL DEFAULT ''"},
		{"policy_version", "TEXT NOT NULL DEFAULT ''"},
		{"expires_at", "INTEGER"},
		{"decided_by", "TEXT NOT NULL DEFAULT ''"},
		{"consumed_at", "INTEGER NOT NULL DEFAULT 0"},
		{"consumed_by_receipt_id", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, column := range columns {
		exists, err := approvalSchemaHasColumn(db, column.name)
		if err != nil {
			return fmt.Errorf("approval store: inspect column %s: %w", column.name, err)
		}
		if exists {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE approval_requests ADD COLUMN %s %s", column.name, column.def)); err != nil {
			return fmt.Errorf("approval store: add column %s: %w", column.name, err)
		}
	}
	if err := dedupePendingPolicyApprovals(db); err != nil {
		return err
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS approval_requests_task_id ON approval_requests(task_id)`,
		`CREATE INDEX IF NOT EXISTS approval_requests_policy_decision_id ON approval_requests(policy_decision_id)`,
		`CREATE INDEX IF NOT EXISTS approval_requests_decision_consumed ON approval_requests(decision, consumed_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS approval_requests_policy_decision_pending ON approval_requests(policy_decision_id) WHERE policy_decision_id IS NOT NULL AND decision = 'pending'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("approval store: create provenance index: %w", err)
		}
	}
	return nil
}

func dedupePendingPolicyApprovals(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT policy_decision_id, MIN(id) AS keep_id
		FROM approval_requests
		WHERE policy_decision_id IS NOT NULL AND decision = ?
		GROUP BY policy_decision_id
		HAVING COUNT(*) > 1
	`, string(ApprovalDecisionPending))
	if err != nil {
		return fmt.Errorf("approval store: find duplicate pending policy approvals: %w", err)
	}
	defer rows.Close()

	type duplicate struct {
		policyDecisionID int64
		keepID           int64
	}
	var duplicates []duplicate
	for rows.Next() {
		var dup duplicate
		if err := rows.Scan(&dup.policyDecisionID, &dup.keepID); err != nil {
			return fmt.Errorf("approval store: scan duplicate pending policy approval: %w", err)
		}
		duplicates = append(duplicates, dup)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("approval store: inspect duplicate pending policy approvals: %w", err)
	}

	now := time.Now().UnixMilli()
	for _, dup := range duplicates {
		if _, err := db.Exec(`
			UPDATE approval_requests
			SET decision = ?, decided_by = ?, updated_at = ?
			WHERE policy_decision_id = ? AND decision = ? AND id <> ?
		`, string(ApprovalDecisionDenied), "migration:dedupe", now, dup.policyDecisionID, string(ApprovalDecisionPending), dup.keepID); err != nil {
			return fmt.Errorf("approval store: dedupe pending policy approval %d: %w", dup.policyDecisionID, err)
		}
	}
	return nil
}

func approvalSchemaHasColumn(db *sql.DB, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(approval_requests)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func scanApprovalRequest(scanner approvalScanner) (*ApprovalRequest, error) {
	var req ApprovalRequest
	var decision string
	var taskID, policyDecisionID, actorID, expiresAt, consumedAt, consumedByReceiptID sql.NullInt64
	var createdAt, updatedAt int64
	if err := scanner.Scan(
		&req.ID,
		&req.ToolName,
		&req.Input,
		&decision,
		&taskID,
		&policyDecisionID,
		&actorID,
		&req.ActionKind,
		&req.RiskLevel,
		&req.Reason,
		&req.PolicyVersion,
		&expiresAt,
		&req.DecidedBy,
		&consumedAt,
		&consumedByReceiptID,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	req.Decision = ApprovalDecision(decision)
	req.TaskID = approvalIntFromNull(taskID)
	req.PolicyDecisionID = approvalIntFromNull(policyDecisionID)
	req.ActorID = approvalIntFromNull(actorID)
	req.ExpiresAt = approvalTimeFromNull(expiresAt)
	req.ConsumedAt = approvalTimeFromNull(consumedAt)
	req.ConsumedByReceiptID = approvalIntFromNull(consumedByReceiptID)
	req.CreatedAt = time.UnixMilli(createdAt)
	req.UpdatedAt = time.UnixMilli(updatedAt)
	return &req, nil
}

func nullableApprovalInt(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func approvalIntFromNull(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func nullableApprovalTime(value sql.NullTime) any {
	if !value.Valid || value.Time.IsZero() {
		return nil
	}
	return value.Time.UnixMilli()
}

func approvalTimeFromNull(value sql.NullInt64) sql.NullTime {
	if !value.Valid || value.Int64 == 0 {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: time.UnixMilli(value.Int64), Valid: true}
}

type ApprovalPrompter struct {
	store        *ApprovalStore
	pollInterval time.Duration
}

func NewApprovalPrompter(store *ApprovalStore, pollInterval time.Duration) *ApprovalPrompter {
	return &ApprovalPrompter{store: store, pollInterval: pollInterval}
}

func (p *ApprovalPrompter) Prompt(ctx context.Context, toolName string, input json.RawMessage) (bool, error) {
	createReq, ok := ApprovalContextFromContext(ctx)
	if !ok {
		createReq = ApprovalCreateRequest{}
	}
	createReq.ToolName = toolName
	createReq.Input = input
	req, err := p.store.CreateWithContext(ctx, createReq)
	if err != nil {
		return false, err
	}
	return p.store.Wait(ctx, req.ID, p.pollInterval)
}
