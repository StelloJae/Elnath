package agentictools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/config"
	basetools "github.com/stello/elnath/internal/tools"
)

const DelegateEnqueueToolName = "agentic_delegate_enqueue"

type delegateEnqueueStore interface {
	GetAgenticTask(context.Context, int64) (*agentic.AgenticTask, error)
	GetTaskEdge(context.Context, int64, int64, string) (*agentic.TaskEdge, error)
}

type delegateEnqueueService interface {
	EnqueueDelegated(context.Context, DelegateEnqueueRequest) (*DelegateEnqueueResult, error)
}

type DelegateEnqueueRequest struct {
	TaskID                  int64
	OperatorID              string
	Reason                  string
	RequestedEnforcement    string
	RequestedCompletionGate string
}

type DelegateEnqueueResult struct {
	QueueTaskID    int64
	Existed        bool
	DecisionID     int64
	DecisionStatus string
}

type DelegateEnqueueTool struct {
	store   delegateEnqueueStore
	service delegateEnqueueService
}

func NewDelegateEnqueueTool(store delegateEnqueueStore, service delegateEnqueueService) *DelegateEnqueueTool {
	return &DelegateEnqueueTool{store: store, service: service}
}

func (t *DelegateEnqueueTool) Name() string { return DelegateEnqueueToolName }

func (t *DelegateEnqueueTool) Description() string {
	return "Enqueue a proposed delegated child agentic task for daemon execution"
}

func (t *DelegateEnqueueTool) Schema() json.RawMessage {
	return basetools.Object(map[string]basetools.Property{
		"child_task_id":           basetools.Int("Delegated child agentic task id to enqueue."),
		"parent_task_id":          basetools.Int("Optional parent id. Defaults to the child task parent_id."),
		"operator_id":             basetools.String("Optional operator or actor identifier for the enqueue decision."),
		"reason":                  basetools.String("Short reason recorded on the enqueue decision."),
		"agentic_enforcement":     basetools.StringEnum("Optional requested enforcement mode.", config.AgenticEnforcementModeGateway),
		"agentic_completion_gate": basetools.StringEnum("Optional requested completion gate mode.", config.AgenticCompletionGateModeVerification),
	}, []string{"child_task_id"})
}

func (t *DelegateEnqueueTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *DelegateEnqueueTool) Reversible() bool { return false }

func (t *DelegateEnqueueTool) Scope(json.RawMessage) basetools.ToolScope {
	return basetools.ToolScope{Persistent: true}
}

func (t *DelegateEnqueueTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *DelegateEnqueueTool) DeferInitialToolSchema() bool { return true }

type delegateEnqueueToolInput struct {
	ChildTaskID           int64  `json:"child_task_id"`
	ParentTaskID          int64  `json:"parent_task_id"`
	OperatorID            string `json:"operator_id"`
	Reason                string `json:"reason"`
	AgenticEnforcement    string `json:"agentic_enforcement"`
	AgenticCompletionGate string `json:"agentic_completion_gate"`
}

type delegateEnqueueToolOutput struct {
	ParentTaskID         int64              `json:"parent_task_id"`
	ChildTaskID          int64              `json:"child_task_id"`
	QueueTaskID          int64              `json:"queue_task_id"`
	Enqueued             bool               `json:"enqueued"`
	Deduplicated         bool               `json:"deduplicated"`
	DecisionID           int64              `json:"decision_id,omitempty"`
	DecisionStatus       string             `json:"decision_status,omitempty"`
	RequestedEnforcement string             `json:"requested_enforcement,omitempty"`
	RequestedCompletion  string             `json:"requested_completion_gate,omitempty"`
	Boundary             string             `json:"boundary"`
	Receipt              agenticToolReceipt `json:"receipt"`
}

func (t *DelegateEnqueueTool) Execute(ctx context.Context, params json.RawMessage) (*basetools.Result, error) {
	if t == nil || t.store == nil {
		return basetools.ErrorResult("agentic_delegate_enqueue: store unavailable"), nil
	}
	var input delegateEnqueueToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ChildTaskID == 0 {
		return basetools.ErrorResult("agentic_delegate_enqueue: child_task_id is required"), nil
	}

	child, err := t.store.GetAgenticTask(ctx, input.ChildTaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_enqueue: child task %d not found", input.ChildTaskID)), nil
		}
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_enqueue: child task %d: %v", input.ChildTaskID, err)), nil
	}
	parentID := input.ParentTaskID
	if parentID == 0 {
		parentID = child.ParentID
	}
	if parentID == 0 {
		return basetools.ErrorResult("agentic_delegate_enqueue: child task is not a delegated child"), nil
	}
	if _, err := t.store.GetTaskEdge(ctx, parentID, child.ID, delegateEdgeType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return basetools.ErrorResult("agentic_delegate_enqueue: delegates_to edge not found for delegated child"), nil
		}
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_enqueue: delegates_to edge lookup: %v", err)), nil
	}
	if t.service == nil {
		return basetools.ErrorResult("agentic_delegate_enqueue: enqueue service unavailable"), nil
	}

	result, err := t.service.EnqueueDelegated(ctx, DelegateEnqueueRequest{
		TaskID:                  child.ID,
		OperatorID:              strings.TrimSpace(input.OperatorID),
		Reason:                  strings.TrimSpace(input.Reason),
		RequestedEnforcement:    input.AgenticEnforcement,
		RequestedCompletionGate: input.AgenticCompletionGate,
	})
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_enqueue: %v", err)), nil
	}
	output := delegateEnqueueToolOutput{
		ParentTaskID:         parentID,
		ChildTaskID:          child.ID,
		QueueTaskID:          result.QueueTaskID,
		Enqueued:             result.QueueTaskID != 0,
		Deduplicated:         result.Existed,
		RequestedEnforcement: input.AgenticEnforcement,
		RequestedCompletion:  input.AgenticCompletionGate,
		Boundary:             "delegated child task enqueued through daemon queue; execution remains queue/receipt backed",
	}
	output.DecisionID = result.DecisionID
	output.DecisionStatus = result.DecisionStatus
	output.Receipt = agenticToolReceipt{
		Tool:            DelegateEnqueueToolName,
		Action:          "enqueue",
		ReadOnly:        false,
		Persistent:      true,
		ExecutionPolicy: "agentic_delegation_enqueue",
		ParentTaskID:    parentID,
		ChildTaskID:     child.ID,
		QueueTaskID:     result.QueueTaskID,
		DecisionID:      result.DecisionID,
		DecisionStatus:  result.DecisionStatus,
		Enqueued:        result.QueueTaskID != 0,
		Deduplicated:    result.Existed,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_enqueue: marshal output: %v", err)), nil
	}
	return basetools.SuccessResult(string(raw)), nil
}
