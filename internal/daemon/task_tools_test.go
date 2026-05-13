package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/tools"
)

func TestTaskCreateToolEnqueuesPendingTask(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)

	result, err := NewTaskCreateTool(queue).Execute(ctx, json.RawMessage(`{
		"prompt": "continue the reference lane",
		"session_id": "sess-123",
		"surface": "tool-test",
		"idempotency_key": "task-create-1",
		"agentic_enforcement": "observe",
		"agentic_completion_gate": "verification"
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskCreateToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.TaskID == 0 {
		t.Fatal("TaskID = 0, want created task id")
	}
	if output.Status != string(StatusPending) {
		t.Fatalf("Status = %q, want pending", output.Status)
	}
	if output.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q, want sess-123", output.SessionID)
	}
	if output.Receipt.Tool != TaskCreateToolName || output.Receipt.Action != "create" || output.Receipt.ReadOnly || !output.Receipt.Persistent || !output.Receipt.QueueBacked || output.Receipt.TaskID != output.TaskID || output.Receipt.FollowupTool != TaskMonitorToolName {
		t.Fatalf("receipt = %+v, want task_create queue-backed mutation receipt", output.Receipt)
	}
	if output.Receipt.SessionID != "sess-123" {
		t.Fatalf("receipt SessionID = %q, want sess-123", output.Receipt.SessionID)
	}

	task, err := queue.Get(ctx, output.TaskID)
	if err != nil {
		t.Fatalf("Get created task: %v", err)
	}
	payload := ParseTaskPayload(task.Payload)
	if payload.Prompt != "continue the reference lane" || payload.SessionID != "sess-123" || payload.Surface != "tool-test" {
		t.Fatalf("payload = %+v, want normalized task payload", payload)
	}
	if payload.AgenticEnforcement != "observe" || payload.AgenticCompletionGate != "verification" {
		t.Fatalf("payload gates = (%q,%q), want observe/verification", payload.AgenticEnforcement, payload.AgenticCompletionGate)
	}
}

func TestUserQuestionAnswerToolEnqueuesSessionBoundFollowUp(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)

	result, err := NewUserQuestionAnswerTool(queue).Execute(ctx, json.RawMessage(`{
		"session_id": "sess-123",
		"request_id": "req-123",
		"question": "Which branch?",
		"answer": "Use main.",
		"surface": "tool-test",
		"idempotency_key": "answer-1"
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output userQuestionAnswerToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Type != "user_input_answer_enqueued" || output.TaskID == 0 || output.Status != string(StatusPending) || output.SessionID != "sess-123" || output.RequestID != "req-123" {
		t.Fatalf("output = %+v, want pending session-bound answer task", output)
	}
	if output.AnswerChars != len("Use main.") {
		t.Fatalf("AnswerChars = %d, want %d", output.AnswerChars, len("Use main."))
	}
	if output.Receipt.Tool != UserQuestionAnswerToolName || output.Receipt.Action != "answer" || output.Receipt.ReadOnly || !output.Receipt.Persistent || !output.Receipt.QueueBacked || output.Receipt.TaskID != output.TaskID || output.Receipt.RequestID != "req-123" || output.Receipt.SessionID != "sess-123" || output.Receipt.FollowupTool != TaskMonitorToolName {
		t.Fatalf("receipt = %+v, want user answer queue-backed mutation receipt", output.Receipt)
	}
	if output.Receipt.ExecutionPolicy != "daemon_queue_user_answer_resume" || output.Receipt.QuestionChars != len("Which branch?") {
		t.Fatalf("receipt policy/bounds = %+v", output.Receipt)
	}

	task, err := queue.Get(ctx, output.TaskID)
	if err != nil {
		t.Fatalf("Get created task: %v", err)
	}
	payload := ParseTaskPayload(task.Payload)
	if payload.SessionID != "sess-123" || payload.Surface != "tool-test" {
		t.Fatalf("payload routing = %+v, want session-bound tool-test follow-up", payload)
	}
	if !strings.Contains(payload.Prompt, "Request ID:\nreq-123") || !strings.Contains(payload.Prompt, "Question:\nWhich branch?") || !strings.Contains(payload.Prompt, "Answer:\nUse main.") || !strings.Contains(payload.Prompt, "Continue the existing session") {
		t.Fatalf("payload prompt = %q, want question, answer, and continuation instruction", payload.Prompt)
	}
}

func TestUserQuestionAnswerToolRejectsMissingRequiredFields(t *testing.T) {
	tool := NewUserQuestionAnswerTool(newTaskToolTestQueue(t))

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"answer":"main"}`))
	if err != nil {
		t.Fatalf("missing session Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "session_id is required") {
		t.Fatalf("missing session result = %+v, want session_id error", result)
	}

	result, err = tool.Execute(context.Background(), json.RawMessage(`{"session_id":"sess-123","answer":" "}`))
	if err != nil {
		t.Fatalf("missing answer Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "answer is required") {
		t.Fatalf("missing answer result = %+v, want answer error", result)
	}
}

func TestTaskCreateToolDeduplicatesActiveTask(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	params := json.RawMessage(`{"prompt":"same task","idempotency_key":"same-key"}`)

	first, err := NewTaskCreateTool(queue).Execute(ctx, params)
	if err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	second, err := NewTaskCreateTool(queue).Execute(ctx, params)
	if err != nil {
		t.Fatalf("second Execute error = %v", err)
	}

	var firstOutput, secondOutput taskCreateToolOutput
	if err := json.Unmarshal([]byte(first.Output), &firstOutput); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	if err := json.Unmarshal([]byte(second.Output), &secondOutput); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if firstOutput.TaskID != secondOutput.TaskID {
		t.Fatalf("dedup ids = %d/%d, want same", firstOutput.TaskID, secondOutput.TaskID)
	}
	if !secondOutput.Deduplicated {
		t.Fatal("second Deduplicated = false, want true")
	}
}

func TestTaskCreateToolRejectsMissingPrompt(t *testing.T) {
	result, err := NewTaskCreateTool(newTaskToolTestQueue(t)).Execute(context.Background(), json.RawMessage(`{"prompt":" "}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "prompt is required") {
		t.Fatalf("result = %+v, want prompt error", result)
	}
}

