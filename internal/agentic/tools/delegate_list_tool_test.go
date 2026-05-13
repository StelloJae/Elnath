package agentictools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
)

func TestDelegateListToolListsProposedChildren(t *testing.T) {
	ctx := context.Background()
	_, store, _, parent := newGatewayTestStore(t)
	createDelegateChild(t, ctx, store, parent.ID, "Child one")
	createDelegateChild(t, ctx, store, parent.ID, "Child two")

	result, err := NewDelegateListTool(store).Execute(ctx, json.RawMessage(`{"parent_task_id":`+jsonNumber(parent.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}

	var out delegateListToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.ParentTaskID != parent.ID || out.Total != 2 {
		t.Fatalf("output = %+v, want two children", out)
	}
	if out.Children[0].Title != "Child one" || out.Children[1].Title != "Child two" {
		t.Fatalf("children = %+v, want ordered children", out.Children)
	}
	if out.Children[0].Status != agentic.TaskStatusProposed || out.Children[0].Enqueued {
		t.Fatalf("child boundary = %+v, want proposed non-enqueued", out.Children[0])
	}
	if out.Receipt.Tool != DelegateListToolName || out.Receipt.Action != "list" || !out.Receipt.ReadOnly || out.Receipt.Persistent || out.Receipt.ParentTaskID != parent.ID || out.Receipt.Total != 2 || out.Receipt.ExecutionPolicy != "agentic_delegation_observation" {
		t.Fatalf("receipt = %+v, want delegation list receipt", out.Receipt)
	}
}

func TestDelegateListToolRequiresParentTaskID(t *testing.T) {
	_, store, _, _ := newGatewayTestStore(t)

	result, err := NewDelegateListTool(store).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "parent_task_id is required") {
		t.Fatalf("result = %+v, want parent_task_id error", result)
	}
}

func TestDelegateListToolMetadataIsReadOnlyAndDeferred(t *testing.T) {
	tool := NewDelegateListTool(nil)

	if tool.Name() != DelegateListToolName {
		t.Fatalf("Name() = %q, want %q", tool.Name(), DelegateListToolName)
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

func createDelegateChild(t *testing.T, ctx context.Context, store *agentic.Store, parentID int64, title string) *agentic.AgenticTask {
	t.Helper()
	result, err := NewDelegateCreateTool(store).Execute(ctx, json.RawMessage(`{
		"parent_task_id":`+jsonNumber(parentID)+`,
		"title":`+jsonString(title)+`,
		"prompt":"Do the delegated work."
	}`))
	if err != nil {
		t.Fatalf("NewDelegateCreateTool.Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("delegate create result = %+v, want success", result)
	}
	var out delegateCreateToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal delegate create: %v", err)
	}
	task, err := store.GetAgenticTask(ctx, out.ChildTaskID)
	if err != nil {
		t.Fatalf("GetAgenticTask child: %v", err)
	}
	return task
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
