package agentictools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
)

func TestActorGraphToolListsActorsAndHandoffs(t *testing.T) {
	ctx := context.Background()
	_, store, _, task := newGatewayTestStore(t)
	planner := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRolePlanner)
	executor := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRoleExecutor)
	if _, err := store.CreateActorHandoff(ctx, agentic.ActorHandoff{
		TaskID:      task.ID,
		FromActorID: planner.ID,
		ToActorID:   executor.ID,
		HandoffType: "plan_to_executor",
		PayloadJSON: `{"summary":"implement one slice"}`,
		Status:      agentic.ActorStatusCreated,
	}); err != nil {
		t.Fatalf("CreateActorHandoff: %v", err)
	}

	result, err := NewActorGraphTool(store).Execute(ctx, json.RawMessage(`{"task_id":`+jsonNumber(task.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}

	var out actorGraphToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.TaskID != task.ID || out.TotalActors != 2 || out.TotalHandoffs != 1 {
		t.Fatalf("output counts = %+v, want task and actor/handoff counts", out)
	}
	if out.Actors[0].ID != planner.ID || out.Actors[0].Role != agentic.ActorRolePlanner {
		t.Fatalf("first actor = %+v, want planner", out.Actors[0])
	}
	if out.Handoffs[0].FromRole != agentic.ActorRolePlanner || out.Handoffs[0].ToRole != agentic.ActorRoleExecutor {
		t.Fatalf("handoff roles = %+v, want role labels", out.Handoffs[0])
	}
	if strings.Contains(result.Output, "implement one slice") {
		t.Fatalf("output leaked handoff payload; got %s", result.Output)
	}
}

func TestActorGraphToolRequiresTaskID(t *testing.T) {
	_, store, _, _ := newGatewayTestStore(t)

	result, err := NewActorGraphTool(store).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "task_id is required") {
		t.Fatalf("result = %+v, want task_id error", result)
	}
}

func TestActorGraphToolMetadataIsReadOnlyAndDeferred(t *testing.T) {
	tool := NewActorGraphTool(nil)

	if tool.Name() != ActorGraphToolName {
		t.Fatalf("Name() = %q, want %q", tool.Name(), ActorGraphToolName)
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

func jsonNumber(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
