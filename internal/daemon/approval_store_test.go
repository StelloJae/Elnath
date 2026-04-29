package daemon

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openApprovalTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "elnath.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=30000",
	} {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("exec pragma %q: %v", p, err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApprovalStoreCreateListDecideAndWait(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}

	req, err := store.Create(context.Background(), "bash", []byte(`{"cmd":"git status"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if req.Decision != ApprovalDecisionPending {
		t.Fatalf("Decision = %q, want pending", req.Decision)
	}

	pending, err := store.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != req.ID {
		t.Fatalf("pending = %+v, want request %d", pending, req.ID)
	}

	done := make(chan bool, 1)
	go func() {
		approved, waitErr := store.Wait(context.Background(), req.ID, 5*time.Millisecond)
		if waitErr != nil {
			t.Errorf("Wait: %v", waitErr)
			return
		}
		done <- approved
	}()

	time.Sleep(20 * time.Millisecond)
	if err := store.Decide(context.Background(), req.ID, true); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	select {
	case approved := <-done:
		if !approved {
			t.Fatal("approved = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after approval")
	}

	pending, err = store.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending after decide: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after decide = %+v, want empty", pending)
	}

	row, err := store.Get(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Decision != ApprovalDecisionApproved {
		t.Fatalf("final decision = %q, want approved", row.Decision)
	}
}

func TestApprovalStoreWaitHonorsContextCancellation(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	req, err := store.Create(context.Background(), "bash", []byte(`{"cmd":"rm -rf /tmp/nope"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	approved, err := store.Wait(ctx, req.ID, 5*time.Millisecond)
	if err == nil {
		t.Fatal("Wait error = nil, want context deadline exceeded")
	}
	if approved {
		t.Fatal("approved = true, want false when context cancels")
	}
}

func TestApprovalPrompterUsesContextProvenanceWhenPresent(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	prompter := NewApprovalPrompter(store, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithApprovalContext(ctx, ApprovalCreateRequest{
		TaskID:           11,
		PolicyDecisionID: 22,
		ActorID:          33,
		ActionKind:       "tool_call",
		RiskLevel:        "high",
		Reason:           "shell command requires approval",
		PolicyVersion:    "agentic-policy-v1",
	})

	done := make(chan error, 1)
	go func() {
		_, err := prompter.Prompt(ctx, "bash", []byte(`{"cmd":"make test"}`))
		done <- err
	}()

	var pending []ApprovalRequest
	for i := 0; i < 20; i++ {
		pending, err = store.ListPending(context.Background())
		if err != nil {
			t.Fatalf("ListPending: %v", err)
		}
		if len(pending) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
	assertApprovalProvenance(t, &pending[0], 11, 22, 33)
	if err := store.Decide(context.Background(), pending[0].ID, true); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Prompt: %v", err)
	}
}

func TestApprovalStore_MigratesProvenanceColumns(t *testing.T) {
	db := openApprovalTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE approval_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tool_name TEXT NOT NULL,
			input TEXT NOT NULL DEFAULT '',
			decision TEXT NOT NULL DEFAULT 'pending',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO approval_requests(tool_name, input, decision, created_at, updated_at)
		VALUES ('bash', '{"cmd":"git status"}', 'pending', 10, 10);
	`); err != nil {
		t.Fatalf("create legacy approvals table: %v", err)
	}

	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}

	for _, column := range []string{
		"task_id",
		"policy_decision_id",
		"actor_id",
		"action_kind",
		"risk_level",
		"reason",
		"policy_version",
		"expires_at",
		"decided_by",
	} {
		if !approvalColumnExists(t, db, column) {
			t.Fatalf("approval_requests.%s missing after migration", column)
		}
	}

	req, err := store.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("Get legacy approval: %v", err)
	}
	if req.ToolName != "bash" || !strings.Contains(req.Input, "git status") || req.TaskID != 0 || req.PolicyDecisionID != 0 || req.DecidedBy != "" {
		t.Fatalf("legacy approval after migration = %+v", req)
	}
}

func TestApprovalStore_CreateLegacyRequestStillWorks(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}

	req, err := store.Create(context.Background(), "bash", []byte(`{"cmd":"git status"}`))
	if err != nil {
		t.Fatalf("Create legacy approval: %v", err)
	}

	got, err := store.Get(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TaskID != 0 || got.PolicyDecisionID != 0 || got.ActorID != 0 || got.ActionKind != "" || got.RiskLevel != "" || got.Reason != "" || got.PolicyVersion != "" || got.DecidedBy != "" {
		t.Fatalf("legacy provenance fields = %+v, want empty", got)
	}
}

func TestApprovalStore_CreateWithContextStoresTaskPolicyRiskReason(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}

	req, err := store.CreateWithContext(context.Background(), ApprovalCreateRequest{
		ToolName:         "bash",
		Input:            []byte(`{"cmd":"make test"}`),
		TaskID:           11,
		PolicyDecisionID: 22,
		ActorID:          33,
		ActionKind:       "tool_call",
		RiskLevel:        "high",
		Reason:           "shell command requires approval",
		PolicyVersion:    "agentic-policy-v1",
	})
	if err != nil {
		t.Fatalf("CreateWithContext: %v", err)
	}

	got, err := store.Get(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertApprovalProvenance(t, got, 11, 22, 33)

	pending, err := store.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	assertApprovalProvenance(t, &pending[0], 11, 22, 33)
}

func TestApprovalStore_DecideByRecordsOperator(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}

	req, err := store.Create(context.Background(), "bash", []byte(`{"cmd":"git status"}`))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.DecideBy(context.Background(), req.ID, true, "telegram:user-7"); err != nil {
		t.Fatalf("DecideBy: %v", err)
	}
	got, err := store.Get(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Decision != ApprovalDecisionApproved || got.DecidedBy != "telegram:user-7" {
		t.Fatalf("approval after DecideBy = %+v", got)
	}

	req2, err := store.Create(context.Background(), "bash", []byte(`{"cmd":"git diff"}`))
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if err := store.Decide(context.Background(), req2.ID, false); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	got2, err := store.Get(context.Background(), req2.ID)
	if err != nil {
		t.Fatalf("Get second: %v", err)
	}
	if got2.Decision != ApprovalDecisionDenied || got2.DecidedBy != "" {
		t.Fatalf("legacy Decide fields = %+v", got2)
	}
}

func TestApprovalStore_PreventsDuplicatePendingPolicyDecision(t *testing.T) {
	db := openApprovalTestDB(t)
	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}

	req := ApprovalCreateRequest{
		ToolName:         "bash",
		Input:            []byte(`{"cmd":"make test"}`),
		TaskID:           11,
		PolicyDecisionID: 22,
		ActionKind:       "tool_call",
		RiskLevel:        "high",
		Reason:           "shell command requires approval",
		PolicyVersion:    "agentic-policy-v1",
	}
	if _, err := store.CreateWithContext(context.Background(), req); err != nil {
		t.Fatalf("CreateWithContext first: %v", err)
	}
	if _, err := store.CreateWithContext(context.Background(), req); err == nil {
		t.Fatal("CreateWithContext duplicate error = nil, want unique pending policy decision protection")
	}
}

func TestApprovalStore_MigratesDuplicatePendingPolicyDecisionBeforeUniqueIndex(t *testing.T) {
	db := openApprovalTestDB(t)
	if _, err := db.Exec(`
		CREATE TABLE approval_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tool_name TEXT NOT NULL,
			input TEXT NOT NULL DEFAULT '',
			decision TEXT NOT NULL DEFAULT 'pending',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			policy_decision_id INTEGER,
			decided_by TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO approval_requests(tool_name, input, decision, created_at, updated_at, policy_decision_id)
		VALUES
			('bash', '{"cmd":"one"}', 'pending', 10, 10, 22),
			('bash', '{"cmd":"two"}', 'pending', 20, 20, 22);
	`); err != nil {
		t.Fatalf("create duplicate pending approvals: %v", err)
	}

	store, err := NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}

	pending, err := store.GetPendingByPolicyDecision(context.Background(), 22)
	if err != nil {
		t.Fatalf("GetPendingByPolicyDecision: %v", err)
	}
	if pending.ID != 1 {
		t.Fatalf("kept pending approval = %d, want oldest id 1", pending.ID)
	}

	row, err := store.Get(context.Background(), 2)
	if err != nil {
		t.Fatalf("Get deduped approval: %v", err)
	}
	if row.Decision != ApprovalDecisionDenied || row.DecidedBy != "migration:dedupe" {
		t.Fatalf("deduped approval = %+v, want denied by migration:dedupe", row)
	}
	if _, err := store.CreateWithContext(context.Background(), ApprovalCreateRequest{
		ToolName:         "bash",
		Input:            []byte(`{"cmd":"three"}`),
		PolicyDecisionID: 22,
	}); err == nil {
		t.Fatal("CreateWithContext duplicate after migration error = nil, want unique index active")
	}
}

func approvalColumnExists(t *testing.T, db *sql.DB, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(approval_requests)`)
	if err != nil {
		t.Fatalf("table_info approval_requests: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	return false
}

func assertApprovalProvenance(t *testing.T, req *ApprovalRequest, taskID, policyDecisionID, actorID int64) {
	t.Helper()
	if req.TaskID != taskID || req.PolicyDecisionID != policyDecisionID || req.ActorID != actorID {
		t.Fatalf("approval ids = task:%d policy:%d actor:%d, want task:%d policy:%d actor:%d", req.TaskID, req.PolicyDecisionID, req.ActorID, taskID, policyDecisionID, actorID)
	}
	if req.ActionKind != "tool_call" || req.RiskLevel != "high" || req.Reason == "" || req.PolicyVersion != "agentic-policy-v1" {
		t.Fatalf("approval provenance = %+v", req)
	}
}
