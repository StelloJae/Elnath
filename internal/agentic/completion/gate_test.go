package completion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"
	_ "modernc.org/sqlite"
)

func TestCompletionGate_ExplicitGateRequiresPassedVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !decision.Passed || decision.VerificationRunID != run.ID || decision.Status != agentic.CompletionGateStatusPassed {
		t.Fatalf("decision = %+v, want passed with verifier %d", decision, run.ID)
	}
	assertCompletionGateCount(t, db, 1)
}

func TestCompletionGate_BlocksWithoutVerification(t *testing.T) {
	ctx := context.Background()
	db, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, time.Now().UTC()), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || decision.Status != agentic.CompletionGateStatusBlocked || !strings.Contains(decision.Reason, "missing verifier") {
		t.Fatalf("decision = %+v, want missing verifier block", decision)
	}
	assertCompletionGateCount(t, db, 1)
}

func TestCompletionGate_ConfigObserveRejectsGateRequest(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)

	err := NewGate(store, ModeObserve).Validate(ctx, completionQueueTask(task.ID, time.Now().UTC()), task.ID)
	if err == nil {
		t.Fatal("Validate error = nil, want config maximum rejection")
	}
	if !strings.Contains(err.Error(), "config maximum") {
		t.Fatalf("Validate error = %q, want config maximum", err.Error())
	}
}

func TestCompletionGate_MissingAgenticTaskIDFailsClosed(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)

	err := NewGate(store, ModeVerification).Validate(ctx, completionQueueTask(0, time.Now().UTC()), 0)
	if !errors.Is(err, ErrMissingTaskID) {
		t.Fatalf("Validate error = %v, want ErrMissingTaskID", err)
	}
}

func TestCompletionGate_BlocksFailedVerification(t *testing.T) {
	assertCompletionGateBlocksVerdict(t, agentic.VerificationVerdictFailed)
}

func TestCompletionGate_BlocksInconclusiveVerification(t *testing.T) {
	assertCompletionGateBlocksVerdict(t, agentic.VerificationVerdictInconclusive)
}

func TestCompletionGate_UsesLatestRelevantVerificationRun(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(-time.Second))

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || !strings.Contains(decision.Reason, "stale verifier") {
		t.Fatalf("decision = %+v, want stale verifier block", decision)
	}

	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))
	decision, err = NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate after fresh verifier: %v", err)
	}
	if !decision.Passed || decision.VerificationRunID != run.ID {
		t.Fatalf("decision = %+v, want latest fresh verifier %d", decision, run.ID)
	}
}

func TestCompletionGate_NonTerminalReceiptBlocksCompletion(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusStarted)

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || !strings.Contains(decision.Reason, "non-terminal receipt") {
		t.Fatalf("decision = %+v, want non-terminal receipt block", decision)
	}
}

func TestCompletionGate_TerminalReceiptsDoNotBypassVerifier(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusFailed)
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusDenied)
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusApprovalRequired)

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, time.Now().UTC()), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || !strings.Contains(decision.Reason, "missing verifier") {
		t.Fatalf("decision = %+v, want verifier requirement to remain blocking", decision)
	}
}

func TestCompletionGate_NoAutonomousSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	if _, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, table := range []string{"approval_requests", "memory_updates", "followups"} {
		if got := completionCountRows(t, db, table); got != 0 {
			t.Fatalf("%s = %d, want 0", table, got)
		}
	}
}

func TestCompletionGate_ReceiptSummaryIncludesOptionalCompletionContext(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))
	createCompletionTestReceipt(t, ctx, store, task.ID, agentic.ReceiptStatusSucceeded)
	observed := false

	gate := NewGate(store, ModeVerification, WithCompletionContextProvider(completionContextProviderFunc(
		func(context.Context, daemon.Task, int64) (CompletionContext, error) {
			return CompletionContext{
				VerificationHint:     true,
				VerificationObserved: &observed,
				VerificationCommand:  "go test ./internal/agentic/completion -count=1",
				CompletionWarning:    "final_response_reports_incomplete",
				EditIntent:           true,
				EditObserved:         &observed,
				ReasoningEffort:      "high",
				ReasoningEffortMode:  "auto",
				RetryDecision:        "retry_smaller_scope",
				RetryReason:          "final_response_reports_incomplete",
			}, nil
		},
	)))

	if _, err := gate.Evaluate(ctx, completionQueueTask(task.ID, started), task.ID); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	gates, err := store.ListCompletionGatesByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListCompletionGatesByTask: %v", err)
	}
	if len(gates) != 1 {
		t.Fatalf("completion gates = %d, want 1", len(gates))
	}

	var summary map[string]any
	if err := json.Unmarshal([]byte(gates[0].ReceiptSummaryJSON), &summary); err != nil {
		t.Fatalf("summary json: %v", err)
	}
	if summary[agentic.ReceiptStatusSucceeded] != float64(1) {
		t.Fatalf("succeeded count = %v, want 1; summary=%v", summary[agentic.ReceiptStatusSucceeded], summary)
	}
	if summary["verification_hint"] != true {
		t.Fatalf("verification_hint = %v, want true; summary=%v", summary["verification_hint"], summary)
	}
	if summary["verification_observed"] != false {
		t.Fatalf("verification_observed = %v, want false; summary=%v", summary["verification_observed"], summary)
	}
	if summary["verification_command"] != "go test ./internal/agentic/completion -count=1" {
		t.Fatalf("verification_command = %v; summary=%v", summary["verification_command"], summary)
	}
	if summary["completion_warning"] != "final_response_reports_incomplete" {
		t.Fatalf("completion_warning = %v; summary=%v", summary["completion_warning"], summary)
	}
	if summary["edit_intent"] != true || summary["edit_observed"] != false {
		t.Fatalf("edit fields missing: summary=%v", summary)
	}
	if summary["reasoning_effort"] != "high" || summary["reasoning_effort_mode"] != "auto" {
		t.Fatalf("reasoning fields missing: summary=%v", summary)
	}
	if summary["retry_decision"] != "retry_smaller_scope" || summary["retry_reason"] != "final_response_reports_incomplete" {
		t.Fatalf("retry fields missing: summary=%v", summary)
	}
}