func TestTaskListToolListsQueueTasks(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "first task", ""); err != nil {
		t.Fatalf("Enqueue first: %v", err)
	}
	if _, _, err := queue.Enqueue(ctx, "second task", ""); err != nil {
		t.Fatalf("Enqueue second: %v", err)
	}

	result, err := NewTaskListTool(queue).Execute(ctx, json.RawMessage(`{"limit":10}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskListToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.TotalReturned != 2 {
		t.Fatalf("TotalReturned = %d, want 2", output.TotalReturned)
	}
	if output.Receipt.Tool != TaskListToolName || output.Receipt.Action != "list" || !output.Receipt.ReadOnly || output.Receipt.Persistent || output.Receipt.TotalReturned != 2 || output.Receipt.Limit != 10 {
		t.Fatalf("receipt = %+v, want task_list read-only observation receipt", output.Receipt)
	}
	if output.Limit != 10 {
		t.Fatalf("Limit = %d, want 10", output.Limit)
	}
	for _, task := range output.Tasks {
		if task.Status != StatusPending {
			t.Fatalf("task status = %q, want pending", task.Status)
		}
		if task.PayloadPreview == "" {
			t.Fatalf("task %+v has empty payload preview", task)
		}
	}
}

func TestTaskListToolReportsTimingEvidence(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "running task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	now := time.Date(2026, 5, 13, 5, 0, 0, 0, time.UTC)
	created := now.Add(-45 * time.Second)
	started := now.Add(-30 * time.Second)
	updated := now.Add(-12 * time.Second)
	if _, err := queue.db.ExecContext(ctx, `
		UPDATE task_queue
		SET created_at = ?, started_at = ?, updated_at = ?, timeout_class = ?, idle_timeout_count = ?, active_timeout_count = ?
		WHERE id = ?`,
		created.UnixMilli(), started.UnixMilli(), updated.UnixMilli(), string(TimeoutClassIdle), 2, 1, task.ID,
	); err != nil {
		t.Fatalf("update task timing: %v", err)
	}

	tool := NewTaskListTool(queue)
	tool.now = func() time.Time { return now }
	result, err := tool.Execute(ctx, json.RawMessage(`{"status":"running"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskListToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.TotalReturned != 1 {
		t.Fatalf("TotalReturned = %d, want 1", output.TotalReturned)
	}
	got := output.Tasks[0]
	if got.ObservedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("ObservedAt = %q, want %q", got.ObservedAt, now.Format(time.RFC3339Nano))
	}
	if got.AgeSeconds != 45 || got.RunningSeconds != 30 || got.IdleSeconds != 12 {
		t.Fatalf("timing = age %d running %d idle %d, want 45/30/12", got.AgeSeconds, got.RunningSeconds, got.IdleSeconds)
	}
	if got.TimeoutClass != string(TimeoutClassIdle) || got.IdleTimeoutCount != 2 || got.ActiveTimeoutCount != 1 {
		t.Fatalf("timeout metadata = class %q idle %d active %d", got.TimeoutClass, got.IdleTimeoutCount, got.ActiveTimeoutCount)
	}
}

func TestTaskListToolFiltersStatus(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "done task", ""); err != nil {
		t.Fatalf("Enqueue done: %v", err)
	}
	if _, _, err := queue.Enqueue(ctx, "pending task", ""); err != nil {
		t.Fatalf("Enqueue pending: %v", err)
	}
	claimed, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if claimed == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.MarkDone(ctx, claimed.ID, "finished", "done summary"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	result, err := NewTaskListTool(queue).Execute(ctx, json.RawMessage(`{"status":"done"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskListToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.TotalReturned != 1 {
		t.Fatalf("TotalReturned = %d, want 1", output.TotalReturned)
	}
	if output.Tasks[0].Status != StatusDone {
		t.Fatalf("status = %q, want done", output.Tasks[0].Status)
	}
}

func TestTaskGetToolReturnsDetails(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "inspect me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	result, err := NewTaskGetTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(id)+`}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskGetToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.Found || output.Task == nil {
		t.Fatalf("output = %+v, want found task", output)
	}
	if output.Receipt.Tool != TaskGetToolName || output.Receipt.Action != "get" || !output.Receipt.ReadOnly || output.Receipt.Persistent || output.Receipt.TaskID != id || !output.Receipt.Found {
		t.Fatalf("receipt = %+v, want task_get read-only receipt", output.Receipt)
	}
	if output.Task.ID != id {
		t.Fatalf("ID = %d, want %d", output.Task.ID, id)
	}
	if output.Task.Payload != "inspect me" {
		t.Fatalf("Payload = %q, want inspect me", output.Task.Payload)
	}
}

func TestTaskGetToolReportsTimingEvidence(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "inspect timing", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	now := time.Date(2026, 5, 13, 5, 30, 0, 0, time.UTC)
	created := now.Add(-60 * time.Second)
	started := now.Add(-50 * time.Second)
	updated := now.Add(-15 * time.Second)
	if _, err := queue.db.ExecContext(ctx, `
		UPDATE task_queue
		SET created_at = ?, started_at = ?, updated_at = ?
		WHERE id = ?`,
		created.UnixMilli(), started.UnixMilli(), updated.UnixMilli(), task.ID,
	); err != nil {
		t.Fatalf("update task timing: %v", err)
	}

	tool := NewTaskGetTool(queue)
	tool.now = func() time.Time { return now }
	result, err := tool.Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskGetToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.Found || output.Task == nil {
		t.Fatalf("output = %+v, want found task", output)
	}
	if output.Task.ObservedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("ObservedAt = %q, want %q", output.Task.ObservedAt, now.Format(time.RFC3339Nano))
	}
	if output.Task.AgeSeconds != 60 || output.Task.RunningSeconds != 50 || output.Task.IdleSeconds != 15 {
		t.Fatalf("timing = age %d running %d idle %d, want 60/50/15", output.Task.AgeSeconds, output.Task.RunningSeconds, output.Task.IdleSeconds)
	}
}

func TestTaskGetToolReturnsStructuredNotFound(t *testing.T) {
	result, err := NewTaskGetTool(newTaskToolTestQueue(t)).Execute(context.Background(), json.RawMessage(`{"id":404}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskGetToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Found || output.Task != nil {
		t.Fatalf("output = %+v, want structured not found", output)
	}
}

func TestTaskStopToolCancelsPendingTask(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "cancel me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	result, err := NewTaskStopTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(id)+`,"reason":"not needed"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskStopToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.TaskID != id || !output.Stopped || !output.Accepted || !output.Terminal || output.PreviousStatus != StatusPending || output.Status != StatusFailed {
		t.Fatalf("output = %+v, want accepted terminal pending->failed stop", output)
	}
	if output.Receipt.Tool != TaskStopToolName || output.Receipt.Action != "stop" || output.Receipt.ReadOnly || !output.Receipt.Persistent || output.Receipt.TaskID != id || output.Receipt.PreviousStatus != StatusPending || output.Receipt.Status != StatusFailed {
		t.Fatalf("receipt = %+v, want task_stop mutation receipt", output.Receipt)
	}

	task, err := queue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get stopped task: %v", err)
	}
	if task.Status != StatusFailed || task.Progress != "cancelled" || !strings.Contains(task.Result, "not needed") {
		t.Fatalf("task = %+v, want failed cancelled with reason", task)
	}
}

type fakeRunningTaskCanceller struct {
	called  int
	taskID  int64
	reason  string
	stopped bool
	err     error
}

func (f *fakeRunningTaskCanceller) CancelRunningTask(id int64, reason string) (bool, error) {
	f.called++
	f.taskID = id
	f.reason = reason
	return f.stopped, f.err
}

func TestTaskStopToolCancelsRunningTaskWithCanceller(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "running task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	canceller := &fakeRunningTaskCanceller{stopped: true}
	result, err := NewTaskStopTool(queue).WithRunningCanceller(canceller).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"reason":"operator stop"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	if canceller.called != 1 || canceller.taskID != task.ID || canceller.reason != "operator stop" {
		t.Fatalf("canceller = %+v, want one call for task %d", canceller, task.ID)
	}

	var output taskStopToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.TaskID != task.ID || !output.Stopped || !output.Accepted || output.Terminal || output.PreviousStatus != StatusRunning || output.Status != StatusRunning || output.FollowupTool != TaskMonitorToolName {
		t.Fatalf("output = %+v, want accepted non-terminal running stop request with task_monitor followup", output)
	}
	stillRunning, err := queue.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get running task: %v", err)
	}
	if stillRunning.Status != StatusRunning {
		t.Fatalf("task status = %s, want queue state left running for worker cancellation", stillRunning.Status)
	}
}

func TestTaskStopToolRejectsRunningTaskWithoutCanceller(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "running task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	result, err := NewTaskStopTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "running task cancellation unavailable") {
		t.Fatalf("result = %+v, want running cancellation unavailable error", result)
	}
}

