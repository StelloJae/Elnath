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

func TestUserQuestionWaitToolReturnsAnsweredWhenAnswerArrives(t *testing.T) {
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
		t.Fatalf("Append ask: %v", err)
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = store.Append(OutcomeRecord{
			ProjectID: "elnath",
			Intent:    "user_input_answer",
			Workflow:  "task_answer",
			Timestamp: time.Date(2026, 5, 13, 7, 0, 1, 0, time.UTC),
			ControlToolReceipts: []ControlToolReceipt{{
				Tool:         "user_question_answer",
				Action:       "answer",
				RequestID:    "req-1",
				SessionID:    "sess-1",
				TaskID:       42,
				AnswerChars:  8,
				FollowupTool: "task_monitor",
			}},
		})
	}()

	result, err := NewUserQuestionWaitTool(store).Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1","request_id":"req-1","wait_ms":500}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	var output userQuestionWaitToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Status != "answered" || output.TaskID != 42 || output.AnswerChars != 8 || output.WaitTimedOut {
		t.Fatalf("output = %+v, want answered task 42 without wait timeout", output)
	}
	if output.Receipt.Tool != UserQuestionWaitToolName || output.Receipt.Action != "wait" || !output.Receipt.ReadOnly || output.Receipt.ExecutionPolicy != "user_input_wait" {
		t.Fatalf("receipt = %+v, want user_question_wait receipt", output.Receipt)
	}
	if output.Receipt.WaitMS != 500 || output.Receipt.WaitTimedOut || output.Receipt.FollowupTool != "task_monitor" || output.Receipt.TaskID != 42 || output.Receipt.AnswerChars != 8 {
		t.Fatalf("receipt wait/followup = %+v", output.Receipt)
	}
}

func TestUserQuestionWaitToolReportsPendingOnWaitTimeout(t *testing.T) {
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
		t.Fatalf("Append ask: %v", err)
	}

	result, err := NewUserQuestionWaitTool(store).Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1","request_id":"req-1","wait_ms":10}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	var output userQuestionWaitToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Status != "pending" || !output.WaitTimedOut || output.TaskID != 0 {
		t.Fatalf("output = %+v, want pending wait timeout", output)
	}
	if output.Receipt.Status != "pending" || !output.Receipt.WaitTimedOut || output.Receipt.FollowupTool != UserQuestionWaitToolName {
		t.Fatalf("receipt = %+v, want pending wait followup", output.Receipt)
	}
}

func TestUserQuestionWaitToolReturnsCanceled(t *testing.T) {
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
		t.Fatalf("Append ask: %v", err)
	}
	if err := store.Append(OutcomeRecord{
		ProjectID: "elnath",
		Intent:    "user_input_cancel",
		Workflow:  "task_cancel_question",
		Timestamp: time.Date(2026, 5, 13, 7, 0, 1, 0, time.UTC),
		ControlToolReceipts: []ControlToolReceipt{{
			Tool:      UserQuestionCancelToolName,
			Action:    "cancel",
			RequestID: "req-1",
			SessionID: "sess-1",
			Status:    "cancelled",
		}},
	}); err != nil {
		t.Fatalf("Append cancel: %v", err)
	}

	result, err := NewUserQuestionWaitTool(store).Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1","request_id":"req-1","wait_ms":500}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	var output userQuestionWaitToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Status != "cancelled" || output.WaitTimedOut {
		t.Fatalf("output = %+v, want cancelled without wait timeout", output)
	}
	if output.Receipt.Status != "cancelled" || output.Receipt.FollowupTool != UserQuestionListToolName {
		t.Fatalf("receipt = %+v, want cancelled with list followup", output.Receipt)
	}
}

