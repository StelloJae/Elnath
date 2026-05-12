package agentictools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
)

func TestTaskEvidenceToolSummarizesReceiptsVerificationAndMemory(t *testing.T) {
	ctx := context.Background()
	_, store, _, task := newGatewayTestStore(t)
	actor := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRoleExecutor)
	decision, err := store.CreatePolicyDecision(ctx, agentic.PolicyDecisionRecord{
		TaskID:        task.ID,
		ActorID:       actor.ID,
		ActionKind:    "tool_call",
		ToolName:      "bash",
		RiskLevel:     agentic.RiskLevelLow,
		Decision:      agentic.PolicyDecisionAuto,
		Reason:        "test decision",
		PolicyVersion: "test-policy",
	})
	if err != nil {
		t.Fatalf("CreatePolicyDecision: %v", err)
	}
	if _, err := store.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:           task.ID,
		ActorID:          actor.ID,
		PolicyDecisionID: decision.ID,
		ToolName:         "bash",
		ToolCallID:       "call-1",
		InputHash:        "input",
		OutputHash:       "output",
		OutputSummary:    strings.Repeat("verified ", 40),
		Status:           agentic.ReceiptStatusSucceeded,
	}); err != nil {
		t.Fatalf("CreateToolActionReceipt: %v", err)
	}
	run, err := store.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           task.ID,
		VerifierActorID:  actor.ID,
		CriteriaJSON:     `["go test"]`,
		EvidenceRefsJSON: `["go test ./internal/... ./cmd/elnath -count=1"]`,
		Verdict:          agentic.VerificationVerdictPassed,
		Reason:           "focused evidence passed",
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}
	if _, err := store.CreateCompletionGate(ctx, agentic.CompletionGate{
		TaskID:             task.ID,
		VerificationRunID:  run.ID,
		Status:             agentic.CompletionGateStatusPassed,
		Reason:             "latest verification passed",
		ReceiptSummaryJSON: `{"receipts":1}`,
	}); err != nil {
		t.Fatalf("CreateCompletionGate: %v", err)
	}
	if _, err := store.CreateMemoryUpdate(ctx, agentic.MemoryUpdate{
		TaskID:            task.ID,
		ReceiptID:         1,
		VerificationRunID: run.ID,
		Target:            "wiki",
		Operation:         "append",
		PayloadHash:       "hash",
		Status:            agentic.MemoryUpdateStatusApplied,
		Source:            "agentic",
		Reason:            "verified learning",
	}); err != nil {
		t.Fatalf("CreateMemoryUpdate: %v", err)
	}

	result, err := NewTaskEvidenceTool(store).Execute(ctx, json.RawMessage(`{"task_id":`+jsonNumber(task.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}

	var out taskEvidenceToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.TaskID != task.ID || out.Totals.Receipts != 1 || out.Totals.VerificationRuns != 1 || out.Totals.CompletionGates != 1 || out.Totals.MemoryUpdates != 1 {
		t.Fatalf("output totals = %+v, want one of each", out)
	}
	if out.LatestReceipt == nil || out.LatestReceipt.Status != agentic.ReceiptStatusSucceeded || out.LatestReceipt.OutputSummary == "" {
		t.Fatalf("latest receipt = %+v, want succeeded summary", out.LatestReceipt)
	}
	if len(out.LatestReceipt.OutputSummary) > taskEvidenceTextLimit {
		t.Fatalf("receipt summary length = %d, want <= %d", len(out.LatestReceipt.OutputSummary), taskEvidenceTextLimit)
	}
	if out.LatestVerification == nil || out.LatestVerification.Verdict != agentic.VerificationVerdictPassed {
		t.Fatalf("latest verification = %+v, want passed", out.LatestVerification)
	}
	if out.LatestCompletionGate == nil || out.LatestCompletionGate.Status != agentic.CompletionGateStatusPassed {
		t.Fatalf("latest completion gate = %+v, want passed", out.LatestCompletionGate)
	}
	if out.LatestMemoryUpdate == nil || out.LatestMemoryUpdate.Status != agentic.MemoryUpdateStatusApplied || out.LatestMemoryUpdate.Target != "wiki" {
		t.Fatalf("latest memory update = %+v, want applied wiki update", out.LatestMemoryUpdate)
	}
}

func TestTaskEvidenceToolRequiresTaskID(t *testing.T) {
	_, store, _, _ := newGatewayTestStore(t)

	result, err := NewTaskEvidenceTool(store).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "task_id is required") {
		t.Fatalf("result = %+v, want task_id error", result)
	}
}

func TestTaskEvidenceToolMetadataIsReadOnlyAndDeferred(t *testing.T) {
	tool := NewTaskEvidenceTool(nil)

	if tool.Name() != TaskEvidenceToolName {
		t.Fatalf("Name() = %q, want %q", tool.Name(), TaskEvidenceToolName)
	}
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatalf("metadata = concurrency:%t reversible:%t, want read-only", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
	if tool.Scope(nil).Persistent {
		t.Fatalf("Scope().Persistent = true, want read-only scope")
	}
	if !tool.DeferInitialToolSchema() {
		t.Fatal("DeferInitialToolSchema() = false, want deferred control surface")
	}
}
