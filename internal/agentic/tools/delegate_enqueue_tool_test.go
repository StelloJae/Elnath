package agentictools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
)

type fakeDelegateEnqueueService struct {
	req DelegateEnqueueRequest
}

func (s *fakeDelegateEnqueueService) EnqueueDelegated(_ context.Context, req DelegateEnqueueRequest) (*DelegateEnqueueResult, error) {
	s.req = req
	return &DelegateEnqueueResult{
		QueueTaskID:    91,
		DecisionID:     7,
		DecisionStatus: agentic.TaskEnqueueStatusEnqueued,
	}, nil
}

func TestDelegateEnqueueToolEnqueuesDelegatedChild(t *testing.T) {
	ctx := context.Background()
	_, store, _, parent := newGatewayTestStore(t)
	createResult, err := NewDelegateCreateTool(store).Execute(ctx, json.RawMessage(`{
		"parent_task_id":`+jsonNumber(parent.ID)+`,
		"title":"Implement child slice",
		"prompt":"Implement the bounded child slice."
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
	service := &fakeDelegateEnqueueService{}

	result, err := NewDelegateEnqueueTool(store, service).Execute(ctx, json.RawMessage(`{
		"child_task_id":`+jsonNumber(created.ChildTaskID)+`,
		"operator_id":"model",
		"reason":"bounded delegated execution"
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}

	var out delegateEnqueueToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.ParentTaskID != parent.ID || out.ChildTaskID != created.ChildTaskID || out.QueueTaskID == 0 || !out.Enqueued || out.Deduplicated {
		t.Fatalf("output = %+v, want enqueued delegated child", out)
	}
	if out.DecisionStatus != agentic.TaskEnqueueStatusEnqueued || !strings.Contains(out.Boundary, "daemon queue") {
		t.Fatalf("output boundary = %+v, want explicit queue boundary", out)
	}
	if out.Receipt.Tool != DelegateEnqueueToolName || out.Receipt.Action != "enqueue" || out.Receipt.ParentTaskID != parent.ID || out.Receipt.ChildTaskID != created.ChildTaskID || out.Receipt.QueueTaskID != out.QueueTaskID || out.Receipt.DecisionID != out.DecisionID || out.Receipt.ExecutionPolicy != "agentic_delegation_enqueue" || !out.Receipt.Enqueued {
		t.Fatalf("receipt = %+v, want delegated enqueue receipt", out.Receipt)
	}
	if service.req.TaskID != created.ChildTaskID || service.req.OperatorID != "model" || service.req.Reason != "bounded delegated execution" {
		t.Fatalf("service request = %+v, want delegated child enqueue request", service.req)
	}
}

func TestDelegateEnqueueToolRequiresDelegatedChild(t *testing.T) {
	ctx := context.Background()
	_, store, _, _ := newGatewayTestStore(t)
	child, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Unlinked",
		Prompt:             "Should not enqueue through delegate tool",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}

	result, err := NewDelegateEnqueueTool(store, nil).Execute(ctx, json.RawMessage(`{"child_task_id":`+jsonNumber(child.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "delegated child") {
		t.Fatalf("result = %+v, want delegated child error", result)
	}
}

func TestDelegateEnqueueToolMetadataIsMutatingAndDeferred(t *testing.T) {
	tool := NewDelegateEnqueueTool(nil, nil)

	if tool.Name() != DelegateEnqueueToolName {
		t.Fatalf("Name() = %q, want %q", tool.Name(), DelegateEnqueueToolName)
	}
	if tool.IsConcurrencySafe(nil) || tool.Reversible() {
		t.Fatalf("metadata = concurrency:%t reversible:%t, want persistent mutating", tool.IsConcurrencySafe(nil), tool.Reversible())
	}
	if !tool.Scope(nil).Persistent {
		t.Fatalf("Scope().Persistent = false, want persistent write scope")
	}
	if !tool.DeferInitialToolSchema() {
		t.Fatal("DeferInitialToolSchema() = false, want deferred control surface")
	}
}
