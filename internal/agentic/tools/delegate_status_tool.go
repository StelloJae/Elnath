package agentictools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"
	basetools "github.com/stello/elnath/internal/tools"
)

const DelegateStatusToolName = "agentic_delegate_status"

type delegateStatusStore interface {
	GetAgenticTask(context.Context, int64) (*agentic.AgenticTask, error)
	ListTaskEdgesByParent(context.Context, int64, string) ([]agentic.TaskEdge, error)
	ListTaskEnqueueDecisionsByTask(context.Context, int64) ([]agentic.TaskEnqueueDecision, error)
	ListToolActionReceiptsByTask(context.Context, int64) ([]agentic.ToolActionReceipt, error)
	ListVerificationRunsByTask(context.Context, int64) ([]agentic.VerificationRun, error)
	ListCompletionGatesByTask(context.Context, int64) ([]agentic.CompletionGate, error)
}

type delegateStatusQueue interface {
	Get(context.Context, int64) (*daemon.Task, error)
}

type DelegateStatusTool struct {
	store delegateStatusStore
	queue delegateStatusQueue
}

func NewDelegateStatusTool(store delegateStatusStore, queue delegateStatusQueue) *DelegateStatusTool {
	return &DelegateStatusTool{store: store, queue: queue}
}

func (t *DelegateStatusTool) Name() string { return DelegateStatusToolName }

func (t *DelegateStatusTool) Description() string {
	return "Summarize delegated child task status, queue status, and verification evidence for a parent agentic task"
}

func (t *DelegateStatusTool) Schema() json.RawMessage {
	return basetools.Object(map[string]basetools.Property{
		"parent_task_id": basetools.Int("Parent agentic task id whose delegated child statuses should be summarized."),
		"child_task_id":  basetools.Int("Optional delegated child agentic task id to narrow the status view."),
	}, []string{"parent_task_id"})
}

func (t *DelegateStatusTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *DelegateStatusTool) Reversible() bool { return true }

func (t *DelegateStatusTool) Scope(json.RawMessage) basetools.ToolScope { return basetools.ToolScope{} }

func (t *DelegateStatusTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *DelegateStatusTool) DeferInitialToolSchema() bool { return true }

type delegateStatusToolInput struct {
	ParentTaskID int64 `json:"parent_task_id"`
	ChildTaskID  int64 `json:"child_task_id"`
}

type delegateStatusToolOutput struct {
	ParentTaskID int64                     `json:"parent_task_id"`
	ChildTaskID  int64                     `json:"child_task_id,omitempty"`
	Total        int                       `json:"total"`
	Children     []delegateStatusChildItem `json:"children"`
	Receipt      agenticToolReceipt        `json:"receipt"`
}

type delegateStatusChildItem struct {
	TaskID                 int64                              `json:"task_id"`
	Title                  string                             `json:"title,omitempty"`
	Status                 string                             `json:"status"`
	Priority               int                                `json:"priority"`
	RiskLevel              string                             `json:"risk_level,omitempty"`
	EdgeType               string                             `json:"edge_type"`
	Enqueued               bool                               `json:"enqueued"`
	QueueTaskID            int64                              `json:"queue_task_id,omitempty"`
	QueueFound             bool                               `json:"queue_found"`
	QueueStatus            string                             `json:"queue_status,omitempty"`
	QueueProgress          string                             `json:"queue_progress,omitempty"`
	QueueSummary           string                             `json:"queue_summary,omitempty"`
	QueueUpdatedAt         string                             `json:"queue_updated_at,omitempty"`
	QueueCompletedAt       string                             `json:"queue_completed_at,omitempty"`
	QueueError             string                             `json:"queue_error,omitempty"`
	LatestEnqueueDecision  *delegateStatusEnqueueDecisionItem `json:"latest_enqueue_decision,omitempty"`
	Totals                 delegateStatusEvidenceTotals       `json:"totals"`
	ReceiptStatusCounts    map[string]int                     `json:"receipt_status_counts,omitempty"`
	VerificationVerdicts   map[string]int                     `json:"verification_verdicts,omitempty"`
	CompletionGateStatuses map[string]int                     `json:"completion_gate_statuses,omitempty"`
	LatestReceipt          *taskEvidenceReceiptItem           `json:"latest_receipt,omitempty"`
	LatestVerification     *taskEvidenceVerificationItem      `json:"latest_verification,omitempty"`
	LatestCompletionGate   *taskEvidenceCompletionGateItem    `json:"latest_completion_gate,omitempty"`
	CreatedAt              string                             `json:"created_at,omitempty"`
	UpdatedAt              string                             `json:"updated_at,omitempty"`
}

