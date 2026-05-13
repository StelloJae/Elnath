package learning

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUserQuestionListToolListsPendingQuestions(t *testing.T) {
	store := NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	if err := store.Append(OutcomeRecord{
		ProjectID: "elnath",
		Intent:    "complex_task",
		Workflow:  "single",
		Timestamp: time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC),
		ControlToolReceipts: []ControlToolReceipt{{
			Tool:      "ask_user_question",
			Action:    "request",
			RequestID: "req-1",
			SessionID: "sess-1",
			Question:  "Which branch?",
		}},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	result, err := NewUserQuestionListTool(store).Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1","limit":5}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output userQuestionListToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Count != 1 || len(output.Pending) != 1 || output.Pending[0].RequestID != "req-1" || output.Pending[0].Question != "Which branch?" {
		t.Fatalf("output = %+v, want req-1 pending", output)
	}
	if output.Receipt.Tool != UserQuestionListToolName || output.Receipt.Action != "list" || !output.Receipt.ReadOnly || output.Receipt.SessionID != "sess-1" || output.Receipt.Limit != 5 || output.Receipt.TotalReturned != 1 {
		t.Fatalf("receipt = %+v, want read-only pending-question list receipt", output.Receipt)
	}
}

func TestUserQuestionListToolMetadataAndErrors(t *testing.T) {
	tool := NewUserQuestionListTool(nil)
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatal("user_question_list should be read-only and reversible")
	}
	if got := tool.Scope(nil); got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("Scope() = %+v, want empty scope", got)
	}
	if !tool.DeferInitialToolSchema() {
		t.Fatal("user_question_list should defer initial schema")
	}
	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "outcome store unavailable") {
		t.Fatalf("result = %+v, want unavailable error", result)
	}
}
