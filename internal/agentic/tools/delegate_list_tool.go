package agentictools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/stello/elnath/internal/agentic"
	basetools "github.com/stello/elnath/internal/tools"
)

const DelegateListToolName = "agentic_delegate_list"

type delegateListStore interface {
	GetAgenticTask(context.Context, int64) (*agentic.AgenticTask, error)
	ListTaskEdgesByParent(context.Context, int64, string) ([]agentic.TaskEdge, error)
}

type DelegateListTool struct {
	store delegateListStore
}

func NewDelegateListTool(store delegateListStore) *DelegateListTool {
	return &DelegateListTool{store: store}
}

func (t *DelegateListTool) Name() string { return DelegateListToolName }

func (t *DelegateListTool) Description() string {
	return "List proposed child agentic tasks recorded as delegation intent for a parent task"
}

func (t *DelegateListTool) Schema() json.RawMessage {
	return basetools.Object(map[string]basetools.Property{
		"parent_task_id": basetools.Int("Parent agentic task id whose delegation children should be listed."),
	}, []string{"parent_task_id"})
}

func (t *DelegateListTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *DelegateListTool) Reversible() bool { return true }

func (t *DelegateListTool) Scope(json.RawMessage) basetools.ToolScope { return basetools.ToolScope{} }

func (t *DelegateListTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *DelegateListTool) DeferInitialToolSchema() bool { return true }

type delegateListToolInput struct {
	ParentTaskID int64 `json:"parent_task_id"`
}

type delegateListToolOutput struct {
	ParentTaskID int64                   `json:"parent_task_id"`
	Total        int                     `json:"total"`
	Children     []delegateListChildItem `json:"children"`
}

type delegateListChildItem struct {
	TaskID      int64  `json:"task_id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Priority    int    `json:"priority"`
	RiskLevel   string `json:"risk_level"`
	EdgeType    string `json:"edge_type"`
	Enqueued    bool   `json:"enqueued"`
	QueueTaskID int64  `json:"queue_task_id,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func (t *DelegateListTool) Execute(ctx context.Context, params json.RawMessage) (*basetools.Result, error) {
	if t == nil || t.store == nil {
		return basetools.ErrorResult("agentic_delegate_list: store unavailable"), nil
	}
	var input delegateListToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ParentTaskID == 0 {
		return basetools.ErrorResult("agentic_delegate_list: parent_task_id is required"), nil
	}
	if _, err := t.store.GetAgenticTask(ctx, input.ParentTaskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_list: parent task %d not found", input.ParentTaskID)), nil
		}
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_list: parent task %d: %v", input.ParentTaskID, err)), nil
	}
	edges, err := t.store.ListTaskEdgesByParent(ctx, input.ParentTaskID, delegateEdgeType)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_list: list edges: %v", err)), nil
	}

	children := make([]delegateListChildItem, 0, len(edges))
	for _, edge := range edges {
		child, err := t.store.GetAgenticTask(ctx, edge.ChildID)
		if err != nil {
			return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_list: child task %d: %v", edge.ChildID, err)), nil
		}
		children = append(children, delegateListChildItem{
			TaskID:      child.ID,
			Title:       child.Title,
			Status:      child.Status,
			Priority:    child.Priority,
			RiskLevel:   child.RiskLevel,
			EdgeType:    edge.EdgeType,
			Enqueued:    child.QueueTaskID != 0,
			QueueTaskID: child.QueueTaskID,
			CreatedAt:   formatEvidenceTime(child.CreatedAt),
		})
	}

	output := delegateListToolOutput{
		ParentTaskID: input.ParentTaskID,
		Total:        len(children),
		Children:     children,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_list: marshal output: %v", err)), nil
	}
	return basetools.SuccessResult(string(raw)), nil
}
