package agentictools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
)

func TestDelegateCreateToolCreatesProposedChildWithoutEnqueue(t *testing.T) {
	ctx := context.Background()
	_, store, _, parent := newGatewayTestStore(t)

	result, err := NewDelegateCreateTool(store).Execute(ctx, json.RawMessage(`{
		"parent_task_id":`+jsonNumber(parent.ID)+`,
		"title":"Inspect bounded spawn",
		"prompt":"Research the bounded spawn/send design and report risks.",
		"priority":3,
		"risk_level":"low"
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}

	var out delegateCreateToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.ParentTaskID != parent.ID || out.ChildTaskID == 0 || out.Status != agentic.TaskStatusProposed || out.Enqueued {
		t.Fatalf("output = %+v, want proposed non-enqueued child", out)
	}
	if out.EdgeType != delegateEdgeType || !strings.Contains(out.Boundary, "not enqueued") {
		t.Fatalf("output boundary = %+v, want non-execution edge boundary", out)
	}

	child, err := store.GetAgenticTask(ctx, out.ChildTaskID)
	if err != nil {
		t.Fatalf("GetAgenticTask child: %v", err)
	}
	if child.ParentID != parent.ID || child.QueueTaskID != 0 || child.Title != "Inspect bounded spawn" || child.Prompt == "" {
		t.Fatalf("child = %+v, want proposed child linked to parent without queue", child)
	}
	if child.AutonomyDecision != agentic.PolicyDecisionObserveOnly || child.VerificationStatus != agentic.VerificationStatusPending {
		t.Fatalf("child control fields = %+v, want observe-only pending verification", child)
	}
	if _, err := store.GetTaskEdge(ctx, parent.ID, child.ID, delegateEdgeType); err != nil {
		t.Fatalf("GetTaskEdge delegates_to: %v", err)
	}
}

func TestDelegateCreateToolRequiresTitleAndPrompt(t *testing.T) {
	_, store, _, _ := newGatewayTestStore(t)

	result, err := NewDelegateCreateTool(store).Execute(context.Background(), json.RawMessage(`{"title":"missing prompt"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "prompt is required") {
		t.Fatalf("result = %+v, want prompt error", result)
	}
}

func TestDelegateCreateToolMetadataIsMutatingAndDeferred(t *testing.T) {
	tool := NewDelegateCreateTool(nil)

	if tool.Name() != DelegateCreateToolName {
		t.Fatalf("Name() = %q, want %q", tool.Name(), DelegateCreateToolName)
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
