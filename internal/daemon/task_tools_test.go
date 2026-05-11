package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/tools"
)

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
}

func TestTaskToolsMetadata(t *testing.T) {
	listTool := NewTaskListTool(nil)
	getTool := NewTaskGetTool(nil)
	for _, tool := range []tools.Tool{listTool, getTool} {
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
