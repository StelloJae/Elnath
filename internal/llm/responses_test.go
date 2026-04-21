package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseLines builds a valid SSE response body from a slice of JSON-encodable event maps.
func sseLines(events []map[string]interface{}) string {
	var sb strings.Builder
	for _, ev := range events {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(&sb, "data: %s\n\n", data)
	}
	return sb.String()
}

func newResponsesProvider(baseURL string) *ResponsesProvider {
	return NewResponsesProvider("test-token", "codex-mini", "acct-1",
		WithResponsesBaseURL(baseURL))
}

// --- Stream: text delta ---

func TestResponsesStreamText(t *testing.T) {
	events := []map[string]interface{}{
		{"type": "response.output_text.delta", "delta": "Hello"},
		{"type": "response.output_text.delta", "delta": ", world"},
		{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":     "resp_1",
				"output": []interface{}{},
				"usage":  map[string]interface{}{"input_tokens": 10, "output_tokens": 5},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseLines(events))
	}))
	defer srv.Close()

	p := newResponsesProvider(srv.URL)

	var textParts []string
	var gotDone bool
	err := p.Stream(context.Background(), ChatRequest{Messages: []Message{NewUserMessage("hi")}}, func(ev StreamEvent) {
		switch ev.Type {
		case EventTextDelta:
			textParts = append(textParts, ev.Content)
		case EventDone:
			gotDone = true
		}
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if !gotDone {
		t.Error("EventDone not received")
	}
	if got := strings.Join(textParts, ""); got != "Hello, world" {
		t.Errorf("text = %q, want %q", got, "Hello, world")
	}
}

// --- Stream: tool use ---

func TestResponsesStreamToolUse(t *testing.T) {
	events := []map[string]interface{}{
		{
			"type": "response.output_item.added",
			"item": map[string]interface{}{
				"type":    "function_call",
				"id":      "item_1",
				"call_id": "call_abc",
				"name":    "bash",
			},
		},
		{"type": "response.function_call_arguments.delta", "item_id": "item_1", "delta": `{"cmd"`},
		{"type": "response.function_call_arguments.delta", "item_id": "item_1", "delta": `:"ls"}`},
		{"type": "response.function_call_arguments.done", "item_id": "item_1", "arguments": `{"cmd":"ls"}`},
		{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":     "resp_2",
				"output": []interface{}{},
				"usage":  map[string]interface{}{"input_tokens": 20, "output_tokens": 8},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseLines(events))
	}))
	defer srv.Close()

	p := newResponsesProvider(srv.URL)

	type toolEvent struct {
		evType StreamEventType
		call   ToolUseEvent
	}
	var toolEvents []toolEvent

	err := p.Stream(context.Background(), ChatRequest{Messages: []Message{NewUserMessage("run ls")}}, func(ev StreamEvent) {
		switch ev.Type {
		case EventToolUseStart, EventToolUseDelta, EventToolUseDone:
			if ev.ToolCall != nil {
				toolEvents = append(toolEvents, toolEvent{evType: ev.Type, call: *ev.ToolCall})
			}
		}
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	if len(toolEvents) < 3 {
		t.Fatalf("tool events len = %d, want >= 3", len(toolEvents))
	}

	start := toolEvents[0]
	if start.evType != EventToolUseStart {
		t.Errorf("first event type = %v, want EventToolUseStart", start.evType)
	}
	if start.call.ID != "call_abc" || start.call.Name != "bash" {
		t.Errorf("start call = {%q %q}, want {call_abc bash}", start.call.ID, start.call.Name)
	}

	done := toolEvents[len(toolEvents)-1]
	if done.evType != EventToolUseDone {
		t.Errorf("last event type = %v, want EventToolUseDone", done.evType)
	}
	if done.call.Input != `{"cmd":"ls"}` {
		t.Errorf("done input = %q, want {\"cmd\":\"ls\"}", done.call.Input)
	}
}

// --- Chat: text response ---

func TestResponsesChat(t *testing.T) {
	events := []map[string]interface{}{
		{"type": "response.output_text.delta", "delta": "Sure thing."},
		{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":     "resp_3",
				"output": []interface{}{},
				"usage":  map[string]interface{}{"input_tokens": 15, "output_tokens": 3},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseLines(events))
	}))
	defer srv.Close()

	p := newResponsesProvider(srv.URL)
	resp, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{NewUserMessage("hello")}})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Content != "Sure thing." {
		t.Errorf("content = %q, want %q", resp.Content, "Sure thing.")
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", resp.StopReason)
	}
	if resp.Usage.InputTokens != 15 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage = {%d %d}, want {15 3}", resp.Usage.InputTokens, resp.Usage.OutputTokens)
	}
}

