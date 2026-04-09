package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseCodexSSE_TextDoneFallback(t *testing.T) {
	stream := strings.NewReader("data: {\"type\":\"response.output_text.done\",\"text\":\"Hello from done\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":4}}}\n\n")

	var gotText string
	var gotDone bool
	err := parseCodexSSE(stream, func(ev StreamEvent) {
		switch ev.Type {
		case EventTextDelta:
			gotText += ev.Content
		case EventDone:
			gotDone = true
		}
	})
	if err != nil {
		t.Fatalf("parseCodexSSE: %v", err)
	}
	if gotText != "Hello from done" {
		t.Fatalf("gotText = %q, want %q", gotText, "Hello from done")
	}
	if !gotDone {
		t.Fatal("expected EventDone")
	}
}

func TestParseCodexSSE_OutputItemAddedFunctionCall(t *testing.T) {
	stream := strings.NewReader(
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"item_1\",\"call_id\":\"call_1\",\"name\":\"glob\",\"arguments\":\"\"}}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\"{\\\"pattern\\\":\\\"*.go\\\"}\"}\n\n" +
			"data: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"item_1\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":4}}}\n\n",
	)

	var starts, dones int
	var finalInput string
	err := parseCodexSSE(stream, func(ev StreamEvent) {
		switch ev.Type {
		case EventToolUseStart:
			starts++
			if ev.ToolCall == nil || ev.ToolCall.Name != "glob" || ev.ToolCall.ID != "call_1" {
				t.Fatalf("unexpected start event: %+v", ev.ToolCall)
			}
		case EventToolUseDone:
			dones++
			if ev.ToolCall != nil {
				finalInput = ev.ToolCall.Input
			}
		}
	})
	if err != nil {
		t.Fatalf("parseCodexSSE: %v", err)
	}
	if starts != 1 || dones != 1 {
		t.Fatalf("starts=%d dones=%d", starts, dones)
	}
	if !strings.Contains(finalInput, "pattern") {
		t.Fatalf("unexpected final input: %q", finalInput)
	}
}

func TestBuildCodexRequestUsesCallIDForFunctionHistory(t *testing.T) {
	body, err := buildCodexRequest(ChatRequest{
		Model: "gpt-5.4",
		Messages: []Message{
			NewUserMessage("hi"),
			{Role: RoleAssistant, Content: []ContentBlock{
				ToolUseBlock{ID: "call_1", Name: "glob", Input: json.RawMessage(`{"pattern":"*.go"}`)},
			}},
			NewToolResultMessage("call_1", "file.go", false),
		},
	}, "gpt-5.4")
	if err != nil {
		t.Fatalf("buildCodexRequest: %v", err)
	}
	raw := string(body)
	if !strings.Contains(raw, `"call_id":"call_1"`) {
		t.Fatalf("expected call_id in request: %s", raw)
	}
	if strings.Contains(raw, `"id":"call_1"`) {
		t.Fatalf("did not expect legacy id field for function call: %s", raw)
	}
}
