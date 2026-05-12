package agentictools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agentic"
	basetools "github.com/stello/elnath/internal/tools"
)

const (
	DelegateCreateToolName = "agentic_delegate_create"
	delegateEdgeType       = "delegates_to"
)

type delegateCreateStore interface {
	GetAgenticTask(context.Context, int64) (*agentic.AgenticTask, error)
	CreateAgenticTask(context.Context, agentic.AgenticTask) (*agentic.AgenticTask, error)
	CreateTaskEdge(context.Context, agentic.TaskEdge) (*agentic.TaskEdge, error)
}

type DelegateCreateTool struct {
	store delegateCreateStore
}

func NewDelegateCreateTool(store delegateCreateStore) *DelegateCreateTool {
	return &DelegateCreateTool{store: store}
}

func (t *DelegateCreateTool) Name() string { return DelegateCreateToolName }

func (t *DelegateCreateTool) Description() string {
	return "Record a proposed child agentic task as delegation intent without enqueueing execution"
}

func (t *DelegateCreateTool) Schema() json.RawMessage {
	return basetools.Object(map[string]basetools.Property{
		"parent_task_id": basetools.Int("Optional parent agentic task id to link this delegation under."),
		"title":          basetools.String("Short title for the proposed child task."),
		"prompt":         basetools.String("Prompt/instructions for the proposed child task."),
		"priority":       basetools.Int("Optional priority. Defaults to the parent priority when available, otherwise 1."),
		"risk_level":     basetools.StringEnum("Optional risk level. Defaults to low.", agentic.RiskLevelLow, agentic.RiskLevelMedium, agentic.RiskLevelHigh, agentic.RiskLevelCritical),
	}, []string{"title", "prompt"})
}

func (t *DelegateCreateTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *DelegateCreateTool) Reversible() bool { return false }

func (t *DelegateCreateTool) Scope(json.RawMessage) basetools.ToolScope {
	return basetools.ToolScope{Persistent: true}
}

func (t *DelegateCreateTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *DelegateCreateTool) DeferInitialToolSchema() bool { return true }

type delegateCreateToolInput struct {
	ParentTaskID int64  `json:"parent_task_id"`
	Title        string `json:"title"`
	Prompt       string `json:"prompt"`
	Priority     int    `json:"priority"`
	RiskLevel    string `json:"risk_level"`
}

type delegateCreateToolOutput struct {
	ParentTaskID int64  `json:"parent_task_id,omitempty"`
	ChildTaskID  int64  `json:"child_task_id"`
	Status       string `json:"status"`
	EdgeType     string `json:"edge_type,omitempty"`
	Enqueued     bool   `json:"enqueued"`
	Boundary     string `json:"boundary"`
}

func (t *DelegateCreateTool) Execute(ctx context.Context, params json.RawMessage) (*basetools.Result, error) {
	if t == nil || t.store == nil {
		return basetools.ErrorResult("agentic_delegate_create: store unavailable"), nil
	}
	var input delegateCreateToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return basetools.ErrorResult("agentic_delegate_create: title is required"), nil
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return basetools.ErrorResult("agentic_delegate_create: prompt is required"), nil
	}

	var parent *agentic.AgenticTask
	if input.ParentTaskID != 0 {
		task, err := t.store.GetAgenticTask(ctx, input.ParentTaskID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_create: parent task %d not found", input.ParentTaskID)), nil
			}
			return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_create: parent task %d: %v", input.ParentTaskID, err)), nil
		}
		parent = task
	}

	priority := input.Priority
	if priority <= 0 {
		priority = 1
		if parent != nil && parent.Priority > 0 {
			priority = parent.Priority
		}
	}
	risk, ok := normalizeDelegateRisk(input.RiskLevel)
	if !ok {
		return basetools.ErrorResult("agentic_delegate_create: risk_level must be low, medium, high, or critical"), nil
	}

	child := agentic.AgenticTask{
		Title:              title,
		Prompt:             prompt,
		Status:             agentic.TaskStatusProposed,
		Priority:           priority,
		RiskLevel:          risk,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	}
	if parent != nil {
		child.ParentID = parent.ID
		child.GoalID = parent.GoalID
		child.SignalID = parent.SignalID
	}
	created, err := t.store.CreateAgenticTask(ctx, child)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_create: create child task: %v", err)), nil
	}

	edgeType := ""
	if parent != nil {
		if _, err := t.store.CreateTaskEdge(ctx, agentic.TaskEdge{
			ParentID: parent.ID,
			ChildID:  created.ID,
			EdgeType: delegateEdgeType,
		}); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_create: create task edge: %v", err)), nil
		}
		edgeType = delegateEdgeType
	}

	output := delegateCreateToolOutput{
		ParentTaskID: input.ParentTaskID,
		ChildTaskID:  created.ID,
		Status:       created.Status,
		EdgeType:     edgeType,
		Enqueued:     false,
		Boundary:     "delegation intent recorded; child task is proposed and not enqueued",
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_create: marshal output: %v", err)), nil
	}
	return basetools.SuccessResult(string(raw)), nil
}

func normalizeDelegateRisk(risk string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "":
		return agentic.RiskLevelLow, true
	case agentic.RiskLevelLow, agentic.RiskLevelMedium, agentic.RiskLevelHigh, agentic.RiskLevelCritical:
		return strings.ToLower(strings.TrimSpace(risk)), true
	default:
		return "", false
	}
}