// --- Chat: tool use response ---

func TestResponsesChatToolUse(t *testing.T) {
	events := []map[string]interface{}{
		{
			"type": "response.output_item.added",
			"item": map[string]interface{}{
				"type":    "function_call",
				"id":      "item_2",
				"call_id": "call_xyz",
				"name":    "read",
			},
		},
		{"type": "response.function_call_arguments.delta", "item_id": "item_2", "delta": `{"path":"/etc"}`},
		{"type": "response.function_call_arguments.done", "item_id": "item_2", "arguments": `{"path":"/etc"}`},
		{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":     "resp_4",
				"output": []interface{}{},
				"usage":  map[string]interface{}{"input_tokens": 30, "output_tokens": 12},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseLines(events))
	}))
	defer srv.Close()

	p := newResponsesProvider(srv.URL)
	resp, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{NewUserMessage("read /etc")}})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_xyz" || tc.Name != "read" {
		t.Errorf("tool_call = {%q %q}, want {call_xyz read}", tc.ID, tc.Name)
	}
	if tc.Input != `{"path":"/etc"}` {
		t.Errorf("input = %q, want {\"path\":\"/etc\"}", tc.Input)
	}
}

// --- HTTP error ---

func TestResponsesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := newResponsesProvider(srv.URL)
	_, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{NewUserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error for non-200 response, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want to contain '401'", err.Error())
	}
}

// --- Request structure ---