type delegateStatusEvidenceTotals struct {
	EnqueueDecisions int `json:"enqueue_decisions"`
	Receipts         int `json:"receipts"`
	VerificationRuns int `json:"verification_runs"`
	CompletionGates  int `json:"completion_gates"`
}

type delegateStatusEnqueueDecisionItem struct {
	ID                      int64  `json:"id"`
	QueueTaskID             int64  `json:"queue_task_id,omitempty"`
	Decision                string `json:"decision"`
	Status                  string `json:"status"`
	Reason                  string `json:"reason,omitempty"`
	RequestedEnforcement    string `json:"requested_enforcement,omitempty"`
	RequestedCompletionGate string `json:"requested_completion_gate,omitempty"`
	FailureReason           string `json:"failure_reason,omitempty"`
	UpdatedAt               string `json:"updated_at,omitempty"`
}

func (t *DelegateStatusTool) Execute(ctx context.Context, params json.RawMessage) (*basetools.Result, error) {
	if t == nil || t.store == nil {
		return basetools.ErrorResult("agentic_delegate_status: store unavailable"), nil
	}
	var input delegateStatusToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ParentTaskID == 0 {
		return basetools.ErrorResult("agentic_delegate_status: parent_task_id is required"), nil
	}
	if _, err := t.store.GetAgenticTask(ctx, input.ParentTaskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_status: parent task %d not found", input.ParentTaskID)), nil
		}
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_status: parent task %d: %v", input.ParentTaskID, err)), nil
	}

	edges, err := t.store.ListTaskEdgesByParent(ctx, input.ParentTaskID, delegateEdgeType)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_status: list edges: %v", err)), nil
	}
	children := make([]delegateStatusChildItem, 0, len(edges))
	for _, edge := range edges {
		if input.ChildTaskID != 0 && edge.ChildID != input.ChildTaskID {
			continue
		}
		child, err := t.store.GetAgenticTask(ctx, edge.ChildID)
		if err != nil {
			return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_status: child task %d: %v", edge.ChildID, err)), nil
		}
		item, err := t.childStatus(ctx, edge, child)
		if err != nil {
			return basetools.ErrorResult(err.Error()), nil
		}
		children = append(children, item)
	}
	if input.ChildTaskID != 0 && len(children) == 0 {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_status: child task %d is not delegated by parent task %d", input.ChildTaskID, input.ParentTaskID)), nil
	}

	output := delegateStatusToolOutput{
		ParentTaskID: input.ParentTaskID,
		ChildTaskID:  input.ChildTaskID,
		Total:        len(children),
		Children:     children,
		Receipt: agenticToolReceipt{
			Tool:            DelegateStatusToolName,
			Action:          "status",
			ReadOnly:        true,
			Persistent:      false,
			ExecutionPolicy: "agentic_delegation_observation",
			ParentTaskID:    input.ParentTaskID,
			ChildTaskID:     input.ChildTaskID,
			Total:           len(children),
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_delegate_status: marshal output: %v", err)), nil
	}
	return basetools.SuccessResult(string(raw)), nil
}

