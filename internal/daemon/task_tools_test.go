package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
	if output.Task.ID != id {
		t.Fatalf("ID = %d, want %d", output.Task.ID, id)
	}
	if output.Task.Payload != "inspect me" {
		t.Fatalf("Payload = %q, want inspect me", output.Task.Payload)
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
	if output.TaskID != id || !output.Stopped || output.PreviousStatus != StatusPending || output.Status != StatusFailed {
		t.Fatalf("output = %+v, want stopped pending->failed", output)
	}

	task, err := queue.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get stopped task: %v", err)
	}
	if task.Status != StatusFailed || task.Progress != "cancelled" || !strings.Contains(task.Result, "not needed") {
		t.Fatalf("task = %+v, want failed cancelled with reason", task)
	}
}

func TestTaskStopToolRejectsRunningTask(t *testing.T) {
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
	if !result.IsError || !strings.Contains(result.Output, "only pending tasks can be stopped") {
		t.Fatalf("result = %+v, want running unsupported error", result)
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
	for _, tool := range []tools.Tool{listTool, getTool, outputTool} {
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
