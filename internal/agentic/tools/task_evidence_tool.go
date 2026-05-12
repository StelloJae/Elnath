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

const (
	TaskEvidenceToolName  = "agentic_task_evidence"
	taskEvidenceTextLimit = 180
)

type taskEvidenceStore interface {
	GetAgenticTask(context.Context, int64) (*agentic.AgenticTask, error)
	ListToolActionReceiptsByTask(context.Context, int64) ([]agentic.ToolActionReceipt, error)
	ListVerificationRunsByTask(context.Context, int64) ([]agentic.VerificationRun, error)
	ListCompletionGatesByTask(context.Context, int64) ([]agentic.CompletionGate, error)
	ListMemoryUpdatesByTask(context.Context, int64) ([]agentic.MemoryUpdate, error)
}

type TaskEvidenceTool struct {
	store taskEvidenceStore
}

func NewTaskEvidenceTool(store taskEvidenceStore) *TaskEvidenceTool {
	return &TaskEvidenceTool{store: store}
}

func (t *TaskEvidenceTool) Name() string { return TaskEvidenceToolName }

func (t *TaskEvidenceTool) Description() string {
	return "Summarize agentic task receipts, verification, completion gates, and memory update evidence"
}

func (t *TaskEvidenceTool) Schema() json.RawMessage {
	return basetools.Object(map[string]basetools.Property{
		"task_id": basetools.Int("Agentic task id to inspect."),
	}, []string{"task_id"})
}

func (t *TaskEvidenceTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TaskEvidenceTool) Reversible() bool { return true }

func (t *TaskEvidenceTool) Scope(json.RawMessage) basetools.ToolScope { return basetools.ToolScope{} }

func (t *TaskEvidenceTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *TaskEvidenceTool) DeferInitialToolSchema() bool { return true }

type taskEvidenceToolInput struct {
	TaskID int64 `json:"task_id"`
}

type taskEvidenceToolOutput struct {
	TaskID                 int64                           `json:"task_id"`
	TaskStatus             string                          `json:"task_status"`
	TaskTitle              string                          `json:"task_title,omitempty"`
	Totals                 taskEvidenceTotals              `json:"totals"`
	ReceiptStatusCounts    map[string]int                  `json:"receipt_status_counts"`
	VerificationVerdicts   map[string]int                  `json:"verification_verdicts"`
	CompletionGateStatuses map[string]int                  `json:"completion_gate_statuses"`
	MemoryUpdateStatuses   map[string]int                  `json:"memory_update_statuses"`
	LatestReceipt          *taskEvidenceReceiptItem        `json:"latest_receipt,omitempty"`
	LatestVerification     *taskEvidenceVerificationItem   `json:"latest_verification,omitempty"`
	LatestCompletionGate   *taskEvidenceCompletionGateItem `json:"latest_completion_gate,omitempty"`
	LatestMemoryUpdate     *taskEvidenceMemoryUpdateItem   `json:"latest_memory_update,omitempty"`
}

type taskEvidenceTotals struct {
	Receipts         int `json:"receipts"`
	VerificationRuns int `json:"verification_runs"`
	CompletionGates  int `json:"completion_gates"`
	MemoryUpdates    int `json:"memory_updates"`
}

type taskEvidenceReceiptItem struct {
	ID            int64  `json:"id"`
	ActorID       int64  `json:"actor_id,omitempty"`
	ToolName      string `json:"tool_name"`
	ToolCallID    string `json:"tool_call_id,omitempty"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason,omitempty"`
	OutputSummary string `json:"output_summary,omitempty"`
	Reversible    bool   `json:"reversible"`
	StartedAt     string `json:"started_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
}

type taskEvidenceVerificationItem struct {
	ID              int64  `json:"id"`
	VerifierActorID int64  `json:"verifier_actor_id,omitempty"`
	Verdict         string `json:"verdict"`
	Reason          string `json:"reason,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
}

