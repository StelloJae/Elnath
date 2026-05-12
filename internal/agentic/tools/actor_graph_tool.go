package agentictools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/stello/elnath/internal/agentic"
	basetools "github.com/stello/elnath/internal/tools"
)

const ActorGraphToolName = "agentic_actor_graph"

type actorGraphStore interface {
	GetAgenticTask(context.Context, int64) (*agentic.AgenticTask, error)
	ListAgentActorsByTask(context.Context, int64) ([]agentic.AgentActor, error)
	ListActorHandoffsByTask(context.Context, int64) ([]agentic.ActorHandoff, error)
}

type ActorGraphTool struct {
	store actorGraphStore
}

func NewActorGraphTool(store actorGraphStore) *ActorGraphTool {
	return &ActorGraphTool{store: store}
}

func (t *ActorGraphTool) Name() string { return ActorGraphToolName }

func (t *ActorGraphTool) Description() string {
	return "List agentic task actors and handoffs as read-only execution graph metadata"
}

func (t *ActorGraphTool) Schema() json.RawMessage {
	return basetools.Object(map[string]basetools.Property{
		"task_id": basetools.Int("Agentic task id to inspect."),
	}, []string{"task_id"})
}

func (t *ActorGraphTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *ActorGraphTool) Reversible() bool { return true }

func (t *ActorGraphTool) Scope(json.RawMessage) basetools.ToolScope { return basetools.ToolScope{} }

func (t *ActorGraphTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *ActorGraphTool) DeferInitialToolSchema() bool { return true }

type actorGraphToolInput struct {
	TaskID int64 `json:"task_id"`
}

type actorGraphToolOutput struct {
	TaskID        int64                   `json:"task_id"`
	TaskStatus    string                  `json:"task_status"`
	TaskTitle     string                  `json:"task_title,omitempty"`
	TotalActors   int                     `json:"total_actors"`
	Actors        []actorGraphActorItem   `json:"actors"`
	TotalHandoffs int                     `json:"total_handoffs"`
	Handoffs      []actorGraphHandoffItem `json:"handoffs"`
}

type actorGraphActorItem struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type actorGraphHandoffItem struct {
	ID          int64  `json:"id"`
	FromActorID int64  `json:"from_actor_id"`
	FromRole    string `json:"from_role,omitempty"`
	ToActorID   int64  `json:"to_actor_id"`
	ToRole      string `json:"to_role,omitempty"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func (t *ActorGraphTool) Execute(ctx context.Context, params json.RawMessage) (*basetools.Result, error) {
	if t == nil || t.store == nil {
		return basetools.ErrorResult("agentic_actor_graph: store unavailable"), nil
	}
	var input actorGraphToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.TaskID == 0 {
		return basetools.ErrorResult("agentic_actor_graph: task_id is required"), nil
	}

	task, err := t.store.GetAgenticTask(ctx, input.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return basetools.ErrorResult(fmt.Sprintf("agentic_actor_graph: task %d not found", input.TaskID)), nil
		}
		return basetools.ErrorResult(fmt.Sprintf("agentic_actor_graph: task %d: %v", input.TaskID, err)), nil
	}
	actors, err := t.store.ListAgentActorsByTask(ctx, input.TaskID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_actor_graph: list actors: %v", err)), nil
	}
	handoffs, err := t.store.ListActorHandoffsByTask(ctx, input.TaskID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_actor_graph: list handoffs: %v", err)), nil
	}

	roleByActor := make(map[int64]string, len(actors))
	actorItems := make([]actorGraphActorItem, 0, len(actors))
	for _, actor := range actors {
		roleByActor[actor.ID] = actor.Role
		actorItems = append(actorItems, actorGraphActorItem{
			ID:        actor.ID,
			Role:      actor.Role,
			Status:    actor.Status,
			CreatedAt: formatActorGraphTime(actor.CreatedAt),
			UpdatedAt: formatActorGraphTime(actor.UpdatedAt),
		})
	}
	handoffItems := make([]actorGraphHandoffItem, 0, len(handoffs))
	for _, handoff := range handoffs {
		handoffItems = append(handoffItems, actorGraphHandoffItem{
			ID:          handoff.ID,
			FromActorID: handoff.FromActorID,
			FromRole:    roleByActor[handoff.FromActorID],
			ToActorID:   handoff.ToActorID,
			ToRole:      roleByActor[handoff.ToActorID],
			Type:        handoff.HandoffType,
			Status:      handoff.Status,
			CreatedAt:   formatActorGraphTime(handoff.CreatedAt),
		})
	}

	output := actorGraphToolOutput{
		TaskID:        task.ID,
		TaskStatus:    task.Status,
		TaskTitle:     task.Title,
		TotalActors:   len(actorItems),
		Actors:        actorItems,
		TotalHandoffs: len(handoffItems),
		Handoffs:      handoffItems,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_actor_graph: marshal output: %v", err)), nil
	}
	return basetools.SuccessResult(string(raw)), nil
}

func formatActorGraphTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
