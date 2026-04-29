package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"

	_ "modernc.org/sqlite"
)

func TestPolicy_ReadOnlyActionAutoAllowed(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "tool_call",
		ToolName:   "read_file",
		Input:      json.RawMessage(`{"path":"README.md"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionAuto {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionAuto)
	}
	if result.RiskLevel != agentic.RiskLevelLow {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelLow)
	}
	if result.PolicyVersion == "" {
		t.Fatal("policy version must be recorded")
	}
}

func TestPolicy_DecisionLiteralsMatchRoadmapContract(t *testing.T) {
	assertLiteral(t, agentic.PolicyDecisionAuto, "auto_allowed")
	assertLiteral(t, agentic.PolicyDecisionApprovalRequired, "approval_required")
	assertLiteral(t, agentic.PolicyDecisionDenied, "hardline_denied")
	assertLiteral(t, agentic.PolicyDecisionObserveOnly, "observe_only")
	assertLiteral(t, agentic.PolicyDecisionEscalated, "escalated")
	assertLiteral(t, agentic.PolicyDecisionRequireApproval, agentic.PolicyDecisionApprovalRequired)
}

func TestPolicy_MutatingToolRequiresApproval(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "tool_call",
		ToolName:   "write_file",
		Input:      json.RawMessage(`{"path":"notes.md","content":"draft"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionApprovalRequired {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionApprovalRequired)
	}
	if result.RiskLevel != agentic.RiskLevelMedium {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelMedium)
	}
}

func TestPolicy_GitReadOnlyActionAutoAllowed(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "tool_call",
		ToolName:   "git",
		Input:      json.RawMessage(`{"subcommand":"status"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionAuto {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionAuto)
	}
	if result.RiskLevel != agentic.RiskLevelLow {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelLow)
	}
}

func TestPolicy_PureObserveOnlyActionRecordedAsObserveOnly(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "observe_only",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionObserveOnly {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionObserveOnly)
	}
	if result.RiskLevel != agentic.RiskLevelLow {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelLow)
	}
}

func TestPolicy_GitMutatingActionRequiresApproval(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "tool_call",
		ToolName:   "git",
		Input:      json.RawMessage(`{"subcommand":"commit","message":"checkpoint"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionApprovalRequired {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionApprovalRequired)
	}
	if result.RiskLevel != agentic.RiskLevelMedium {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelMedium)
	}
}

func TestPolicy_GitHardlineActionDenied(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "tool_call",
		ToolName:   "git",
		Input:      json.RawMessage(`{"subcommand":"push","args":["--force","origin","main"]}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionDenied {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionDenied)
	}
	if result.RiskLevel != agentic.RiskLevelCritical {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelCritical)
	}
}

func TestPolicy_FileSystemHardlineActionDenied(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "tool_call",
		ToolName:   "write_file",
		Input:      json.RawMessage(`{"path":"/etc/hosts","content":"127.0.0.1 example.test"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionDenied {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionDenied)
	}
	if result.RiskLevel != agentic.RiskLevelCritical {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelCritical)
	}
}