func TestTaskOutputToolReturnsBoundedResultTail(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "task payload", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.MarkDone(ctx, task.ID, "abcdef", "done summary"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	result, err := NewTaskOutputTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"max_chars":3}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskOutputToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Field != "result" || output.Content != "def" || output.TotalChars != 6 || !output.Truncated {
		t.Fatalf("output = %+v, want result tail def", output)
	}
	if output.Receipt.Tool != TaskOutputToolName || output.Receipt.Action != "output" || !output.Receipt.ReadOnly || output.Receipt.TaskID != task.ID || output.Receipt.Field != "result" || output.Receipt.RetrievalStatus != taskOutputRetrievalSuccess {
		t.Fatalf("receipt = %+v, want task_output observation receipt", output.Receipt)
	}
}

func TestTaskOutputToolReadsProgressField(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "task payload", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.UpdateProgress(ctx, task.ID, "still working"); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	result, err := NewTaskOutputTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"field":"progress"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskOutputToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Field != "progress" || output.Content != "still working" || output.Truncated {
		t.Fatalf("output = %+v, want progress content", output)
	}
}

func TestTaskOutputToolReportsNotReadyForNonBlockingActiveTask(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "task payload", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.UpdateProgress(ctx, task.ID, "still working"); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	result, err := NewTaskOutputTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"field":"progress","block":false}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskOutputToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.RetrievalStatus != "not_ready" || output.Terminal {
		t.Fatalf("retrieval = %q terminal=%v, want not_ready non-terminal", output.RetrievalStatus, output.Terminal)
	}
	if output.Content != "still working" {
		t.Fatalf("Content = %q, want progress content", output.Content)
	}
}