func TestUserQuestionWaitToolReturnsTimedOutQuestion(t *testing.T) {
	store := NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	if err := store.Append(OutcomeRecord{
		ProjectID: "elnath",
		Intent:    "complex_task",
		Workflow:  "single",
		Timestamp: time.Now().Add(-2 * time.Second),
		ControlToolReceipts: []ControlToolReceipt{{
			Tool:           "ask_user_question",
			Action:         "request",
			RequestID:      "req-1",
			SessionID:      "sess-1",
			Question:       "Still needed?",
			TimeoutSeconds: 1,
		}},
	}); err != nil {
		t.Fatalf("Append ask: %v", err)
	}

	result, err := NewUserQuestionWaitTool(store).Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1","request_id":"req-1","wait_ms":500}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	var output userQuestionWaitToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Status != "timed_out" || output.WaitTimedOut {
		t.Fatalf("output = %+v, want request timed_out without wait timeout", output)
	}
	if output.Receipt.Status != "timed_out" || output.Receipt.Reason == "" || output.Receipt.FollowupTool != UserQuestionListToolName {
		t.Fatalf("receipt = %+v, want timed_out with reason and list followup", output.Receipt)
	}
}

func TestUserQuestionCancelToolCancelsPendingQuestion(t *testing.T) {
	store := NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	if err := store.Append(OutcomeRecord{
		ProjectID: "elnath",
		Intent:    "complex_task",
		Workflow:  "single",
		Timestamp: time.Date(2026, 5, 13, 7, 0, 0, 0, time.UTC),
		ControlToolReceipts: []ControlToolReceipt{{
			Tool:          "ask_user_question",
			Action:        "request",
			RequestID:     "req-1",
			SessionID:     "sess-1",
			Question:      "Which branch?",
			QuestionChars: 13,
		}},
	}); err != nil {
		t.Fatalf("Append ask: %v", err)
	}

	result, err := NewUserQuestionCancelTool(store).Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1","request_id":"req-1","reason":"operator changed direction"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	var output userQuestionCancelToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Status != "cancelled" || output.RequestID != "req-1" || output.SessionID != "sess-1" || output.QuestionChars != 13 {
		t.Fatalf("output = %+v, want cancelled req-1", output)
	}
	if output.Receipt.Tool != UserQuestionCancelToolName || output.Receipt.Action != "cancel" || output.Receipt.ReadOnly || !output.Receipt.Persistent || output.Receipt.Status != "cancelled" || output.Receipt.Reason != "operator changed direction" {
		t.Fatalf("receipt = %+v, want persistent cancel receipt", output.Receipt)
	}
	records, err := store.Recent(0)
	if err != nil {
		t.Fatalf("Recent outcomes: %v", err)
	}
	if pending := PendingUserQuestions(records, "sess-1", 10); len(pending) != 0 {
		t.Fatalf("pending = %+v, want cancel receipt to close req-1", pending)
	}
}

func TestUserQuestionWaitToolReturnsNotFound(t *testing.T) {
	store := NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	result, err := NewUserQuestionWaitTool(store).Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1","request_id":"req-missing","wait_ms":500}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	var output userQuestionWaitToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Status != "not_found" || output.WaitTimedOut {
		t.Fatalf("output = %+v, want immediate not_found", output)
	}
	if output.Receipt.Status != "not_found" || output.Receipt.WaitTimedOut {
		t.Fatalf("receipt = %+v, want not_found without wait timeout", output.Receipt)
	}
}

func TestUserQuestionWaitToolMetadataAndErrors(t *testing.T) {
	tool := NewUserQuestionWaitTool(nil)
	if !tool.IsConcurrencySafe(nil) || !tool.Reversible() {
		t.Fatal("user_question_wait should be read-only and reversible")
	}
	if got := tool.Scope(nil); got.Persistent || got.Network || len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 {
		t.Fatalf("Scope() = %+v, want empty scope", got)
	}
	if !tool.DeferInitialToolSchema() {
		t.Fatal("user_question_wait should defer initial schema")
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1","request_id":"req-1"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "outcome store unavailable") {
		t.Fatalf("result = %+v, want unavailable error", result)
	}
	result, err = NewUserQuestionWaitTool(NewOutcomeStore(filepath.Join(t.TempDir(), "outcomes.jsonl"))).Execute(context.Background(), json.RawMessage(`{"session_id":"sess-1"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "request_id is required") {
		t.Fatalf("result = %+v, want request_id required", result)
	}
}