func TestPolicy_DangerousActionDenied(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "tool_call",
		ToolName:   "bash",
		Input:      json.RawMessage(`{"command":"sudo rm -rf /"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionDenied {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionDenied)
	}
	if result.RiskLevel != agentic.RiskLevelCritical {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelCritical)
	}
	if result.Reason == "" {
		t.Fatal("denied decision must include reason")
	}
}

func TestPolicy_DangerousObserveOnlyActionDenied(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "observe_only",
		ToolName:   "bash",
		Input:      json.RawMessage(`{"command":"sudo rm -rf /"}`),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.Decision != agentic.PolicyDecisionDenied {
		t.Fatalf("decision = %q, want %q", result.Decision, agentic.PolicyDecisionDenied)
	}
	if result.RiskLevel != agentic.RiskLevelCritical {
		t.Fatalf("risk = %q, want %q", result.RiskLevel, agentic.RiskLevelCritical)
	}
}

func TestPolicy_ApprovalRequiredLiteralIsCanonical(t *testing.T) {
	if agentic.PolicyDecisionRequireApproval != agentic.PolicyDecisionApprovalRequired {
		t.Fatalf("legacy approval literal = %q, want canonical %q", agentic.PolicyDecisionRequireApproval, agentic.PolicyDecisionApprovalRequired)
	}
}

func TestPolicy_DecisionPersisted(t *testing.T) {
	ctx := context.Background()
	_, store := newPolicyTestStore(t)
	task := createPolicyTestTask(t, ctx, store)
	actor := createPolicyTestActor(t, ctx, store, task.ID)
	evaluator := NewEvaluator()

	decision, err := evaluator.EvaluateAndRecord(ctx, store, Request{
		TaskID:     task.ID,
		ActorID:    actor.ID,
		ActionKind: "tool_call",
		ToolName:   "grep",
		Input:      json.RawMessage(`{"pattern":"TODO","path":"."}`),
	})
	if err != nil {
		t.Fatalf("EvaluateAndRecord: %v", err)
	}

	got, err := store.GetPolicyDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetPolicyDecision: %v", err)
	}
	if got.TaskID != task.ID || got.ActorID != actor.ID || got.ActionKind != "tool_call" || got.ToolName != "grep" {
		t.Fatalf("decision linkage mismatch: %+v", got)
	}
	if got.Decision != agentic.PolicyDecisionAuto || got.RiskLevel != agentic.RiskLevelLow || got.PolicyVersion == "" {
		t.Fatalf("unexpected persisted decision: %+v", got)
	}
}

func TestPolicy_DeterministicForSameInput(t *testing.T) {
	evaluator := NewEvaluator()
	req := Request{
		TaskID:     42,
		ActionKind: "tool_call",
		ToolName:   "bash",
		Input:      json.RawMessage(`{"command":"git status --short"}`),
	}

	first, err := evaluator.Evaluate(req)
	if err != nil {
		t.Fatalf("Evaluate first: %v", err)
	}
	second, err := evaluator.Evaluate(req)
	if err != nil {
		t.Fatalf("Evaluate second: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("policy decision not deterministic:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

func TestPolicy_VersionRecorded(t *testing.T) {
	evaluator := NewEvaluator()

	result, err := evaluator.Evaluate(Request{
		TaskID:     42,
		ActionKind: "observe",
		ToolName:   "watcher_bridge",
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if result.PolicyVersion != Version {
		t.Fatalf("policy version = %q, want %q", result.PolicyVersion, Version)
	}
}

func TestPolicy_NoApprovalRequestCreated(t *testing.T) {
	ctx := context.Background()
	db, store := newPolicyTestStore(t)
	task := createPolicyTestTask(t, ctx, store)
	evaluator := NewEvaluator()

	if _, err := evaluator.EvaluateAndRecord(ctx, store, Request{
		TaskID:     task.ID,
		ActionKind: "tool_call",
		ToolName:   "write_file",
		Input:      json.RawMessage(`{"path":"notes.md","content":"draft"}`),
	}); err != nil {
		t.Fatalf("EvaluateAndRecord: %v", err)
	}

	assertTableCount(t, db, "approval_requests", 0)
}

func TestPolicy_NoToolReceiptCreated(t *testing.T) {
	ctx := context.Background()
	db, store := newPolicyTestStore(t)
	task := createPolicyTestTask(t, ctx, store)
	evaluator := NewEvaluator()

	if _, err := evaluator.EvaluateAndRecord(ctx, store, Request{
		TaskID:     task.ID,
		ActionKind: "tool_call",
		ToolName:   "write_file",
		Input:      json.RawMessage(`{"path":"notes.md","content":"draft"}`),
	}); err != nil {
		t.Fatalf("EvaluateAndRecord: %v", err)
	}

	assertTableCount(t, db, "tool_action_receipts", 0)
}

func TestPolicy_NoVerifierMemoryFollowupSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store := newPolicyTestStore(t)
	task := createPolicyTestTask(t, ctx, store)
	evaluator := NewEvaluator()

	if _, err := evaluator.EvaluateAndRecord(ctx, store, Request{
		TaskID:     task.ID,
		ActionKind: "tool_call",
		ToolName:   "read_file",
		Input:      json.RawMessage(`{"path":"README.md"}`),
	}); err != nil {
		t.Fatalf("EvaluateAndRecord: %v", err)
	}

	assertTableCount(t, db, "verification_runs", 0)
	assertTableCount(t, db, "memory_updates", 0)
	assertTableCount(t, db, "followups", 0)
}

func TestPolicy_DoesNotChangeExistingDaemonBehavior(t *testing.T) {
	ctx := context.Background()
	db, store := newPolicyTestStore(t)
	task := createPolicyTestTask(t, ctx, store)
	evaluator := NewEvaluator()

	queue, err := daemon.NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	beforeID, duplicate, err := queue.Enqueue(ctx, `{"prompt":"before"}`, "")
	if err != nil {
		t.Fatalf("Enqueue before policy: %v", err)
	}
	if duplicate {
		t.Fatal("unexpected duplicate before policy")
	}

	if _, err := evaluator.EvaluateAndRecord(ctx, store, Request{
		TaskID:     task.ID,
		ActionKind: "tool_call",
		ToolName:   "grep",
		Input:      json.RawMessage(`{"pattern":"TODO","path":"."}`),
	}); err != nil {
		t.Fatalf("EvaluateAndRecord: %v", err)
	}

	afterID, duplicate, err := queue.Enqueue(ctx, `{"prompt":"after"}`, "")
	if err != nil {
		t.Fatalf("Enqueue after policy: %v", err)
	}
	if duplicate {
		t.Fatal("unexpected duplicate after policy")
	}
	if afterID <= beforeID {
		t.Fatalf("queue IDs did not advance normally: before=%d after=%d", beforeID, afterID)
	}
}

func newPolicyTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if _, err := daemon.NewApprovalStore(db); err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	return db, agentic.NewStore(db)
}

func createPolicyTestTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	goal, err := store.CreateStandingGoal(ctx, agentic.StandingGoal{
		Title:         "Keep control-plane explicit",
		Description:   "Policy decisions are recorded before enforcement exists.",
		Status:        agentic.GoalStatusActive,
		Priority:      5,
		AutonomyLevel: agentic.AutonomyLevelObserve,
		RiskBudget:    "medium",
	})
	if err != nil {
		t.Fatalf("CreateStandingGoal: %v", err)
	}
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		GoalID:             goal.ID,
		Title:              "Review proposed tool action",
		Prompt:             "Record policy only.",
		Status:             agentic.TaskStatusProposed,
		Priority:           5,
		RiskLevel:          agentic.RiskLevelMedium,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func createPolicyTestActor(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64) *agentic.AgentActor {
	t.Helper()
	actor, err := store.CreateAgentActor(ctx, agentic.AgentActor{
		TaskID:            taskID,
		Role:              "policy-evaluator",
		StateJSON:         `{}`,
		InboxJSON:         `[]`,
		OutboxJSON:        `[]`,
		ToolAllowlistJSON: `[]`,
		BudgetJSON:        `{}`,
		Status:            "active",
	})
	if err != nil {
		t.Fatalf("CreateAgentActor: %v", err)
	}
	return actor
}

func assertTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func assertLiteral(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("literal = %q, want %q", got, want)
	}
}