func TestTaskOutputToolBlocksUntilTaskCompletes(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "task payload", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = queue.MarkDone(ctx, task.ID, "abcdef", "done summary")
	}()

	result, err := NewTaskOutputTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"block":true,"timeout_ms":500,"max_chars":3}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskOutputToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.RetrievalStatus != "success" || !output.Terminal || output.Status != StatusDone {
		t.Fatalf("output = %+v, want terminal success", output)
	}
	if output.Content != "def" || output.TotalChars != 6 || !output.Truncated {
		t.Fatalf("content = %q total=%d truncated=%v, want def/6/true", output.Content, output.TotalChars, output.Truncated)
	}
}

func TestTaskOutputToolBlockTimeoutReturnsCurrentTask(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "task payload", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	result, err := NewTaskOutputTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"block":true,"timeout_ms":1}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskOutputToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.RetrievalStatus != "timeout" || output.Terminal || output.Status != StatusRunning {
		t.Fatalf("output = %+v, want timeout with current running task", output)
	}
}

func TestTaskMonitorToolReturnsRunningSnapshot(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "task payload", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if _, err := queue.UpdateAnnotation(ctx, task.ID, "still working", "halfway"); err != nil {
		t.Fatalf("UpdateAnnotation: %v", err)
	}

	result, err := NewTaskMonitorTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskMonitorToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.TaskID != task.ID || output.Status != StatusRunning || output.Terminal {
		t.Fatalf("output = %+v, want running non-terminal task %d", output, task.ID)
	}
	if output.Progress != "still working" || output.Summary != "halfway" {
		t.Fatalf("progress/summary = %q/%q, want still working/halfway", output.Progress, output.Summary)
	}
	if output.NextPollSeconds != defaultTaskMonitorPollSeconds {
		t.Fatalf("NextPollSeconds = %d, want %d", output.NextPollSeconds, defaultTaskMonitorPollSeconds)
	}
	if output.Receipt.Tool != TaskMonitorToolName || output.Receipt.Action != "monitor" || !output.Receipt.ReadOnly || output.Receipt.TaskID != task.ID || output.Receipt.RetrievalStatus != taskMonitorRetrievalSnapshot {
		t.Fatalf("receipt = %+v, want task_monitor observation receipt", output.Receipt)
	}
}

