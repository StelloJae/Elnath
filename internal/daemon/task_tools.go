package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/tools"
)

const (
	TaskCreateToolName            = "task_create"
	TaskListToolName              = "task_list"
	TaskGetToolName               = "task_get"
	TaskStopToolName              = "task_stop"
	TaskOutputToolName            = "task_output"
	TaskMonitorToolName           = "task_monitor"
	TaskUpdateToolName            = "task_update"
	defaultTaskListLimit          = 20
	maxTaskListLimit              = 100
	taskToolPreviewMaxRune        = 240
	defaultTaskOutputRunes        = 4000
	maxTaskOutputRunes            = 20000
	defaultTaskOutputTimeout      = 30 * time.Second
	maxTaskOutputTimeout          = 10 * time.Minute
	taskOutputPollInterval        = 100 * time.Millisecond
	defaultTaskMonitorPollSeconds = 5
)

const (
	taskOutputRetrievalSuccess  = "success"
	taskOutputRetrievalNotReady = "not_ready"
	taskOutputRetrievalTimeout  = "timeout"
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

func (t *TaskCreateTool) DeferInitialToolSchema() bool { return true }

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

func (t *TaskListTool) DeferInitialToolSchema() bool { return true }

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

func (t *TaskGetTool) DeferInitialToolSchema() bool { return true }

type taskGetToolInput struct {
	ID int64 `json:"id"`
}

type taskGetToolOutput struct {
	Found bool            `json:"found"`
	Task  *taskToolDetail `json:"task"`
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
		if errors.Is(err, core.ErrNotFound) {
			raw, marshalErr := json.Marshal(taskGetToolOutput{})
			if marshalErr != nil {
				return tools.ErrorResult(fmt.Sprintf("task_get: marshal output: %v", marshalErr)), nil
			}
			return tools.SuccessResult(string(raw)), nil
		}
		return tools.ErrorResult(fmt.Sprintf("task_get: %v", err)), nil
	}
	item := taskToolItemFromTask(*task)
	output := taskGetToolOutput{
		Found: true,
		Task: &taskToolDetail{
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

type RunningTaskCanceller interface {
	CancelRunningTask(id int64, reason string) (bool, error)
}

type TaskStopTool struct {
	queue            *Queue
	runningCanceller RunningTaskCanceller
}

func NewTaskStopTool(queue *Queue) *TaskStopTool {
	return &TaskStopTool{queue: queue}
}

func (t *TaskStopTool) WithRunningCanceller(canceller RunningTaskCanceller) *TaskStopTool {
	t.runningCanceller = canceller
	return t
}

func (t *TaskStopTool) Name() string { return TaskStopToolName }

func (t *TaskStopTool) Description() string {
	return "Stop a pending or running daemon queue task by ID"
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

func (t *TaskStopTool) DeferInitialToolSchema() bool { return true }

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
	output := taskStopToolOutput{
		TaskID:         input.ID,
		Stopped:        true,
		PreviousStatus: task.Status,
		Reason:         reason,
	}
	switch task.Status {
	case StatusPending:
		if err := t.queue.CancelTask(ctx, input.ID, reason); err != nil {
			return tools.ErrorResult(fmt.Sprintf("task_stop: %v", err)), nil
		}
		output.Status = StatusFailed
	case StatusRunning:
		if t.runningCanceller == nil {
			return tools.ErrorResult("task_stop: running task cancellation unavailable"), nil
		}
		stopped, err := t.runningCanceller.CancelRunningTask(input.ID, reason)
		if err != nil {
			return tools.ErrorResult(fmt.Sprintf("task_stop: %v", err)), nil
		}
		if !stopped {
			return tools.ErrorResult(fmt.Sprintf("task_stop: task %d is running but no active runner was found", input.ID)), nil
		}
		output.Status = StatusRunning
	default:
		return tools.ErrorResult(fmt.Sprintf("task_stop: task %d is %s; only pending or running tasks can be stopped", input.ID, task.Status)), nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_stop: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

type TaskOutputTool struct {
	queue *Queue
}

func NewTaskOutputTool(queue *Queue) *TaskOutputTool {
	return &TaskOutputTool{queue: queue}
}

func (t *TaskOutputTool) Name() string { return TaskOutputToolName }

func (t *TaskOutputTool) Description() string {
	return "Read a bounded tail of a daemon task output field"
}

func (t *TaskOutputTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"id":        tools.Int("Daemon task ID."),
		"field":     tools.StringEnum("Output field to read. Defaults to result.", "result", "progress", "summary", "payload"),
		"max_chars": tools.Int("Maximum trailing characters to return. Defaults to 4000 and caps at 20000."),
		"block":     tools.Bool("When true, wait until the task reaches a terminal status before returning or until timeout_ms expires."),
		"timeout_ms": tools.Int(
			"Maximum wait time in milliseconds when block is true. Defaults to 30000 and caps at 600000.",
		),
	}, []string{"id"})
}

func (t *TaskOutputTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TaskOutputTool) Reversible() bool { return true }

func (t *TaskOutputTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *TaskOutputTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *TaskOutputTool) DeferInitialToolSchema() bool { return true }

type taskOutputToolInput struct {
	ID        int64  `json:"id"`
	Field     string `json:"field"`
	MaxChars  int    `json:"max_chars"`
	Block     bool   `json:"block"`
	TimeoutMS int    `json:"timeout_ms"`
}

type taskOutputToolOutput struct {
	TaskID          int64      `json:"task_id"`
	Status          TaskStatus `json:"status"`
	RetrievalStatus string     `json:"retrieval_status"`
	Terminal        bool       `json:"terminal"`
	Field           string     `json:"field"`
	MaxChars        int        `json:"max_chars"`
	TotalChars      int        `json:"total_chars"`
	Truncated       bool       `json:"truncated"`
	Content         string     `json:"content"`
}

func (t *TaskOutputTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.queue == nil {
		return tools.ErrorResult("task_output: queue unavailable"), nil
	}
	var input taskOutputToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ID <= 0 {
		return tools.ErrorResult("task_output: id must be positive"), nil
	}
	field, ok := normalizeTaskOutputField(input.Field)
	if !ok {
		return tools.ErrorResult("task_output: field must be result, progress, summary, or payload"), nil
	}
	limit := normalizeTaskOutputLimit(input.MaxChars)

	task, err := t.queue.Get(ctx, input.ID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_output: %v", err)), nil
	}
	retrievalStatus := taskOutputRetrievalSuccess
	terminal := isTerminalTaskStatus(task.Status)
	if input.Block && !terminal {
		task, retrievalStatus, err = t.waitForTerminalTask(ctx, input.ID, normalizeTaskOutputTimeout(input.TimeoutMS))
		if err != nil {
			return tools.ErrorResult(fmt.Sprintf("task_output: %v", err)), nil
		}
		terminal = isTerminalTaskStatus(task.Status)
	} else if !terminal {
		retrievalStatus = taskOutputRetrievalNotReady
	}
	content := taskOutputField(task, field)
	tail, total, truncated := tailTaskOutput(content, limit)
	output := taskOutputToolOutput{
		TaskID:          task.ID,
		Status:          task.Status,
		RetrievalStatus: retrievalStatus,
		Terminal:        terminal,
		Field:           field,
		MaxChars:        limit,
		TotalChars:      total,
		Truncated:       truncated,
		Content:         tail,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_output: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

func (t *TaskOutputTool) waitForTerminalTask(ctx context.Context, id int64, timeout time.Duration) (*Task, string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(taskOutputPollInterval)
	defer ticker.Stop()

	var last *Task
	for {
		task, err := t.queue.Get(ctx, id)
		if err != nil {
			return nil, "", err
		}
		last = task
		if isTerminalTaskStatus(task.Status) {
			return task, taskOutputRetrievalSuccess, nil
		}

		select {
		case <-ctx.Done():
			return last, "", ctx.Err()
		case <-deadline.C:
			return last, taskOutputRetrievalTimeout, nil
		case <-ticker.C:
		}
	}
}

type TaskMonitorTool struct {
	queue *Queue
}

func NewTaskMonitorTool(queue *Queue) *TaskMonitorTool {
	return &TaskMonitorTool{queue: queue}
}

func (t *TaskMonitorTool) Name() string { return TaskMonitorToolName }

func (t *TaskMonitorTool) Description() string {
	return "Monitor a daemon queue task with status, progress, summary, and result tail"
}

func (t *TaskMonitorTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"id":        tools.Int("Daemon task ID."),
		"max_chars": tools.Int("Maximum trailing result characters to return. Defaults to 4000 and caps at 20000."),
	}, []string{"id"})
}

func (t *TaskMonitorTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TaskMonitorTool) Reversible() bool { return true }

func (t *TaskMonitorTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *TaskMonitorTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *TaskMonitorTool) DeferInitialToolSchema() bool { return true }

type taskMonitorToolInput struct {
	ID       int64 `json:"id"`
	MaxChars int   `json:"max_chars"`
}

type taskMonitorToolOutput struct {
	TaskID           int64      `json:"task_id"`
	Status           TaskStatus `json:"status"`
	Terminal         bool       `json:"terminal"`
	NextPollSeconds  int        `json:"next_poll_seconds"`
	Progress         string     `json:"progress,omitempty"`
	Summary          string     `json:"summary,omitempty"`
	ResultTail       string     `json:"result_tail,omitempty"`
	ResultTotalChars int        `json:"result_total_chars"`
	ResultTruncated  bool       `json:"result_truncated"`
	UpdatedAt        string     `json:"updated_at,omitempty"`
	CompletedAt      string     `json:"completed_at,omitempty"`
}

func (t *TaskMonitorTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.queue == nil {
		return tools.ErrorResult("task_monitor: queue unavailable"), nil
	}
	var input taskMonitorToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ID <= 0 {
		return tools.ErrorResult("task_monitor: id must be positive"), nil
	}

	task, err := t.queue.Get(ctx, input.ID)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_monitor: %v", err)), nil
	}
	limit := normalizeTaskOutputLimit(input.MaxChars)
	tail, total, truncated := tailTaskOutput(task.Result, limit)
	terminal := isTerminalTaskStatus(task.Status)
	nextPollSeconds := defaultTaskMonitorPollSeconds
	if terminal {
		nextPollSeconds = 0
	}

	output := taskMonitorToolOutput{
		TaskID:           task.ID,
		Status:           task.Status,
		Terminal:         terminal,
		NextPollSeconds:  nextPollSeconds,
		Progress:         task.Progress,
		Summary:          task.Summary,
		ResultTail:       tail,
		ResultTotalChars: total,
		ResultTruncated:  truncated,
		UpdatedAt:        formatTaskToolTime(task.UpdatedAt),
		CompletedAt:      formatTaskToolTime(task.CompletedAt),
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_monitor: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}

type TaskUpdateTool struct {
	queue *Queue
}

func NewTaskUpdateTool(queue *Queue) *TaskUpdateTool {
	return &TaskUpdateTool{queue: queue}
}

func (t *TaskUpdateTool) Name() string { return TaskUpdateToolName }

func (t *TaskUpdateTool) Description() string {
	return "Update progress or summary annotations for a pending or running daemon task"
}

func (t *TaskUpdateTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"id":       tools.Int("Daemon task ID."),
		"progress": tools.String("Optional progress annotation."),
		"summary":  tools.String("Optional summary annotation."),
	}, []string{"id"})
}

func (t *TaskUpdateTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *TaskUpdateTool) Reversible() bool { return false }

func (t *TaskUpdateTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *TaskUpdateTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *TaskUpdateTool) DeferInitialToolSchema() bool { return true }

type taskUpdateToolInput struct {
	ID       int64  `json:"id"`
	Progress string `json:"progress"`
	Summary  string `json:"summary"`
}

type taskUpdateToolOutput struct {
	TaskID   int64      `json:"task_id"`
	Status   TaskStatus `json:"status"`
	Progress string     `json:"progress,omitempty"`
	Summary  string     `json:"summary,omitempty"`
	Updated  bool       `json:"updated"`
}

func (t *TaskUpdateTool) Execute(ctx context.Context, params json.RawMessage) (*tools.Result, error) {
	if t == nil || t.queue == nil {
		return tools.ErrorResult("task_update: queue unavailable"), nil
	}
	var input taskUpdateToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	if input.ID <= 0 {
		return tools.ErrorResult("task_update: id must be positive"), nil
	}
	task, err := t.queue.UpdateAnnotation(ctx, input.ID, input.Progress, input.Summary)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_update: %v", err)), nil
	}
	output := taskUpdateToolOutput{
		TaskID:   task.ID,
		Status:   task.Status,
		Progress: task.Progress,
		Summary:  task.Summary,
		Updated:  true,
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("task_update: marshal output: %v", err)), nil
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

func normalizeTaskOutputField(field string) (string, bool) {
	field = strings.ToLower(strings.TrimSpace(field))
	switch field {
	case "":
		return "result", true
	case "result", "progress", "summary", "payload":
		return field, true
	default:
		return "", false
	}
}

func normalizeTaskOutputLimit(limit int) int {
	if limit <= 0 {
		return defaultTaskOutputRunes
	}
	if limit > maxTaskOutputRunes {
		return maxTaskOutputRunes
	}
	return limit
}

func normalizeTaskOutputTimeout(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		return defaultTaskOutputTimeout
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout > maxTaskOutputTimeout {
		return maxTaskOutputTimeout
	}
	return timeout
}

func taskOutputField(task *Task, field string) string {
	if task == nil {
		return ""
	}
	switch field {
	case "progress":
		return task.Progress
	case "summary":
		return task.Summary
	case "payload":
		return task.Payload
	default:
		return task.Result
	}
}

func isTerminalTaskStatus(status TaskStatus) bool {
	return status == StatusDone || status == StatusFailed
}

func tailTaskOutput(s string, maxRunes int) (string, int, bool) {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	total := len(runes)
	if maxRunes <= 0 || total <= maxRunes {
		return s, total, false
	}
	return string(runes[total-maxRunes:]), total, true
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