func TestCompletionGate_ReceiptSummaryOmitsEmptyCompletionContext(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	if _, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	gates, err := store.ListCompletionGatesByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListCompletionGatesByTask: %v", err)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(gates[0].ReceiptSummaryJSON), &summary); err != nil {
		t.Fatalf("summary json: %v", err)
	}
	if _, ok := summary["verification_hint"]; ok {
		t.Fatalf("verification_hint should be omitted for empty context: %v", summary)
	}
	if _, ok := summary[agentic.ReceiptStatusStarted]; !ok {
		t.Fatalf("receipt count keys should remain present: %v", summary)
	}
}

func TestCompletionGate_ContextProviderErrorDoesNotBlockGate(t *testing.T) {
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, agentic.VerificationVerdictPassed, started.Add(time.Second))

	gate := NewGate(store, ModeVerification, WithCompletionContextProvider(completionContextProviderFunc(
		func(context.Context, daemon.Task, int64) (CompletionContext, error) {
			return CompletionContext{}, errors.New("context unavailable")
		},
	)))
	decision, err := gate.Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !decision.Passed || decision.VerificationRunID != run.ID {
		t.Fatalf("decision = %+v, want pass despite optional context failure", decision)
	}
}

func assertCompletionGateBlocksVerdict(t *testing.T, verdict string) {
	t.Helper()
	ctx := context.Background()
	_, store := newCompletionTestStore(t)
	task := createCompletionTestTask(t, ctx, store)
	started := time.Now().Add(-time.Minute).UTC()
	run := createCompletionTestVerificationAt(t, ctx, store, task.ID, verdict, started.Add(time.Second))

	decision, err := NewGate(store, ModeVerification).Evaluate(ctx, completionQueueTask(task.ID, started), task.ID)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if decision.Passed || decision.VerificationRunID != run.ID || !strings.Contains(decision.Reason, verdict) {
		t.Fatalf("decision = %+v, want %s verifier block", decision, verdict)
	}
}

type completionContextProviderFunc func(context.Context, daemon.Task, int64) (CompletionContext, error)

func (f completionContextProviderFunc) CompletionContext(ctx context.Context, task daemon.Task, agenticTaskID int64) (CompletionContext, error) {
	return f(ctx, task, agenticTaskID)
}

func newCompletionTestStore(t *testing.T) (*sql.DB, *agentic.Store) {
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

func createCompletionTestTask(t *testing.T, ctx context.Context, store *agentic.Store) *agentic.AgenticTask {
	t.Helper()
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "completion gated task",
		Prompt:             "verify before completion",
		Status:             agentic.TaskStatusRunning,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func createCompletionTestVerificationAt(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, verdict string, createdAt time.Time) *agentic.VerificationRun {
	t.Helper()
	run, err := store.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           taskID,
		CriteriaJSON:     `["done means verified"]`,
		EvidenceRefsJSON: `["receipt:1"]`,
		Verdict:          verdict,
		Reason:           verdict + " verifier",
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}
	return run
}

func createCompletionTestReceipt(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, status string) {
	t.Helper()
	if _, err := store.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:    taskID,
		ToolName:  "bash",
		InputHash: "input",
		Status:    status,
	}); err != nil {
		t.Fatalf("CreateToolActionReceipt: %v", err)
	}
}

func completionQueueTask(taskID int64, startedAt time.Time) daemon.Task {
	return daemon.Task{
		ID:        taskID + 100,
		Status:    daemon.StatusRunning,
		StartedAt: startedAt,
		Payload: daemon.EncodeTaskPayload(daemon.TaskPayload{
			Prompt:                "verify before completion",
			AgenticCompletionGate: ModeVerification,
		}),
	}
}

func assertCompletionGateCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	if got := completionCountRows(t, db, "completion_gates"); got != want {
		t.Fatalf("completion_gates = %d, want %d", got, want)
	}
}

func completionCountRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