func TestTaskMonitorToolReportsTimingAndTimeoutMetadata(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "timed task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	now := time.Date(2026, 5, 13, 4, 0, 0, 0, time.UTC)
	created := now.Add(-30 * time.Second)
	started := now.Add(-20 * time.Second)
	updated := now.Add(-7 * time.Second)
	if _, err := queue.db.ExecContext(ctx, `
		UPDATE task_queue
		SET created_at = ?, started_at = ?, updated_at = ?, timeout_class = ?, idle_timeout_count = ?, active_timeout_count = ?
		WHERE id = ?`,
		created.UnixMilli(), started.UnixMilli(), updated.UnixMilli(), string(TimeoutClassIdle), 2, 1, task.ID,
	); err != nil {
		t.Fatalf("update task timestamps: %v", err)
	}

	tool := NewTaskMonitorTool(queue)
	tool.now = func() time.Time { return now }
	result, err := tool.Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskMonitorToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.ObservedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("ObservedAt = %q, want %q", output.ObservedAt, now.Format(time.RFC3339Nano))
	}
	if output.CreatedAt != created.Format(time.RFC3339Nano) || output.StartedAt != started.Format(time.RFC3339Nano) || output.UpdatedAt != updated.Format(time.RFC3339Nano) {
		t.Fatalf("timestamps = created %q started %q updated %q", output.CreatedAt, output.StartedAt, output.UpdatedAt)
	}
	if output.AgeSeconds != 30 || output.RunningSeconds != 20 || output.IdleSeconds != 7 {
		t.Fatalf("durations = age %d running %d idle %d", output.AgeSeconds, output.RunningSeconds, output.IdleSeconds)
	}
	if output.TimeoutClass != string(TimeoutClassIdle) || output.IdleTimeoutCount != 2 || output.ActiveTimeoutCount != 1 {
		t.Fatalf("timeout metadata = class %q idle %d active %d", output.TimeoutClass, output.IdleTimeoutCount, output.ActiveTimeoutCount)
	}
}

