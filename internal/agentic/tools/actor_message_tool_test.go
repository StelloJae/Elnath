package agentictools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
)

func TestActorMessageSendToolRecordsMailboxAndHandoff(t *testing.T) {
	ctx := context.Background()
	_, store, _, task := newGatewayTestStore(t)
	planner := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRolePlanner)
	executor := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRoleExecutor)

	result, err := NewActorMessageSendTool(store).Execute(ctx, json.RawMessage(`{
		"task_id":`+jsonNumber(task.ID)+`,
		"from_actor_id":`+jsonNumber(planner.ID)+`,
		"to_actor_id":`+jsonNumber(executor.ID)+`,
		"summary":"handoff next slice",
		"message":"Please inspect the next bounded tool surface."
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}

	var out actorMessageSendToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.TaskID != task.ID || out.FromActorID != planner.ID || out.ToActorID != executor.ID || out.HandoffID == 0 {
		t.Fatalf("output = %+v, want routed message with handoff id", out)
	}
	if !strings.Contains(out.Boundary, "mailbox record") {
		t.Fatalf("boundary = %q, want mailbox boundary", out.Boundary)
	}

	updatedPlanner, err := store.GetAgentActor(ctx, planner.ID)
	if err != nil {
		t.Fatalf("GetAgentActor planner: %v", err)
	}
	updatedExecutor, err := store.GetAgentActor(ctx, executor.ID)
	if err != nil {
		t.Fatalf("GetAgentActor executor: %v", err)
	}
	if !strings.Contains(updatedPlanner.OutboxJSON, "Please inspect") || !strings.Contains(updatedExecutor.InboxJSON, "Please inspect") {
		t.Fatalf("mailboxes not updated: planner outbox=%s executor inbox=%s", updatedPlanner.OutboxJSON, updatedExecutor.InboxJSON)
	}
	handoffs, err := store.ListActorHandoffsByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListActorHandoffsByTask: %v", err)
	}
	if len(handoffs) != 1 || handoffs[0].HandoffType != actorMessageHandoffType || handoffs[0].FromActorID != planner.ID || handoffs[0].ToActorID != executor.ID {
		t.Fatalf("handoffs = %+v, want actor message handoff", handoffs)
	}
}

func TestActorMessageListToolListsInboxAndOutbox(t *testing.T) {
	ctx := context.Background()
	_, store, _, task := newGatewayTestStore(t)
	planner := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRolePlanner)
	executor := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRoleExecutor)
	sendResult, err := NewActorMessageSendTool(store).Execute(ctx, json.RawMessage(`{
		"task_id":`+jsonNumber(task.ID)+`,
		"from_actor_id":`+jsonNumber(planner.ID)+`,
		"to_actor_id":`+jsonNumber(executor.ID)+`,
		"message":"List this message."
	}`))
	if err != nil {
		t.Fatalf("send Execute: %v", err)
	}
	if sendResult == nil || sendResult.IsError {
		t.Fatalf("send result = %+v, want success", sendResult)
	}

	result, err := NewActorMessageListTool(store).Execute(ctx, json.RawMessage(`{
		"task_id":`+jsonNumber(task.ID)+`,
		"actor_id":`+jsonNumber(executor.ID)+`,
		"box":"inbox"
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success", result)
	}

	var out actorMessageListToolOutput
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, result.Output)
	}
	if out.ActorID != executor.ID || out.Box != "inbox" || out.Total != 1 || out.Messages[0].Text != "List this message." {
		t.Fatalf("output = %+v, want one inbox message", out)
	}
}

func TestActorMessageToolsRejectCrossTaskSend(t *testing.T) {
	ctx := context.Background()
	_, store, _, task := newGatewayTestStore(t)
	otherTask, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		Title:              "Other",
		Prompt:             "other task",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask other: %v", err)
	}
	planner := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRolePlanner)
	otherActor := createGatewayTestActor(t, ctx, store, otherTask.ID, agentic.ActorRoleExecutor)

	result, err := NewActorMessageSendTool(store).Execute(ctx, json.RawMessage(`{
		"task_id":`+jsonNumber(task.ID)+`,
		"from_actor_id":`+jsonNumber(planner.ID)+`,
		"to_actor_id":`+jsonNumber(otherActor.ID)+`,
		"message":"cross task"
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "must belong to task") {
		t.Fatalf("result = %+v, want cross-task error", result)
	}
}

func TestActorMessageSendToolRejectsSelfSend(t *testing.T) {
	ctx := context.Background()
	_, store, _, task := newGatewayTestStore(t)
	planner := createGatewayTestActor(t, ctx, store, task.ID, agentic.ActorRolePlanner)

	result, err := NewActorMessageSendTool(store).Execute(ctx, json.RawMessage(`{
		"task_id":`+jsonNumber(task.ID)+`,
		"from_actor_id":`+jsonNumber(planner.ID)+`,
		"to_actor_id":`+jsonNumber(planner.ID)+`,
		"message":"self"
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "recipient must differ") {
		t.Fatalf("result = %+v, want self-send error", result)
	}
}

func TestActorMessageToolMetadata(t *testing.T) {
	send := NewActorMessageSendTool(nil)
	if send.Name() != ActorMessageSendToolName {
		t.Fatalf("send Name() = %q, want %q", send.Name(), ActorMessageSendToolName)
	}
	if send.IsConcurrencySafe(nil) || send.Reversible() || !send.Scope(nil).Persistent || !send.DeferInitialToolSchema() {
		t.Fatalf("send metadata = concurrency:%t reversible:%t persistent:%t deferred:%t, want mutating deferred", send.IsConcurrencySafe(nil), send.Reversible(), send.Scope(nil).Persistent, send.DeferInitialToolSchema())
	}

	list := NewActorMessageListTool(nil)
	if list.Name() != ActorMessageListToolName {
		t.Fatalf("list Name() = %q, want %q", list.Name(), ActorMessageListToolName)
	}
	if !list.IsConcurrencySafe(nil) || !list.Reversible() || list.Scope(nil).Persistent || !list.DeferInitialToolSchema() {
		t.Fatalf("list metadata = concurrency:%t reversible:%t persistent:%t deferred:%t, want read-only deferred", list.IsConcurrencySafe(nil), list.Reversible(), list.Scope(nil).Persistent, list.DeferInitialToolSchema())
	}
}
