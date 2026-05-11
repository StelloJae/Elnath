package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/tools"
)

const (
	TaskCreateToolName     = "task_create"
	TaskListToolName       = "task_list"
	TaskGetToolName        = "task_get"
	TaskStopToolName       = "task_stop"
	defaultTaskListLimit   = 20
	maxTaskListLimit       = 100
	taskToolPreviewMaxRune = 240
)

type TaskCreateTool struct {
	queue *Queue
}

func NewTaskCreateTool(queue *Queue) *TaskCreateTool {
	return &TaskCreateTool{queue: queue}
}

func (t *TaskCreateTool) Name() string { return TaskCreateToolName }

func (t *TaskCreateTool) Description() string {
	return "Create a pending daemon queue task for background execution"
}

func (t *TaskCreateTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"prompt":                  tools.String("Task prompt to enqueue."),
		"session_id":              tools.String("Optional session id to continue."),
		"surface":                 tools.String("Optional originating surface label."),
		"idempotency_key":         tools.String("Optional key used to deduplicate active pending/running tasks."),
		"agentic_enforcement":     tools.String("Optional explicit agentic enforcement mode."),
		"agentic_completion_gate": tools.String("Optional explicit completion gate mode."),
	}, []string{"prompt"})
}

func (t *TaskCreateTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *TaskCreateTool) Reversible() bool { return false }

func (t *TaskCreateTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *TaskCreateTool) ShouldCancelSiblingsOnError() bool { return false }

type taskCreateToolInput struct {
	Prompt                string `json:"prompt"`
	SessionID             string `json:"session_id"`
	Surface               string `json:"surface"`
	IdempotencyKey        string `json:"idempotency_key"`
	AgenticEnforcement    string `json:"agentic_enforcement"`
	AgenticCompletionGate string `json:"agentic_completion_gate"`
}

