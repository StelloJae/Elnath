package scheduler

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/tools"
)

func TestScheduleCreateListDeleteTools(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scheduled_tasks.yaml")

	createResult, err := NewScheduleCreateTool(path).Execute(ctx, json.RawMessage(`{
		"name": "morning-check",
		"type": "research",
		"prompt": "check readiness",
		"interval": "24h",
		"run_on_start": true,
		"session_id": "sess-1",
		"surface": "tool-test"
	}`))
	if err != nil {
		t.Fatalf("create Execute error = %v", err)
	}
	if createResult.IsError {
		t.Fatalf("create returned error result: %s", createResult.Output)
	}
	var createOutput scheduleCreateToolOutput
	if err := json.Unmarshal([]byte(createResult.Output), &createOutput); err != nil {
		t.Fatalf("unmarshal create output: %v", err)
	}
	if createOutput.Path != path || createOutput.Task.Name != "morning-check" || createOutput.Task.Type != "research" || !createOutput.Task.RunOnStart {
		t.Fatalf("create output = %+v, want created research task", createOutput)
	}

	tasks, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after create: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Name != "morning-check" || tasks[0].SessionID != "sess-1" {
		t.Fatalf("tasks = %+v, want persisted scheduled task", tasks)
	}

	listResult, err := NewScheduleListTool(path).Execute(ctx, nil)
	if err != nil {
		t.Fatalf("list Execute error = %v", err)
	}
	if listResult.IsError {
		t.Fatalf("list returned error result: %s", listResult.Output)
	}
	var listOutput scheduleListToolOutput
	if err := json.Unmarshal([]byte(listResult.Output), &listOutput); err != nil {
		t.Fatalf("unmarshal list output: %v", err)
	}
	if listOutput.Total != 1 || listOutput.Tasks[0].Name != "morning-check" || !listOutput.Tasks[0].Enabled {
		t.Fatalf("list output = %+v, want one enabled task", listOutput)
	}

	deleteResult, err := NewScheduleDeleteTool(path).Execute(ctx, json.RawMessage(`{"name":"morning-check"}`))
	if err != nil {
		t.Fatalf("delete Execute error = %v", err)
	}
	if deleteResult.IsError {
		t.Fatalf("delete returned error result: %s", deleteResult.Output)
	}
	var deleteOutput scheduleDeleteToolOutput
	if err := json.Unmarshal([]byte(deleteResult.Output), &deleteOutput); err != nil {
		t.Fatalf("unmarshal delete output: %v", err)
	}
	if !deleteOutput.Deleted || deleteOutput.Name != "morning-check" {
		t.Fatalf("delete output = %+v, want deleted task", deleteOutput)
	}
	tasks, err = LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after delete: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks after delete = %+v, want empty", tasks)
	}
}

func TestScheduleCreateToolRejectsInvalidAndDuplicateTasks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scheduled_tasks.yaml")

	result, err := NewScheduleCreateTool(path).Execute(ctx, json.RawMessage(`{"name":"fast","prompt":"go","interval":"30s"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "interval must be >= 1m") {
		t.Fatalf("result = %+v, want interval validation error", result)
	}

	params := json.RawMessage(`{"name":"dup","prompt":"go","interval":"1m"}`)
	result, err = NewScheduleCreateTool(path).Execute(ctx, params)
	if err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("first create returned error result: %s", result.Output)
	}
	result, err = NewScheduleCreateTool(path).Execute(ctx, params)
	if err != nil {
		t.Fatalf("second Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "already exists") {
		t.Fatalf("result = %+v, want duplicate error", result)
	}
}

func TestScheduleDeleteToolRejectsMissingTask(t *testing.T) {
	result, err := NewScheduleDeleteTool(filepath.Join(t.TempDir(), "scheduled_tasks.yaml")).Execute(
		context.Background(),
		json.RawMessage(`{"name":"missing"}`),
	)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "not found") {
		t.Fatalf("result = %+v, want not found error", result)
	}
}

func TestScheduleToolsMetadata(t *testing.T) {
	createTool := NewScheduleCreateTool("")
	deleteTool := NewScheduleDeleteTool("")
	for _, tool := range []tools.Tool{createTool, deleteTool} {
		if tool.IsConcurrencySafe(nil) {
			t.Fatalf("%s should not be concurrency-safe", tool.Name())
		}
		if tool.Reversible() {
			t.Fatalf("%s should not be reversible", tool.Name())
		}
		if got := tool.Scope(nil); !got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
			t.Fatalf("%s Scope() = %+v, want persistent-only scope", tool.Name(), got)
		}
		if tool.ShouldCancelSiblingsOnError() {
			t.Fatalf("%s should not cancel siblings", tool.Name())
		}
	}

	listTool := NewScheduleListTool("")
	if !listTool.IsConcurrencySafe(nil) {
		t.Fatal("schedule_list should be concurrency-safe")
	}
	if !listTool.Reversible() {
		t.Fatal("schedule_list should be reversible")
	}
	if got := listTool.Scope(nil); got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("schedule_list Scope() = %+v, want empty scope", got)
	}
	if listTool.ShouldCancelSiblingsOnError() {
		t.Fatal("schedule_list should not cancel siblings")
	}
}
