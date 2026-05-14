package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTodoWriteTool_SummarizesChecklist(t *testing.T) {
	tool := NewTodoWriteTool()
	result, err := tool.Execute(context.Background(), rawJSON(`{
		"todos": [
			{"content": "research command structure", "status": "completed"},
			{"content": "implement todo tool", "status": "in_progress", "activeForm": "implementing todo tool"},
			{"content": "run tests", "status": "pending"}
		]
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output todoWriteOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Total != 3 {
		t.Fatalf("total = %d, want 3", output.Total)
	}
	if output.Counts["completed"] != 1 || output.Counts["in_progress"] != 1 || output.Counts["pending"] != 1 {
		t.Fatalf("counts = %+v, want one of each status", output.Counts)
	}
	if output.Todos[1].ActiveForm != "implementing todo tool" {
		t.Fatalf("active_form = %q, want camel-case compatibility value", output.Todos[1].ActiveForm)
	}
	if output.Receipt.Tool != TodoWriteName || output.Receipt.ExecutionPolicy != "session_todo_scratchpad" {
		t.Fatalf("receipt = %+v, want todo_write scratchpad receipt", output.Receipt)
	}
	if output.Receipt.Total != 3 || output.Receipt.Pending != 1 || output.Receipt.InProgress != 1 || output.Receipt.Completed != 1 {
		t.Fatalf("receipt counts = %+v, want one of each status", output.Receipt)
	}
}

func TestTodoWriteTool_RejectsInvalidTodos(t *testing.T) {
	tool := NewTodoWriteTool()
	cases := []struct {
		name   string
		params string
		want   string
	}{
		{name: "missing todos", params: `{}`, want: "missing todos"},
		{name: "empty content", params: `{"todos":[{"content":" ","status":"pending"}]}`, want: "content is required"},
		{name: "bad status", params: `{"todos":[{"content":"ship","status":"blocked"}]}`, want: "status must be"},
		{name: "multiple in progress", params: `{"todos":[{"content":"one","status":"in_progress","activeForm":"doing one"},{"content":"two","status":"in_progress","activeForm":"doing two"}]}`, want: "at most one in_progress"},
		{name: "in progress missing active form", params: `{"todos":[{"content":"ship","status":"in_progress"}]}`, want: "active_form is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), rawJSON(tc.params))
			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}
			if !result.IsError {
				t.Fatalf("Execute returned success: %s", result.Output)
			}
			if !strings.Contains(result.Output, tc.want) {
				t.Fatalf("output = %q, want substring %q", result.Output, tc.want)
			}
		})
	}
}

func TestTodoWriteTool_NudgesVerificationBeforeFinalClaim(t *testing.T) {
	tool := NewTodoWriteTool()
	result, err := tool.Execute(context.Background(), rawJSON(`{
		"todos": [
			{"content": "research", "status": "completed"},
			{"content": "implement", "status": "completed"},
			{"content": "summarize", "status": "completed"}
		]
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output todoWriteOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.AllCompleted {
		t.Fatal("AllCompleted = false, want true")
	}
	if !output.VerificationNudgeNeeded {
		t.Fatal("VerificationNudgeNeeded = false, want true")
	}
	if output.VerificationNudgeMessage == "" {
		t.Fatal("VerificationNudgeMessage is empty")
	}
}

func TestTodoWriteTool_NoNudgeWhenVerificationTodoExists(t *testing.T) {
	tool := NewTodoWriteTool()
	result, err := tool.Execute(context.Background(), rawJSON(`{
		"todos": [
			{"content": "research", "status": "completed"},
			{"content": "implement", "status": "completed"},
			{"content": "검증 실행", "status": "completed"}
		]
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output todoWriteOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.VerificationNudgeNeeded {
		t.Fatal("VerificationNudgeNeeded = true, want false")
	}
}

func TestTodoWriteToolMetadata(t *testing.T) {
	tool := NewTodoWriteTool()
	if tool.Name() != "todo_write" {
		t.Fatalf("Name() = %q, want todo_write", tool.Name())
	}
	if !strings.Contains(tool.Description(), "active_form") {
		t.Fatalf("Description() = %q, want active_form guidance", tool.Description())
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("Schema unmarshal: %v", err)
	}
	schemaText := string(tool.Schema())
	if !strings.Contains(schemaText, "Required when status is in_progress") {
		t.Fatalf("Schema() = %s, want in_progress active_form requirement", schemaText)
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Fatal("todo_write should be concurrency-safe")
	}
	if !tool.Reversible() {
		t.Fatal("todo_write should be reversible")
	}
	if got := tool.Scope(nil); len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 || got.Network || got.Persistent {
		t.Fatalf("Scope() = %+v, want empty scope", got)
	}
	if tool.ShouldCancelSiblingsOnError() {
		t.Fatal("todo_write should not cancel siblings")
	}
}