func (t *DelegateStatusTool) childStatus(ctx context.Context, edge agentic.TaskEdge, child *agentic.AgenticTask) (delegateStatusChildItem, error) {
	decisions, err := t.store.ListTaskEnqueueDecisionsByTask(ctx, child.ID)
	if err != nil {
		return delegateStatusChildItem{}, fmt.Errorf("agentic_delegate_status: list enqueue decisions for child %d: %v", child.ID, err)
	}
	receipts, err := t.store.ListToolActionReceiptsByTask(ctx, child.ID)
	if err != nil {
		return delegateStatusChildItem{}, fmt.Errorf("agentic_delegate_status: list receipts for child %d: %v", child.ID, err)
	}
	verifications, err := t.store.ListVerificationRunsByTask(ctx, child.ID)
	if err != nil {
		return delegateStatusChildItem{}, fmt.Errorf("agentic_delegate_status: list verification for child %d: %v", child.ID, err)
	}
	gates, err := t.store.ListCompletionGatesByTask(ctx, child.ID)
	if err != nil {
		return delegateStatusChildItem{}, fmt.Errorf("agentic_delegate_status: list completion gates for child %d: %v", child.ID, err)
	}

	item := delegateStatusChildItem{
		TaskID:                 child.ID,
		Title:                  child.Title,
		Status:                 child.Status,
		Priority:               child.Priority,
		RiskLevel:              child.RiskLevel,
		EdgeType:               edge.EdgeType,
		Enqueued:               child.QueueTaskID != 0,
		QueueTaskID:            child.QueueTaskID,
		LatestEnqueueDecision:  latestDelegateStatusDecision(decisions),
		Totals:                 delegateStatusEvidenceTotals{EnqueueDecisions: len(decisions), Receipts: len(receipts), VerificationRuns: len(verifications), CompletionGates: len(gates)},
		ReceiptStatusCounts:    countReceiptStatuses(receipts),
		VerificationVerdicts:   countVerificationVerdicts(verifications),
		CompletionGateStatuses: countCompletionGateStatuses(gates),
		LatestReceipt:          latestReceiptItem(receipts),
		LatestVerification:     latestVerificationItem(verifications),
		LatestCompletionGate:   latestCompletionGateItem(gates),
		CreatedAt:              formatEvidenceTime(child.CreatedAt),
		UpdatedAt:              formatEvidenceTime(child.UpdatedAt),
	}
	t.attachQueueStatus(ctx, &item)
	return item, nil
}

func (t *DelegateStatusTool) attachQueueStatus(ctx context.Context, item *delegateStatusChildItem) {
	if item == nil || item.QueueTaskID == 0 || t.queue == nil {
		return
	}
	task, err := t.queue.Get(ctx, item.QueueTaskID)
	if err != nil {
		item.QueueError = truncateEvidenceText(err.Error())
		return
	}
	item.QueueFound = true
	item.QueueStatus = string(task.Status)
	item.QueueProgress = truncateEvidenceText(task.Progress)
	item.QueueSummary = truncateEvidenceText(task.Summary)
	item.QueueUpdatedAt = formatEvidenceTime(task.UpdatedAt)
	if !task.CompletedAt.IsZero() {
		item.QueueCompletedAt = formatEvidenceTime(task.CompletedAt)
	}
}

func latestDelegateStatusDecision(decisions []agentic.TaskEnqueueDecision) *delegateStatusEnqueueDecisionItem {
	if len(decisions) == 0 {
		return nil
	}
	decision := decisions[len(decisions)-1]
	return &delegateStatusEnqueueDecisionItem{
		ID:                      decision.ID,
		QueueTaskID:             decision.QueueTaskID,
		Decision:                decision.Decision,
		Status:                  decision.Status,
		Reason:                  truncateEvidenceText(decision.Reason),
		RequestedEnforcement:    decision.RequestedEnforcement,
		RequestedCompletionGate: decision.RequestedCompletionGate,
		FailureReason:           truncateEvidenceText(decision.FailureReason),
		UpdatedAt:               formatEvidenceTime(decision.UpdatedAt),
	}
}
