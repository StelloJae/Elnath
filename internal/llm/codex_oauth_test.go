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

// TestBuildCodexRequest_ToolUseResultPairStructure inspects the exact
// payload structure produced when the chat tool loop re-submits an
// assistant tool_use followed by its tool_result. Dogfood 2026-04-21
// 15:16 hit a Codex 400 "No tool call found for function call output
// with call_id ..." — this test pins the serialisation so a regression
// in call_id propagation or item ordering fails here first.
func TestBuildCodexRequest_ToolUseResultPairStructure(t *testing.T) {
	const wantCallID = "call_wH9JVxyuUUHiADnStfgOacFM"
	body, err := buildCodexRequest(ChatRequest{
		Model: "gpt-5.4",
		Messages: []Message{
			NewUserMessage("오늘 미국 주식 인기종목 3개 알아봐줘"),
			{Role: RoleAssistant, Content: []ContentBlock{
				ToolUseBlock{
					ID:    wantCallID,
					Name:  "web_search",
					Input: json.RawMessage(`{"query":"today US popular stocks"}`),
				},
			}},
			NewToolResultMessage(wantCallID, "AAPL, MSFT, NVDA rankings", false),
		},
	}, "gpt-5.4")
	if err != nil {
		t.Fatalf("buildCodexRequest: %v", err)
	}

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v; raw=%s", err, body)
	}

	t.Logf("codex input payload:")
	for i, item := range payload.Input {
		b, _ := json.Marshal(item)
		t.Logf("  [%d] %s", i, b)
	}

	var callIdx, outputIdx = -1, -1
	for i, item := range payload.Input {
		typ, _ := item["type"].(string)
		cid, _ := item["call_id"].(string)
		if typ == "function_call" && cid == wantCallID {
			callIdx = i
		}
		if typ == "function_call_output" && cid == wantCallID {
			outputIdx = i
		}
	}
	if callIdx < 0 {
		t.Fatalf("function_call item with call_id=%s missing from payload: %s", wantCallID, body)
	}
	if outputIdx < 0 {
		t.Fatalf("function_call_output item with call_id=%s missing from payload: %s", wantCallID, body)
	}
	if callIdx >= outputIdx {
		t.Fatalf("function_call (idx=%d) must precede function_call_output (idx=%d)", callIdx, outputIdx)
	}
}

func TestParseCodexSSE_AllowsLargeLines(t *testing.T) {
	longDelta := strings.Repeat("a", 70_000)
	stream := strings.NewReader(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + longDelta + "\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n",
	)

	var gotText string
	err := parseCodexSSE(stream, func(ev StreamEvent) {
		if ev.Type == EventTextDelta {
			gotText += ev.Content
		}
	})
	if err != nil {
		t.Fatalf("parseCodexSSE: %v", err)
	}
	if gotText != longDelta {
		t.Fatalf("got text length = %d, want %d", len(gotText), len(longDelta))
	}
}