func TestTaskMonitorToolWaitsForUpdate(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "waited task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	initial, err := queue.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get initial task: %v", err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = queue.UpdateAnnotation(ctx, task.ID, "new progress", "updated")
	}()

	result, err := NewTaskMonitorTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"wait_for_update":true,"since_updated_at":"`+initial.UpdatedAt.Format(time.RFC3339Nano)+`","timeout_ms":500}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskMonitorToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.RetrievalStatus != taskMonitorRetrievalChanged {
		t.Fatalf("RetrievalStatus = %q, want changed", output.RetrievalStatus)
	}
	if output.Observation.Mode != "wait_for_update" || !output.Observation.WaitForUpdate {
		t.Fatalf("observation = %+v, want wait_for_update mode", output.Observation)
	}
	if output.Observation.SinceUpdatedAt != formatTaskToolTime(initial.UpdatedAt) || output.Observation.TimeoutMS != 500 {
		t.Fatalf("observation = %+v, want baseline and timeout", output.Observation)
	}
	if output.Progress != "new progress" || output.Summary != "updated" {
		t.Fatalf("progress/summary = %q/%q, want new progress/updated", output.Progress, output.Summary)
	}
}

func TestTaskMonitorToolReportsSnapshotObservationReceipt(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "snapshot receipt", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	result, err := NewTaskMonitorTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"max_chars":123}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskMonitorToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Observation.Mode != "snapshot" || output.Observation.WaitForUpdate {
		t.Fatalf("observation = %+v, want snapshot mode", output.Observation)
	}
	if output.Observation.MaxChars != 123 {
		t.Fatalf("observation max chars = %d, want 123", output.Observation.MaxChars)
	}
}

func TestTaskMonitorToolWaitsForTerminalUpdate(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "terminal task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	initial, err := queue.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get initial task: %v", err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = queue.MarkDone(ctx, task.ID, "terminal result", "done summary")
	}()

	result, err := NewTaskMonitorTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"wait_for_update":true,"since_updated_at":"`+initial.UpdatedAt.Format(time.RFC3339Nano)+`","timeout_ms":500}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskMonitorToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.RetrievalStatus != taskMonitorRetrievalTerminal || output.Status != StatusDone || !output.Terminal {
		t.Fatalf("output = %+v, want terminal done task", output)
	}
	if output.ResultTail != "terminal result" || output.Summary != "done summary" {
		t.Fatalf("result/summary = %q/%q, want terminal result/done summary", output.ResultTail, output.Summary)
	}
}

func TestTaskMonitorToolWaitForUpdateTimeoutReturnsCurrentSnapshot(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "quiet task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}

	initial, err := queue.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get initial task: %v", err)
	}
	result, err := NewTaskMonitorTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"wait_for_update":true,"since_updated_at":"`+initial.UpdatedAt.Format(time.RFC3339Nano)+`","timeout_ms":1}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskMonitorToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.RetrievalStatus != taskMonitorRetrievalTimeout || output.Status != StatusRunning || output.Terminal {
		t.Fatalf("output = %+v, want timeout with current running snapshot", output)
	}
}