type taskEvidenceCompletionGateItem struct {
	ID                int64  `json:"id"`
	QueueTaskID       int64  `json:"queue_task_id,omitempty"`
	VerificationRunID int64  `json:"verification_run_id,omitempty"`
	Status            string `json:"status"`
	Reason            string `json:"reason,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

type taskEvidenceMemoryUpdateItem struct {
	ID                int64  `json:"id"`
	ReceiptID         int64  `json:"receipt_id,omitempty"`
	VerificationRunID int64  `json:"verification_run_id,omitempty"`
	Target            string `json:"target"`
	Operation         string `json:"operation"`
	Status            string `json:"status"`
	Source            string `json:"source,omitempty"`
	Reason            string `json:"reason,omitempty"`
	AppliedAt         string `json:"applied_at,omitempty"`
}

func (t *TaskEvidenceTool) Execute(ctx context.Context, params json.RawMessage) (*basetools.Result, error) {
	if t == nil || t.store == nil {
		return basetools.ErrorResult("agentic_task_evidence: store unavailable"), nil
	}
	var input taskEvidenceToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return basetools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.TaskID == 0 {
		return basetools.ErrorResult("agentic_task_evidence: task_id is required"), nil
	}

	task, err := t.store.GetAgenticTask(ctx, input.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return basetools.ErrorResult(fmt.Sprintf("agentic_task_evidence: task %d not found", input.TaskID)), nil
		}
		return basetools.ErrorResult(fmt.Sprintf("agentic_task_evidence: task %d: %v", input.TaskID, err)), nil
	}
	receipts, err := t.store.ListToolActionReceiptsByTask(ctx, input.TaskID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_task_evidence: list receipts: %v", err)), nil
	}
	verifications, err := t.store.ListVerificationRunsByTask(ctx, input.TaskID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_task_evidence: list verification: %v", err)), nil
	}
	gates, err := t.store.ListCompletionGatesByTask(ctx, input.TaskID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_task_evidence: list completion gates: %v", err)), nil
	}
	updates, err := t.store.ListMemoryUpdatesByTask(ctx, input.TaskID)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_task_evidence: list memory updates: %v", err)), nil
	}

	output := taskEvidenceToolOutput{
		TaskID:     task.ID,
		TaskStatus: task.Status,
		TaskTitle:  task.Title,
		Totals: taskEvidenceTotals{
			Receipts:         len(receipts),
			VerificationRuns: len(verifications),
			CompletionGates:  len(gates),
			MemoryUpdates:    len(updates),
		},
		ReceiptStatusCounts:    countReceiptStatuses(receipts),
		VerificationVerdicts:   countVerificationVerdicts(verifications),
		CompletionGateStatuses: countCompletionGateStatuses(gates),
		MemoryUpdateStatuses:   countMemoryUpdateStatuses(updates),
		LatestReceipt:          latestReceiptItem(receipts),
		LatestVerification:     latestVerificationItem(verifications),
		LatestCompletionGate:   latestCompletionGateItem(gates),
		LatestMemoryUpdate:     latestMemoryUpdateItem(updates),
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return basetools.ErrorResult(fmt.Sprintf("agentic_task_evidence: marshal output: %v", err)), nil
	}
	return basetools.SuccessResult(string(raw)), nil
}

func countReceiptStatuses(receipts []agentic.ToolActionReceipt) map[string]int {
	counts := make(map[string]int)
	for _, receipt := range receipts {
		counts[receipt.Status]++
	}
	return counts
}

func countVerificationVerdicts(runs []agentic.VerificationRun) map[string]int {
	counts := make(map[string]int)
	for _, run := range runs {
		counts[run.Verdict]++
	}
	return counts
}

func countCompletionGateStatuses(gates []agentic.CompletionGate) map[string]int {
	counts := make(map[string]int)
	for _, gate := range gates {
		counts[gate.Status]++
	}
	return counts
}

func countMemoryUpdateStatuses(updates []agentic.MemoryUpdate) map[string]int {
	counts := make(map[string]int)
	for _, update := range updates {
		counts[update.Status]++
	}
	return counts
}

func latestReceiptItem(receipts []agentic.ToolActionReceipt) *taskEvidenceReceiptItem {
	if len(receipts) == 0 {
		return nil
	}
	receipt := receipts[len(receipts)-1]
	completedAt := ""
	if receipt.CompletedAt.Valid {
		completedAt = formatEvidenceTime(receipt.CompletedAt.Time)
	}
	return &taskEvidenceReceiptItem{
		ID:            receipt.ID,
		ActorID:       receipt.ActorID,
		ToolName:      receipt.ToolName,
		ToolCallID:    receipt.ToolCallID,
		Status:        receipt.Status,
		FailureReason: truncateEvidenceText(receipt.FailureReason),
		OutputSummary: truncateEvidenceText(receipt.OutputSummary),
		Reversible:    receipt.Reversible,
		StartedAt:     formatEvidenceTime(receipt.StartedAt),
		CompletedAt:   completedAt,
	}
}

func latestVerificationItem(runs []agentic.VerificationRun) *taskEvidenceVerificationItem {
	if len(runs) == 0 {
		return nil
	}
	run := runs[len(runs)-1]
	return &taskEvidenceVerificationItem{
		ID:              run.ID,
		VerifierActorID: run.VerifierActorID,
		Verdict:         run.Verdict,
		Reason:          truncateEvidenceText(run.Reason),
		CreatedAt:       formatEvidenceTime(run.CreatedAt),
	}
}

func latestCompletionGateItem(gates []agentic.CompletionGate) *taskEvidenceCompletionGateItem {
	if len(gates) == 0 {
		return nil
	}
	gate := gates[len(gates)-1]
	return &taskEvidenceCompletionGateItem{
		ID:                gate.ID,
		QueueTaskID:       gate.QueueTaskID,
		VerificationRunID: gate.VerificationRunID,
		Status:            gate.Status,
		Reason:            truncateEvidenceText(gate.Reason),
		UpdatedAt:         formatEvidenceTime(gate.UpdatedAt),
	}
}

func latestMemoryUpdateItem(updates []agentic.MemoryUpdate) *taskEvidenceMemoryUpdateItem {
	if len(updates) == 0 {
		return nil
	}
	update := updates[len(updates)-1]
	appliedAt := ""
	if update.AppliedAt.Valid {
		appliedAt = formatEvidenceTime(update.AppliedAt.Time)
	}
	return &taskEvidenceMemoryUpdateItem{
		ID:                update.ID,
		ReceiptID:         update.ReceiptID,
		VerificationRunID: update.VerificationRunID,
		Target:            update.Target,
		Operation:         update.Operation,
		Status:            update.Status,
		Source:            update.Source,
		Reason:            truncateEvidenceText(update.Reason),
		AppliedAt:         appliedAt,
	}
}

func truncateEvidenceText(s string) string {
	rs := []rune(s)
	if len(rs) <= taskEvidenceTextLimit {
		return s
	}
	return string(rs[:taskEvidenceTextLimit])
}

func formatEvidenceTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
