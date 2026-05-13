package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/tools"
)

func TestAskUserQuestionToolReturnsStructuredRequest(t *testing.T) {
	result, err := NewAskUserQuestionTool().Execute(context.Background(), json.RawMessage(`{
		"question":"Which branch should I use?",
		"options":["main","new branch"],
		"allow_free_text":false,
		"timeout_seconds":120
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output askUserQuestionToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Type != "user_input_required" {
		t.Fatalf("Type = %q, want user_input_required", output.Type)
	}
	if output.Question != "Which branch should I use?" {
		t.Fatalf("Question = %q", output.Question)
	}
	if len(output.Options) != 2 || output.Options[0] != "main" || output.Options[1] != "new branch" {
		t.Fatalf("Options = %#v, want main/new branch", output.Options)
	}
	if output.AllowFreeText {
		t.Fatal("AllowFreeText = true, want false")
	}
	if output.TimeoutSeconds != 120 {
		t.Fatalf("TimeoutSeconds = %d, want 120", output.TimeoutSeconds)
	}
	if output.RequestID == "" {
		t.Fatal("RequestID is empty, want stable question id")
	}
	if !strings.Contains(output.Instruction, "ask the user") {
		t.Fatalf("Instruction = %q, want user-facing guidance", output.Instruction)
	}
	if output.Receipt.Tool != AskUserQuestionToolName || output.Receipt.Action != "request" || !output.Receipt.ReadOnly || output.Receipt.ExecutionPolicy != "user_input_request" {
		t.Fatalf("Receipt identity = %+v", output.Receipt)
	}
	if output.Receipt.Question != "Which branch should I use?" || output.Receipt.QuestionChars != len("Which branch should I use?") || output.Receipt.OptionCount != 2 || output.Receipt.AllowFreeText || output.Receipt.TimeoutSeconds != 120 {
		t.Fatalf("Receipt bounds = %+v", output.Receipt)
	}
	if output.Receipt.RequestID != output.RequestID {
		t.Fatalf("receipt RequestID = %q, want output RequestID %q", output.Receipt.RequestID, output.RequestID)
	}
}

func TestAskUserQuestionToolDefaultsToFreeText(t *testing.T) {
	result, err := NewAskUserQuestionTool().Execute(context.Background(), json.RawMessage(`{
		"question":"What should I do next?"
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output askUserQuestionToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !output.AllowFreeText {
		t.Fatal("AllowFreeText = false, want default true")
	}
	if output.TimeoutSeconds != 0 {
		t.Fatalf("TimeoutSeconds = %d, want 0 for non-blocking default", output.TimeoutSeconds)
	}
}

func TestAskUserQuestionToolIncludesSessionIDWhenBound(t *testing.T) {
	ctx := tools.WithSessionID(context.Background(), "sess-123")
	result, err := NewAskUserQuestionTool().Execute(ctx, json.RawMessage(`{
		"question":"What should I do next?"
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var output askUserQuestionToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.SessionID != "sess-123" || output.Receipt.SessionID != "sess-123" {
		t.Fatalf("session ids = output:%q receipt:%q, want sess-123", output.SessionID, output.Receipt.SessionID)
	}
}

func TestAskUserQuestionToolOmitsSessionIDWhenUnbound(t *testing.T) {
	result, err := NewAskUserQuestionTool().Execute(context.Background(), json.RawMessage(`{
		"question":"What should I do next?"
	}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(result.Output), &raw); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if _, ok := raw["session_id"]; ok {
		t.Fatalf("session_id present in unbound output: %s", result.Output)
	}
	receipt, _ := raw["receipt"].(map[string]any)
	if _, ok := receipt["session_id"]; ok {
		t.Fatalf("receipt session_id present in unbound output: %s", result.Output)
	}
}

func TestAskUserQuestionToolRejectsMissingQuestion(t *testing.T) {
	result, err := NewAskUserQuestionTool().Execute(context.Background(), json.RawMessage(`{"options":["yes"]}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError || !strings.Contains(result.Output, "question is required") {
		t.Fatalf("result = %+v, want required question error", result)
	}
}

func TestAskUserQuestionToolMetadata(t *testing.T) {
	tool := NewAskUserQuestionTool()
	if tool.Name() != AskUserQuestionToolName {
		t.Fatalf("Name() = %q, want %q", tool.Name(), AskUserQuestionToolName)
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Fatal("ask_user_question should be concurrency-safe")
	}
	if !tool.Reversible() {
		t.Fatal("ask_user_question should be reversible")
	}
	if got := tool.Scope(nil); len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 || got.Network || got.Persistent {
		t.Fatalf("Scope(nil) = %+v, want empty scope", got)
	}
	if tool.ShouldCancelSiblingsOnError() {
		t.Fatal("ask_user_question should not cancel siblings")
	}
}