func TestTaskMonitorToolRejectsWaitForUpdateWithoutBaseline(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "baseline required", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	result, err := NewTaskMonitorTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(id)+`,"wait_for_update":true}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "since_updated_at is required") {
		t.Fatalf("result = %+v, want since_updated_at error", result)
	}
}

func TestTaskMonitorToolReturnsTerminalResultTail(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "task payload", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.MarkDone(ctx, task.ID, "abcdef", "done summary"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	result, err := NewTaskMonitorTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"max_chars":3}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskMonitorToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Status != StatusDone || !output.Terminal || output.NextPollSeconds != 0 {
		t.Fatalf("output = %+v, want done terminal with no next poll", output)
	}
	if output.ResultTail != "def" || output.ResultTotalChars != 6 || !output.ResultTruncated {
		t.Fatalf("result tail = %q total=%d truncated=%v, want def/6/true", output.ResultTail, output.ResultTotalChars, output.ResultTruncated)
	}
}

func TestTaskUpdateToolAnnotatesPendingTask(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	id, _, err := queue.Enqueue(ctx, "annotate me", "")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	result, err := NewTaskUpdateTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(id)+`,"progress":"queued for review","summary":"waiting"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output taskUpdateToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.TaskID != id || output.Status != StatusPending || output.Progress != "queued for review" || output.Summary != "waiting" || !output.Updated {
		t.Fatalf("output = %+v, want pending annotation", output)
	}
	if output.Receipt.Tool != TaskUpdateToolName || output.Receipt.Action != "update" || output.Receipt.ReadOnly || !output.Receipt.Persistent || output.Receipt.TaskID != id || output.Receipt.Status != StatusPending {
		t.Fatalf("receipt = %+v, want task_update annotation receipt", output.Receipt)
	}

	task, err := queue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get updated task: %v", err)
	}
	if task.Progress != "queued for review" || task.Summary != "waiting" || task.Status != StatusPending {
		t.Fatalf("task = %+v, want annotated pending task", task)
	}
}