func TestResponsesBuildRequest(t *testing.T) {
	p := newResponsesProvider("http://localhost")

	schema := json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)
	req := ChatRequest{
		Model:  "codex-mini",
		System: "You are a test assistant.",
		Messages: []Message{
			NewUserMessage("run something"),
		},
		Tools: []ToolDef{
			{Name: "bash", Description: "run shell", InputSchema: schema},
		},
		Temperature: 0.7,
	}

	body := p.buildRequest(req, true)

	if body["model"] != "codex-mini" {
		t.Errorf("model = %v, want codex-mini", body["model"])
	}
	if body["instructions"] != "You are a test assistant." {
		t.Errorf("instructions = %v, want system prompt", body["instructions"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if body["store"] != false {
		t.Errorf("store = %v, want false", body["store"])
	}
	if body["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", body["temperature"])
	}

	tools, ok := body["tools"].([]map[string]interface{})
	if !ok {
		t.Fatalf("tools type = %T, want []map", body["tools"])
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	if tools[0]["name"] != "bash" {
		t.Errorf("tool name = %v, want bash", tools[0]["name"])
	}

	input, ok := body["input"].([]interface{})
	if !ok {
		t.Fatalf("input type = %T, want []interface{}", body["input"])
	}
	if len(input) == 0 {
		t.Error("input is empty, want at least one entry")
	}
}

func TestResponsesBuildRequestDefaultInstructions(t *testing.T) {
	p := newResponsesProvider("http://localhost")
	body := p.buildRequest(ChatRequest{Messages: []Message{NewUserMessage("hi")}}, false)
	if body["instructions"] != "You are a helpful assistant." {
		t.Errorf("instructions = %v, want default", body["instructions"])
	}
	if _, hasStream := body["stream"]; hasStream {
		t.Error("stream should be absent when stream=false")
	}
}

// TestResponsesBuildRequest_WebSearchEmitsNativeSchema pins the native-tool
// emission rule for the Responses API. When any ChatRequest tool carries
// Name=="web_search", the wire payload must include a
// {"type":"web_search", "external_web_access":true, "search_context_size":"medium"}
// entry instead of the generic {"type":"function", "name":"web_search", ...}
// form — otherwise OpenAI will execute Elnath's own DDG-scrape fallback
// instead of the server-side search primitive the partner actually saw
// working in the upstream Codex CLI.
//
// Reference: /Users/stello/codex/codex-rs/tools/src/tool_spec.rs:39-51
// (WebSearch variant with external_web_access / filters / user_location /
// search_context_size / search_content_types fields). Elnath only emits the
// two most load-bearing fields here; filters/user_location are Phase B.2+.
//
// TODO(Phase B.2 live probe): Codex comment at tool_spec.rs:33 warns that
// some model/API combinations reject "web_search"; see plan §6 open #1.
func TestResponsesBuildRequest_WebSearchEmitsNativeSchema(t *testing.T) {
	p := newResponsesProvider("http://localhost")

	req := ChatRequest{
		Model:    "gpt-5.4",
		Messages: []Message{NewUserMessage("search something")},
		Tools: []ToolDef{
			{Name: "bash", Description: "run shell", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "web_search", Description: "search the web", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	body := p.buildRequest(req, true)

	tools, ok := body["tools"].([]map[string]interface{})
	if !ok {
		t.Fatalf("tools type = %T, want []map[string]interface{}", body["tools"])
	}
	if len(tools) != 2 {
		t.Fatalf("tools len = %d, want 2", len(tools))
	}

	var bashTool, webSearchTool map[string]interface{}
	for _, tool := range tools {
		switch tool["type"] {
		case "function":
			if tool["name"] == "bash" {
				bashTool = tool
			}
		case "web_search":
			webSearchTool = tool
		}
	}

	if bashTool == nil {
		t.Fatalf("function-typed bash tool missing; tools = %v", tools)
	}
	if webSearchTool == nil {
		t.Fatalf("native web_search tool missing (still emitted as function?); tools = %v", tools)
	}

	// Native web_search should NOT carry function-style metadata — those
	// fields are meaningless to the server tool and leaking them invalidates
	// the contract with the Responses API. Partner-facing: preserving
	// "name"/"parameters" here has historically led to silent fallback to the
	// Elnath DDG scrape implementation.
	if _, hasName := webSearchTool["name"]; hasName {
		t.Errorf(`web_search tool has "name" field; native server tool should only carry type + config`)
	}
	if _, hasParams := webSearchTool["parameters"]; hasParams {
		t.Errorf(`web_search tool has "parameters" field; native server tool should only carry type + config`)
	}

	if got, want := webSearchTool["external_web_access"], true; got != want {
		t.Errorf("external_web_access = %v, want %v (live web access)", got, want)
	}
	if got, want := webSearchTool["search_context_size"], "medium"; got != want {
		t.Errorf("search_context_size = %v, want %q", got, want)
	}
}

// --- Provider metadata ---

func TestResponsesProviderMetadata(t *testing.T) {
	p := newResponsesProvider("http://localhost")
	if p.Name() != "openai-responses" {
		t.Errorf("Name() = %q, want openai-responses", p.Name())
	}
	models := p.Models()
	if len(models) != 1 {
		t.Fatalf("Models() len = %d, want 1", len(models))
	}
	if models[0].ID != "codex-mini" {
		t.Errorf("model ID = %q, want codex-mini", models[0].ID)
	}
	if models[0].MaxTokens <= 0 {
		t.Errorf("MaxTokens = %d, want > 0", models[0].MaxTokens)
	}
}