type taskCreateToolOutput struct {
	TaskID         int64  `json:"task_id"`
	Status         string `json:"status"`
	Deduplicated   bool   `json:"deduplicated"`
	SessionID      string `json:"session_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	PayloadPreview string `json:"payload_preview"`
}

func (t *TaskCreateTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.queue == nil {
		return tools.ErrorResult("task_create: queue unavailable"), nil
	}
	var input taskCreateToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return tools.ErrorResult("task_create: prompt is required"), nil
	}

	payload := EncodeTaskPayload(TaskPayload{
		Prompt:                prompt,
		SessionID:             input.SessionID,
		Surface:               input.Surface,
		AgenticEnforcement:    input.AgenticEnforcement,
		AgenticCompletionGate: input.AgenticCompletionGate,
	})
	id, deduped, err := t.queue.Enqueue(ctx, payload, input.IdempotencyKey)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_create: %v", err)), nil
	}

	output := taskCreateToolOutput{
		TaskID:         id,
		Status:         string(StatusPending),
		Deduplicated:   deduped,
		SessionID:      strings.TrimSpace(input.SessionID),
		IdempotencyKey: strings.TrimSpace(input.IdempotencyKey),
		PayloadPreview: truncateTaskToolText(payload, taskToolPreviewMaxRune),
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_create: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

type TaskListTool struct {
	queue *Queue
}

func NewTaskListTool(queue *Queue) *TaskListTool {
	return &TaskListTool{queue: queue}
}

func (t *TaskListTool) Name() string { return TaskListToolName }

func (t *TaskListTool) Description() string {
	return "List recent daemon queue tasks with structured statuses"
}

func (t *TaskListTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"limit":  tools.Int("Maximum tasks to return. Defaults to 20 and caps at 100."),
		"status": tools.StringEnum("Optional status filter.", string(StatusPending), string(StatusRunning), string(StatusDone), string(StatusFailed)),
	}, nil)
}

func (t *TaskListTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TaskListTool) Reversible() bool { return true }

func (t *TaskListTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *TaskListTool) ShouldCancelSiblingsOnError() bool { return false }

type taskListToolInput struct {
	Limit  int    `json:"limit"`
	Status string `json:"status"`
}

type taskListToolOutput struct {
	TotalReturned int            `json:"total_returned"`
	Limit         int            `json:"limit"`
	Status        string         `json:"status,omitempty"`
	Tasks         []taskToolItem `json:"tasks"`
}

type taskToolItem struct {
	ID                 int64      `json:"id"`
	Status             TaskStatus `json:"status"`
	SessionID          string     `json:"session_id,omitempty"`
	PayloadPreview     string     `json:"payload_preview,omitempty"`
	Progress           string     `json:"progress,omitempty"`
	Summary            string     `json:"summary,omitempty"`
	ResultPreview      string     `json:"result_preview,omitempty"`
	TimeoutClass       string     `json:"timeout_class,omitempty"`
	IdleTimeoutCount   int        `json:"idle_timeout_count,omitempty"`
	ActiveTimeoutCount int        `json:"active_timeout_count,omitempty"`
	CreatedAt          string     `json:"created_at,omitempty"`
	UpdatedAt          string     `json:"updated_at,omitempty"`
	StartedAt          string     `json:"started_at,omitempty"`
	CompletedAt        string     `json:"completed_at,omitempty"`
}

func (t *TaskListTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.queue == nil {
		return tools.ErrorResult("task_list: queue unavailable"), nil
	}
	var input taskListToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}

	limit := normalizeTaskListLimit(input.Limit)
	status, ok := normalizeTaskToolStatus(input.Status)
	if !ok {
		return tools.ErrorResult("task_list: status must be pending, running, done, or failed"), nil
	}

	tasks, err := t.queue.List(ctx)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_list: %v", err)), nil
	}

	items := make([]taskToolItem, 0, minInt(limit, len(tasks)))
	for _, task := range tasks {
		if status != "" && task.Status != status {
			continue
		}
		items = append(items, taskToolItemFromTask(task))
		if len(items) >= limit {
			break
		}
	}

	output := taskListToolOutput{
		TotalReturned: len(items),
		Limit:         limit,
		Status:        string(status),
		Tasks:         items,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_list: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

type TaskGetTool struct {
	queue *Queue
}

func NewTaskGetTool(queue *Queue) *TaskGetTool {
	return &TaskGetTool{queue: queue}
}

func (t *TaskGetTool) Name() string { return TaskGetToolName }

func (t *TaskGetTool) Description() string {
	return "Get one daemon queue task by ID"
}

func (t *TaskGetTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"id": tools.Int("Daemon task ID."),
	}, []string{"id"})
}

func (t *TaskGetTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TaskGetTool) Reversible() bool { return true }

func (t *TaskGetTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *TaskGetTool) ShouldCancelSiblingsOnError() bool { return false }

type taskGetToolInput struct {
	ID int64 `json:"id"`
}

type taskGetToolOutput struct {
	Task taskToolDetail `json:"task"`
}

type taskToolDetail struct {
	taskToolItem
	Payload string `json:"payload,omitempty"`
	Result  string `json:"result,omitempty"`
}

func (t *TaskGetTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.queue == nil {
		return tools.ErrorResult("task_get: queue unavailable"), nil
	}
	var input taskGetToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ID <= 0 {
		return tools.ErrorResult("task_get: id must be positive"), nil
	}

	task, err := t.queue.Get(ctx, input.ID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_get: %v", err)), nil
	}
	item := taskToolItemFromTask(*task)
	output := taskGetToolOutput{
		Task: taskToolDetail{
			taskToolItem: item,
			Payload:      task.Payload,
			Result:       task.Result,
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_get: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

type TaskStopTool struct {
	queue *Queue
}

func NewTaskStopTool(queue *Queue) *TaskStopTool {
	return &TaskStopTool{queue: queue}
}

func (t *TaskStopTool) Name() string { return TaskStopToolName }

func (t *TaskStopTool) Description() string {
	return "Stop a pending daemon queue task by ID"
}

func (t *TaskStopTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"id":     tools.Int("Daemon task ID."),
		"reason": tools.String("Optional cancellation reason."),
	}, []string{"id"})
}

func (t *TaskStopTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *TaskStopTool) Reversible() bool { return false }

func (t *TaskStopTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *TaskStopTool) ShouldCancelSiblingsOnError() bool { return false }

type taskStopToolInput struct {
	ID     int64  `json:"id"`
	Reason string `json:"reason"`
}

type taskStopToolOutput struct {
	TaskID         int64      `json:"task_id"`
	Stopped        bool       `json:"stopped"`
	PreviousStatus TaskStatus `json:"previous_status"`
	Status         TaskStatus `json:"status"`
	Reason         string     `json:"reason"`
}

func (t *TaskStopTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.queue == nil {
		return tools.ErrorResult("task_stop: queue unavailable"), nil
	}
	var input taskStopToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ID <= 0 {
		return tools.ErrorResult("task_stop: id must be positive"), nil
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "task_stop requested"
	}

	task, err := t.queue.Get(ctx, input.ID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_stop: %v", err)), nil
	}
	if task.Status != StatusPending {
		return tools.ErrorResult(fmt.Sprintf("task_stop: task %d is %s; only pending tasks can be stopped", input.ID, task.Status)), nil
	}
	if err := t.queue.CancelTask(ctx, input.ID, reason); err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_stop: %v", err)), nil
	}

	output := taskStopToolOutput{
		TaskID:         input.ID,
		Stopped:        true,
		PreviousStatus: task.Status,
		Status:         StatusFailed,
		Reason:         reason,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_stop: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func normalizeTaskListLimit(limit int) int {
	if limit <= 0 {
		return defaultTaskListLimit
	}
	if limit > maxTaskListLimit {
		return maxTaskListLimit
	}
	return limit
}

func normalizeTaskToolStatus(status string) (TaskStatus, bool) {
	status = strings.ToLower(strings.TrimSpace(status))
	switch TaskStatus(status) {
	case "", StatusPending, StatusRunning, StatusDone, StatusFailed:
		return TaskStatus(status), true
	default:
		return "", false
	}
}

func taskToolItemFromTask(task Task) taskToolItem {
	return taskToolItem{
		ID:                 task.ID,
		Status:             task.Status,
		SessionID:          task.SessionID,
		PayloadPreview:     truncateTaskToolText(task.Payload, taskToolPreviewMaxRune),
		Progress:           task.Progress,
		Summary:            task.Summary,
		ResultPreview:      truncateTaskToolText(task.Result, taskToolPreviewMaxRune),
		TimeoutClass:       string(task.TimeoutClass),
		IdleTimeoutCount:   task.IdleTimeoutCount,
		ActiveTimeoutCount: task.ActiveTimeoutCount,
		CreatedAt:          formatTaskToolTime(task.CreatedAt),
		UpdatedAt:          formatTaskToolTime(task.UpdatedAt),
		StartedAt:          formatTaskToolTime(task.StartedAt),
		CompletedAt:        formatTaskToolTime(task.CompletedAt),
	}
}

func formatTaskToolTime(t time.Time) string {
	if t.IsZero() || t.UnixMilli() == 0 {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func truncateTaskToolText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
