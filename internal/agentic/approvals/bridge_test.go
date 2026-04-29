package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"

	_ "modernc.org/sqlite"
)

func TestApprovalBridge_CreatesApprovalFromApprovalRequiredPolicyDecision(t *testing.T) {
	ctx := context.Background()
	db, store, approvalStore, bridge := newApprovalBridgeTest(t)
	task := createBridgeTask(t, ctx, store)
	decision := createBridgePolicyDecision(t, ctx, store, task.ID, agentic.PolicyDecisionApprovalRequired)

	req, err := bridge.CreateApproval(ctx, Request{
		TaskID:           task.ID,
		PolicyDecisionID: decision.ID,
		ToolName:         "bash",
		Input:            json.RawMessage(`{"cmd":"make test"}`),
	})
	if err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	got, err := approvalStore.Get(ctx, req.ID)
	if err != nil {
		t.Fatalf("Get approval: %v", err)
	}
	if got.TaskID != task.ID || got.PolicyDecisionID != decision.ID || got.ToolName != "bash" {
		t.Fatalf("approval provenance = %+v", got)
	}
	if got.RiskLevel != agentic.RiskLevelHigh || got.Reason != decision.Reason || got.PolicyVersion != decision.PolicyVersion {
		t.Fatalf("approval policy fields = %+v, decision = %+v", got, decision)
	}
	assertBridgeTableCount(t, db, "approval_requests", 1)
}

func TestApprovalBridge_RejectsNonApprovalRequiredDecision(t *testing.T) {
	ctx := context.Background()
	db, store, _, bridge := newApprovalBridgeTest(t)
	task := createBridgeTask(t, ctx, store)

	for _, decisionValue := range []string{
		agentic.PolicyDecisionAutoAllowed,
		agentic.PolicyDecisionHardlineDenied,
		agentic.PolicyDecisionObserveOnly,
	} {
		decision := createBridgePolicyDecision(t, ctx, store, task.ID, decisionValue)
		if _, err := bridge.CreateApproval(ctx, Request{TaskID: task.ID, PolicyDecisionID: decision.ID, ToolName: "bash"}); err == nil {
			t.Fatalf("CreateApproval with decision %q error = nil, want rejection", decisionValue)
		}
	}

	assertBridgeTableCount(t, db, "approval_requests", 0)
}

func TestApprovalBridge_DoesNotCreateDuplicatePendingApprovalForSamePolicyDecision(t *testing.T) {
	ctx := context.Background()
	db, store, approvalStore, bridge := newApprovalBridgeTest(t)
	task := createBridgeTask(t, ctx, store)
	decision := createBridgePolicyDecision(t, ctx, store, task.ID, agentic.PolicyDecisionApprovalRequired)

	first, err := bridge.CreateApproval(ctx, Request{TaskID: task.ID, PolicyDecisionID: decision.ID, ToolName: "bash"})
	if err != nil {
		t.Fatalf("CreateApproval first: %v", err)
	}
	second, err := bridge.CreateApproval(ctx, Request{TaskID: task.ID, PolicyDecisionID: decision.ID, ToolName: "bash"})
	if err != nil {
		t.Fatalf("CreateApproval second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate approval id = %d, want existing %d", second.ID, first.ID)
	}

	pending, err := approvalStore.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
	assertBridgeTableCount(t, db, "approval_requests", 1)
}

func TestApprovalBridge_DetectsPendingPolicyDecisionUniqueConflict(t *testing.T) {
	err := errors.New("constraint failed: UNIQUE constraint failed: approval_requests.policy_decision_id")
	if !isPendingApprovalUniqueConflict(err) {
		t.Fatalf("isPendingApprovalUniqueConflict(%q) = false, want true", err)
	}
}

func TestApprovalBridge_LinksTaskApprovalRequestID(t *testing.T) {
	ctx := context.Background()
	_, store, _, bridge := newApprovalBridgeTest(t)
	task := createBridgeTask(t, ctx, store)
	decision := createBridgePolicyDecision(t, ctx, store, task.ID, agentic.PolicyDecisionApprovalRequired)

	approval, err := bridge.CreateApproval(ctx, Request{TaskID: task.ID, PolicyDecisionID: decision.ID, ToolName: "bash"})
	if err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	got, err := store.GetAgenticTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if got.ApprovalRequestID != approval.IDString() {
		t.Fatalf("task ApprovalRequestID = %q, want %q", got.ApprovalRequestID, approval.IDString())
	}
}

func TestApprovalBridge_NoAutonomousSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store, _, bridge := newApprovalBridgeTest(t)
	if _, err := daemon.NewQueue(db); err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	task := createBridgeTask(t, ctx, store)
	decision := createBridgePolicyDecision(t, ctx, store, task.ID, agentic.PolicyDecisionApprovalRequired)

	if _, err := bridge.CreateApproval(ctx, Request{TaskID: task.ID, PolicyDecisionID: decision.ID, ToolName: "bash"}); err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}

	assertBridgeTableCount(t, db, "task_queue", 0)
	assertBridgeTableCount(t, db, "tool_action_receipts", 0)
	assertBridgeTableCount(t, db, "verification_runs", 0)
	assertBridgeTableCount(t, db, "memory_updates", 0)
	assertBridgeTableCount(t, db, "followups", 0)
}

func newApprovalBridgeTest(t *testing.T) (*sql.DB, *agentic.Store, *daemon.ApprovalStore, *Bridge) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "elnath.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("agentic InitSchema: %v", err)
	}
	approvalStore, err := daemon.NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	store := agentic.NewStore(db)
	bridge := NewBridge(db, store, approvalStore)
	return db, store, approvalStore, bridge
}

func createBridgeTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Bridge task",
		Prompt:             "Inspect approval provenance",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelMedium,
		AutonomyDecision:   agentic.PolicyDecisionApprovalRequired,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func createBridgePolicyDecision(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, decisionValue string) *agentic.PolicyDecisionRecord {
	t.Helper()
	decision, err := store.CreatePolicyDecision(ctx, agentic.PolicyDecisionRecord{
		TaskID:        taskID,
		ActionKind:    "tool_call",
		ToolName:      "bash",
		RiskLevel:     agentic.RiskLevelHigh,
		Decision:      decisionValue,
		Reason:        "shell command requires approval",
		PolicyVersion: "agentic-policy-v1",
	})
	if err != nil {
		t.Fatalf("CreatePolicyDecision: %v", err)
	}
	return decision
}

func assertBridgeTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
