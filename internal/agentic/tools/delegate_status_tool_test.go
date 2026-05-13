package agentictools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	agenticenqueue "github.com/stello/elnath/internal/agentic/enqueue"
	"github.com/stello/elnath/internal/daemon"
)

func TestDelegateStatusToolSummarizesChildQueueAndEvidence(t *testing.T) {
	ctx := context.Background()
	db, store, _, parent := newGatewayTestStore(t)
	queue, err := daemon.NewQueueNoRecover(db)
	if err != nil {
		t.Fatalf("NewQueueNoRecover: %v", err)
	}
	createResult, err := NewDelegateCreateTool(store).Execute(ctx, json.RawMessage(`{
		"parent_task_id":`+jsonNumber(parent.ID)+`,
		"title":"Inspect delegated status",
		"prompt":"Inspect the delegated status tool."
	}`))
	if err != nil {
		t.Fatalf("delegate create: %v", err)
	}
	if createResult == nil || createResult.IsError {
		t.Fatalf("delegate create result = %+v", createResult)
	}
	var created delegateCreateToolOutput
	if err := json.Unmarshal([]byte(createResult.Output), &created); err != nil {
		t.Fatalf("unmarshal create output: %v", err)
	}

	service := agenticenqueue.NewService(store, queue, agenticenqueue.Options{})
	enqueued, err := service.Enqueue(ctx, agenticenqueue.Request{
		TaskID:     created.ChildTaskID,
		OperatorID: "model",
		Reason:     "bounded delegated status test",
	})
	if err != nil {
		t.Fatalf("enqueue child: %v", err)
	}
	actor := createGatewayTestActor(t, ctx, store, created.ChildTaskID, agentic.ActorRoleExecutor)
	if _, err := store.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:        created.ChildTaskID,
		ActorID:       actor.ID,
		ToolName:      "bash",
		ToolCallID:    "call-1",
		OutputSummary: strings.Repeat("verified ", 40),
		Status:        agentic.ReceiptStatusSucceeded,
	}); err != nil {
		t.Fatalf("CreateToolActionReceipt: %v", err)
	}
	run, err := store.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           created.ChildTaskID,
		VerifierActorID:  actor.ID,
		CriteriaJSON:     `["go test"]`,
		EvidenceRefsJSON: `["go test ./internal/agentic/tools -count=1"]`,
		Verdict:          agentic.VerificationVerdictPassed,
		Reason:           "delegated status evidence passed",
	})
	if err != nil {
		t.Fatalf("CreateVerificationRun: %v", err)
	}
	if _, err := store.CreateCompletionGate(ctx, agentic.CompletionGate{
		TaskID:             created.ChildTaskID,
		QueueTaskID:        enqueued.QueueTaskID,
		VerificationRunID:  run.ID,
		Status:             agentic.CompletionGateStatusPassed,
		Reason:             "latest verification passed",
		ReceiptSummaryJSON: `{"succeeded":1}`,
	}); err != nil {
		t.Fatalf("CreateCompletionGate: %v", err)
	}

	result, err := NewDelegateStatusTool(store, queue).Execute(ctx, json.RawMessage(`{"parent_task_id":`+jsonNumber(parent.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}

	var out delegateStatusToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.ParentTaskID != parent.ID || out.Total != 1 || len(out.Children) != 1 {
		t.Fatalf("output = %+v, want one delegated child", out)
	}
	child := out.Children[0]
	if child.TaskID != created.ChildTaskID || child.Status != agentic.TaskStatusPending || !child.Enqueued || child.QueueTaskID != enqueued.QueueTaskID {
		t.Fatalf("child status = %+v, want pending queue-backed delegated child", child)
	}
	if !child.QueueFound || child.QueueStatus != string(daemon.StatusPending) {
		t.Fatalf("child queue status = %+v, want found pending queue task", child)
	}
	if child.Totals.EnqueueDecisions != 1 || child.Totals.Receipts != 1 || child.Totals.VerificationRuns != 1 || child.Totals.CompletionGates != 1 {
		t.Fatalf("child totals = %+v, want one decision/receipt/verification/gate", child.Totals)
	}
	if child.LatestEnqueueDecision == nil || child.LatestEnqueueDecision.Status != agentic.TaskEnqueueStatusEnqueued || child.LatestEnqueueDecision.QueueTaskID != enqueued.QueueTaskID {
		t.Fatalf("latest enqueue decision = %+v", child.LatestEnqueueDecision)
	}
	if child.LatestReceipt == nil || child.LatestReceipt.Status != agentic.ReceiptStatusSucceeded {
		t.Fatalf("latest receipt = %+v", child.LatestReceipt)
	}
	if child.LatestVerification == nil || child.LatestVerification.Verdict != agentic.VerificationVerdictPassed {
		t.Fatalf("latest verification = %+v", child.LatestVerification)
	}
	if child.LatestCompletionGate == nil || child.LatestCompletionGate.Status != agentic.CompletionGateStatusPassed {
		t.Fatalf("latest completion gate = %+v", child.LatestCompletionGate)
	}
	if out.Receipt.Tool != DelegateStatusToolName || out.Receipt.Action != "status" || !out.Receipt.ReadOnly || out.Receipt.Persistent || out.Receipt.ParentTaskID != parent.ID || out.Receipt.Total != 1 {
		t.Fatalf("receipt = %+v, want read-only delegated status receipt", out.Receipt)
	}
}

func TestDelegateStatusToolCanNarrowToChild(t *testing.T) {
	ctx := context.Background()
	_, store, _, parent := newGatewayTestStore(t)
	createResult, err := NewDelegateCreateTool(store).Execute(ctx, json.RawMessage(`{
		"parent_task_id":`+jsonNumber(parent.ID)+`,
		"title":"Narrow child",
		"prompt":"Narrow to this child."
	}`))
	if err != nil {
		t.Fatalf("delegate create: %v", err)
	}
	var created delegateCreateToolOutput
	if err := json.Unmarshal([]byte(createResult.Output), &created); err != nil {
		t.Fatalf("unmarshal create output: %v", err)
	}

	result, err := NewDelegateStatusTool(store, nil).Execute(ctx, json.RawMessage(`{
		"parent_task_id":`+jsonNumber(parent.ID)+`,
		"child_task_id":`+jsonNumber(created.ChildTaskID)+`
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}
	var out delegateStatusToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.ChildTaskID != created.ChildTaskID || out.Total != 1 || out.Receipt.ChildTaskID != created.ChildTaskID {
		t.Fatalf("output = %+v, want narrowed child status", out)
	}
}

func TestDelegateStatusToolRequiresParentTask(t *testing.T) {
	_, store, _, _ := newGatewayTestStore(t)

	result, err := NewDelegateStatusTool(store, nil).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "parent_task_id is required") {
		t.Fatalf("result = %+v, want parent_task_id error", result)
	}
}

func TestDelegateStatusToolMetadataIsReadOnlyAndDeferred(t *testing.T) {
	tool := NewDelegateStatusTool(nil, nil)

	if tool.Name() != DelegateStatusToolName {
		t.Fatalf("Name() = %q, want %q", tool.Name(), DelegateStatusToolName)
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