func TestTaskUpdateToolRejectsTerminalTask(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)
	if _, _, err := queue.Enqueue(ctx, "done task", ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	task, err := queue.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if task == nil {
		t.Fatal("Next returned nil")
	}
	if err := queue.MarkDone(ctx, task.ID, "finished", "done summary"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	result, err := NewTaskUpdateTool(queue).Execute(ctx, json.RawMessage(`{"id":`+jsonInt(task.ID)+`,"summary":"rewrite history"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "only pending or running tasks can be updated") {
		t.Fatalf("result = %+v, want terminal task rejection", result)
	}
}

func TestTaskToolsRejectInvalidInput(t *testing.T) {
	ctx := context.Background()
	queue := newTaskToolTestQueue(t)

	listResult, err := NewTaskListTool(queue).Execute(ctx, json.RawMessage(`{"status":"blocked"}`))
	if err != nil {
		t.Fatalf("task_list Execute error = %v", err)
	}
	if !listResult.IsError || !strings.Contains(listResult.Output, "status must be") {
		t.Fatalf("task_list result = %+v, want status error", listResult)
	}

	getResult, err := NewTaskGetTool(queue).Execute(ctx, json.RawMessage(`{"id":0}`))
	if err != nil {
		t.Fatalf("task_get Execute error = %v", err)
	}
	if !getResult.IsError || !strings.Contains(getResult.Output, "id must be positive") {
		t.Fatalf("task_get result = %+v, want id error", getResult)
	}

	stopResult, err := NewTaskStopTool(queue).Execute(ctx, json.RawMessage(`{"id":0}`))
	if err != nil {
		t.Fatalf("task_stop Execute error = %v", err)
	}
	if !stopResult.IsError || !strings.Contains(stopResult.Output, "id must be positive") {
		t.Fatalf("task_stop result = %+v, want id error", stopResult)
	}

	outputResult, err := NewTaskOutputTool(queue).Execute(ctx, json.RawMessage(`{"id":1,"field":"log"}`))
	if err != nil {
		t.Fatalf("task_output Execute error = %v", err)
	}
	if !outputResult.IsError || !strings.Contains(outputResult.Output, "field must be") {
		t.Fatalf("task_output result = %+v, want field error", outputResult)
	}

	updateResult, err := NewTaskUpdateTool(queue).Execute(ctx, json.RawMessage(`{"id":1}`))
	if err != nil {
		t.Fatalf("task_update Execute error = %v", err)
	}
	if !updateResult.IsError || !strings.Contains(updateResult.Output, "progress or summary is required") {
		t.Fatalf("task_update result = %+v, want missing annotation error", updateResult)
	}
}

func TestTaskToolsMetadata(t *testing.T) {
	createTool := NewTaskCreateTool(nil)
	if createTool.IsConcurrencySafe(nil) {
		t.Fatal("task_create should not be concurrency-safe")
	}
	if createTool.Reversible() {
		t.Fatal("task_create should not be reversible")
	}
	if got := createTool.Scope(nil); !got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("task_create Scope() = %+v, want persistent-only scope", got)
	}
	if createTool.ShouldCancelSiblingsOnError() {
		t.Fatal("task_create should not cancel siblings")
	}

	answerTool := NewUserQuestionAnswerTool(nil)
	if answerTool.IsConcurrencySafe(nil) {
		t.Fatal("user_question_answer should not be concurrency-safe")
	}
	if answerTool.Reversible() {
		t.Fatal("user_question_answer should not be reversible")
	}
	if got := answerTool.Scope(nil); !got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("user_question_answer Scope() = %+v, want persistent-only scope", got)
	}
	if answerTool.ShouldCancelSiblingsOnError() {
		t.Fatal("user_question_answer should not cancel siblings")
	}
	if !tools.ShouldDeferToolSchema(answerTool) {
		t.Fatal("user_question_answer should defer initial schema")
	}

	stopTool := NewTaskStopTool(nil)
	if stopTool.IsConcurrencySafe(nil) {
		t.Fatal("task_stop should not be concurrency-safe")
	}
	if stopTool.Reversible() {
		t.Fatal("task_stop should not be reversible")
	}
	if got := stopTool.Scope(nil); !got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("task_stop Scope() = %+v, want persistent-only scope", got)
	}
	if stopTool.ShouldCancelSiblingsOnError() {
		t.Fatal("task_stop should not cancel siblings")
	}

	updateTool := NewTaskUpdateTool(nil)
	if updateTool.IsConcurrencySafe(nil) {
		t.Fatal("task_update should not be concurrency-safe")
	}
	if updateTool.Reversible() {
		t.Fatal("task_update should not be reversible")
	}
	if got := updateTool.Scope(nil); !got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("task_update Scope() = %+v, want persistent-only scope", got)
	}
	if updateTool.ShouldCancelSiblingsOnError() {
		t.Fatal("task_update should not cancel siblings")
	}

	listTool := NewTaskListTool(nil)
	getTool := NewTaskGetTool(nil)
	outputTool := NewTaskOutputTool(nil)
	monitorTool := NewTaskMonitorTool(nil)
	for _, tool := range []tools.Tool{listTool, getTool, outputTool, monitorTool} {
		if !tool.IsConcurrencySafe(nil) {
			t.Fatalf("%s should be concurrency-safe", tool.Name())
		}
		if !tool.Reversible() {
			t.Fatalf("%s should be reversible", tool.Name())
		}
		if got := tool.Scope(nil); len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 || got.Network || got.Persistent {
			t.Fatalf("%s Scope() = %+v, want empty scope", tool.Name(), got)
		}
		if tool.ShouldCancelSiblingsOnError() {
			t.Fatalf("%s should not cancel siblings", tool.Name())
		}
	}
}

func newTaskToolTestQueue(t *testing.T) *Queue {
	t.Helper()
	db, err := core.OpenDB(t.TempDir())
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	queue, err := NewQueueNoRecover(db.Main)
	if err != nil {
		t.Fatalf("NewQueueNoRecover: %v", err)
	}
	return queue
}

func jsonInt(v int64) string {
	data, _ := json.Marshal(v)
	return string(data)
}
