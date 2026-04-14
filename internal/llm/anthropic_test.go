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

// anthropicSSE joins SSE event blocks with the required double-newline separator.
func anthropicSSE(events ...string) string {
	var b strings.Builder
	for _, ev := range events {
		b.WriteString(ev)
		b.WriteString("\n\n")
	}
	return b.String()
}

// sseEvent formats a single SSE event with an event type line and data line.
func sseEvent(eventType, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s", eventType, data)
}

// newAnthropicTestServer creates an httptest.Server that replies with the given
// SSE body for every POST to /v1/messages, and returns a provider wired to it.
func newAnthropicTestServer(t *testing.T, sseBody string) (*httptest.Server, *AnthropicProvider) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseBody)
	}))
	t.Cleanup(srv.Close)
	p := NewAnthropicProvider("test-key", "claude-test", WithAnthropicBaseURL(srv.URL))
	return srv, p
}

// collectEvents runs Stream and returns all emitted events.
func collectEvents(t *testing.T, p *AnthropicProvider, req Request) ([]StreamEvent, error) {
	t.Helper()
	var events []StreamEvent
	err := p.Stream(context.Background(), req, func(ev StreamEvent) {
		events = append(events, ev)
	})
	return events, err
}

// --- helpers for building SSE payloads ---

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestAnthropicStreamTextDelta verifies that text_delta events are emitted as
// EventTextDelta events and that usage fields are populated from message_start
// and message_delta.
func TestAnthropicStreamTextDelta(t *testing.T) {
	msgStart := mustJSON(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"usage": map[string]any{"input_tokens": 42},
		},
	})
	cbDelta1 := mustJSON(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "text_delta", "text": "Hello"},
	})
	cbDelta2 := mustJSON(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "text_delta", "text": ", world"},
	})
	msgDelta := mustJSON(map[string]any{
		"type":  "message_delta",
		"usage": map[string]any{"output_tokens": 7},
	})
	msgStop := mustJSON(map[string]any{"type": "message_stop"})

	body := anthropicSSE(
		sseEvent("message_start", msgStart),
		sseEvent("content_block_delta", cbDelta1),
		sseEvent("content_block_delta", cbDelta2),
		sseEvent("message_delta", msgDelta),
		sseEvent("message_stop", msgStop),
	)

	_, p := newAnthropicTestServer(t, body)
	events, err := collectEvents(t, p, Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	// Find accumulated text.
	var text string
	var doneEvent *StreamEvent
	for i, ev := range events {
		if ev.Type == EventTextDelta {
			text += ev.Content
		}
		if ev.Type == EventDone {
			doneEvent = &events[i]
		}
	}

	if text != "Hello, world" {
		t.Errorf("accumulated text = %q, want %q", text, "Hello, world")
	}
	if doneEvent == nil {
		t.Fatal("EventDone not emitted")
	}
	if doneEvent.Usage == nil {
		t.Fatal("EventDone.Usage is nil")
	}
	if doneEvent.Usage.InputTokens != 42 {
		t.Errorf("InputTokens = %d, want 42", doneEvent.Usage.InputTokens)
	}
	if doneEvent.Usage.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", doneEvent.Usage.OutputTokens)
	}
}

// TestAnthropicStreamToolUse exercises the full tool_use content block
// lifecycle: content_block_start → content_block_delta (input_json_delta) →
// content_block_stop.
func TestAnthropicStreamToolUse(t *testing.T) {
	const toolID = "toolu_01"
	const toolName = "get_weather"

	cbStart := mustJSON(map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"type": "tool_use",
			"id":   toolID,
			"name": toolName,
		},
	})
	cbDelta1 := mustJSON(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"location`},
	})
	cbDelta2 := mustJSON(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": `":"NYC"}`},
	})
	cbStop := mustJSON(map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	})
	msgStop := mustJSON(map[string]any{"type": "message_stop"})

	body := anthropicSSE(
		sseEvent("content_block_start", cbStart),
		sseEvent("content_block_delta", cbDelta1),
		sseEvent("content_block_delta", cbDelta2),
		sseEvent("content_block_stop", cbStop),
		sseEvent("message_stop", msgStop),
	)

	_, p := newAnthropicTestServer(t, body)
	events, err := collectEvents(t, p, Request{Messages: []Message{NewUserMessage("weather?")}})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var startEv, doneEv *StreamEvent
	var deltaInputs []string
	for i, ev := range events {
		switch ev.Type {
		case EventToolUseStart:
			startEv = &events[i]
		case EventToolUseDelta:
			if ev.ToolCall != nil {
				deltaInputs = append(deltaInputs, ev.ToolCall.Input)
			}
		case EventToolUseDone:
			doneEv = &events[i]
		}
	}

	if startEv == nil {
		t.Fatal("EventToolUseStart not emitted")
	}
	if startEv.ToolCall == nil {
		t.Fatal("EventToolUseStart.ToolCall is nil")
	}
	if startEv.ToolCall.ID != toolID {
		t.Errorf("start ID = %q, want %q", startEv.ToolCall.ID, toolID)
	}
	if startEv.ToolCall.Name != toolName {
		t.Errorf("start Name = %q, want %q", startEv.ToolCall.Name, toolName)
	}

	if len(deltaInputs) != 2 {
		t.Errorf("got %d delta events, want 2", len(deltaInputs))
	}

	if doneEv == nil {
		t.Fatal("EventToolUseDone not emitted")
	}
	if doneEv.ToolCall == nil {
		t.Fatal("EventToolUseDone.ToolCall is nil")
	}
	if doneEv.ToolCall.ID != toolID {
		t.Errorf("done ID = %q, want %q", doneEv.ToolCall.ID, toolID)
	}
	wantInput := `{"location":"NYC"}`
	if doneEv.ToolCall.Input != wantInput {
		t.Errorf("done Input = %q, want %q", doneEv.ToolCall.Input, wantInput)
	}
}

// TestAnthropicStreamMixedContent verifies a stream that contains both text
// deltas and a tool_use block produces all expected event types.
func TestAnthropicStreamMixedContent(t *testing.T) {
	msgStart := mustJSON(map[string]any{
		"type":    "message_start",
		"message": map[string]any{"usage": map[string]any{"input_tokens": 10}},
	})
	// text block at index 0
	textDelta := mustJSON(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "text_delta", "text": "Let me check."},
	})
	// tool block at index 1
	cbStart := mustJSON(map[string]any{
		"type":  "content_block_start",
		"index": 1,
		"content_block": map[string]any{
			"type": "tool_use",
			"id":   "toolu_02",
			"name": "search",
		},
	})
	cbDelta := mustJSON(map[string]any{
		"type":  "content_block_delta",
		"index": 1,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"q":"go"}`},
	})
	cbStop := mustJSON(map[string]any{"type": "content_block_stop", "index": 1})
	msgStop := mustJSON(map[string]any{"type": "message_stop"})

	body := anthropicSSE(
		sseEvent("message_start", msgStart),
		sseEvent("content_block_delta", textDelta),
		sseEvent("content_block_start", cbStart),
		sseEvent("content_block_delta", cbDelta),
		sseEvent("content_block_stop", cbStop),
		sseEvent("message_stop", msgStop),
	)

	_, p := newAnthropicTestServer(t, body)
	events, err := collectEvents(t, p, Request{Messages: []Message{NewUserMessage("search go")}})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var sawText, sawToolStart, sawToolDone, sawDone bool
	for _, ev := range events {
		switch ev.Type {
		case EventTextDelta:
			sawText = true
		case EventToolUseStart:
			sawToolStart = true
		case EventToolUseDone:
			sawToolDone = true
		case EventDone:
			sawDone = true
		}
	}

	if !sawText {
		t.Error("expected EventTextDelta, not seen")
	}
	if !sawToolStart {
		t.Error("expected EventToolUseStart, not seen")
	}
	if !sawToolDone {
		t.Error("expected EventToolUseDone, not seen")
	}
	if !sawDone {
		t.Error("expected EventDone, not seen")
	}
}

// TestAnthropicStreamError verifies that an SSE "error" event causes Stream to
// return a non-nil error containing the error message.
func TestAnthropicStreamError(t *testing.T) {
	errPayload := mustJSON(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "overloaded_error",
			"message": "API is temporarily overloaded",
		},
	})

	body := anthropicSSE(sseEvent("error", errPayload))

	_, p := newAnthropicTestServer(t, body)
	_, err := collectEvents(t, p, Request{Messages: []Message{NewUserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error from SSE error event, got nil")
	}
	if !strings.Contains(err.Error(), "API is temporarily overloaded") {
		t.Errorf("error message = %q, want it to contain %q", err.Error(), "API is temporarily overloaded")
	}
}

// TestAnthropicHTTPErrors is table-driven and verifies that non-200 HTTP
// responses produce the correct error text.
func TestAnthropicHTTPErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantSubstr string
	}{
		{
			name:       "rate_limit_429",
			statusCode: 429,
			body:       `{"error":{"type":"rate_limit_error","message":"rate limit hit"}}`,
			wantSubstr: "rate limit (429)",
		},
		{
			name:       "overloaded_529",
			statusCode: 529,
			body:       `{"error":{"type":"overloaded_error","message":"overloaded"}}`,
			wantSubstr: "overloaded (529)",
		},
		{
			name:       "server_error_500",
			statusCode: 500,
			body:       `{"error":{"type":"api_error","message":"internal server error"}}`,
			wantSubstr: "status 500",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(srv.Close)

			p := NewAnthropicProvider("test-key", "claude-test", WithAnthropicBaseURL(srv.URL))
			_, err := collectEvents(t, p, Request{Messages: []Message{NewUserMessage("hi")}})
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tc.statusCode)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestAnthropicChat verifies that Chat() accumulates the stream into a
// ChatResponse with the correct Content, StopReason, and Usage.
func TestAnthropicChat(t *testing.T) {
	msgStart := mustJSON(map[string]any{
		"type":    "message_start",
		"message": map[string]any{"usage": map[string]any{"input_tokens": 15}},
	})
	cbDelta := mustJSON(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "text_delta", "text": "Sure, I can help!"},
	})
	msgDelta := mustJSON(map[string]any{
		"type":  "message_delta",
		"usage": map[string]any{"output_tokens": 5},
	})
	msgStop := mustJSON(map[string]any{"type": "message_stop"})

	body := anthropicSSE(
		sseEvent("message_start", msgStart),
		sseEvent("content_block_delta", cbDelta),
		sseEvent("message_delta", msgDelta),
		sseEvent("message_stop", msgStop),
	)

	_, p := newAnthropicTestServer(t, body)
	resp, err := p.Chat(context.Background(), Request{Messages: []Message{NewUserMessage("help")}})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "Sure, I can help!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Sure, I can help!")
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "end_turn")
	}
	if resp.Usage.InputTokens != 15 {
		t.Errorf("InputTokens = %d, want 15", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", resp.Usage.OutputTokens)
	}
}

// TestAnthropicChatToolUse verifies that Chat() populates ToolCalls and sets
// StopReason to "tool_use" when the model calls a tool.
func TestAnthropicChatToolUse(t *testing.T) {
	const toolID = "toolu_03"
	const toolName = "calculator"

	cbStart := mustJSON(map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"type": "tool_use",
			"id":   toolID,
			"name": toolName,
		},
	})
	cbDelta := mustJSON(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"expr":"2+2"}`},
	})
	cbStop := mustJSON(map[string]any{"type": "content_block_stop", "index": 0})
	msgDelta := mustJSON(map[string]any{
		"type":  "message_delta",
		"usage": map[string]any{"output_tokens": 20},
	})
	msgStop := mustJSON(map[string]any{"type": "message_stop"})

	body := anthropicSSE(
		sseEvent("content_block_start", cbStart),
		sseEvent("content_block_delta", cbDelta),
		sseEvent("content_block_stop", cbStop),
		sseEvent("message_delta", msgDelta),
		sseEvent("message_stop", msgStop),
	)

	_, p := newAnthropicTestServer(t, body)
	resp, err := p.Chat(context.Background(), Request{Messages: []Message{NewUserMessage("2+2")}})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "tool_use")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != toolID {
		t.Errorf("ToolCall.ID = %q, want %q", tc.ID, toolID)
	}
	if tc.Name != toolName {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, toolName)
	}
	if tc.Input != `{"expr":"2+2"}` {
		t.Errorf("ToolCall.Input = %q, want %q", tc.Input, `{"expr":"2+2"}`)
	}
}

// TestBuildAnthropicRequest verifies the JSON structure produced for a fully
// specified Request: model, max_tokens, system, stream flag, tools, messages.
func TestBuildAnthropicRequest(t *testing.T) {
	req := Request{
		Model:     "claude-opus-4-6",
		MaxTokens: 1024,
		System:    "You are a test assistant.",
		Messages:  []Message{NewUserMessage("ping")},
		Tools: []ToolDef{
			{
				Name:        "echo",
				Description: "echoes input",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			},
		},
	}

	raw, err := buildAnthropicRequest(req, "claude-default")
	if err != nil {
		t.Fatalf("buildAnthropicRequest() error: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	checkStringField := func(key, want string) {
		t.Helper()
		var val string
		if err := json.Unmarshal(got[key], &val); err != nil {
			t.Errorf("field %q: %v", key, err)
			return
		}
		if val != want {
			t.Errorf("field %q = %q, want %q", key, val, want)
		}
	}
	checkBoolField := func(key string, want bool) {
		t.Helper()
		var val bool
		if err := json.Unmarshal(got[key], &val); err != nil {
			t.Errorf("field %q: %v", key, err)
			return
		}
		if val != want {
			t.Errorf("field %q = %v, want %v", key, val, want)
		}
	}
	checkIntField := func(key string, want int) {
		t.Helper()
		var val int
		if err := json.Unmarshal(got[key], &val); err != nil {
			t.Errorf("field %q: %v", key, err)
			return
		}
		if val != want {
			t.Errorf("field %q = %d, want %d", key, val, want)
		}
	}

	checkStringField("model", "claude-opus-4-6")
	checkBoolField("stream", true)
	checkIntField("max_tokens", 1024)

	// Verify system is an array with text block.
	var sysBlocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(got["system"], &sysBlocks); err != nil {
		t.Errorf("system field: %v", err)
	} else if len(sysBlocks) != 1 || sysBlocks[0].Text != "You are a test assistant." {
		t.Errorf("system text = %q, want %q", sysBlocks[0].Text, "You are a test assistant.")
	}

	// Verify tools array has one entry with the expected name.
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(got["tools"], &tools); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	var toolName string
	if err := json.Unmarshal(tools[0]["name"], &toolName); err != nil {
		t.Fatalf("unmarshal tool name: %v", err)
	}
	if toolName != "echo" {
		t.Errorf("tool name = %q, want %q", toolName, "echo")
	}

	// Verify messages array has one entry.
	var msgs []json.RawMessage
	if err := json.Unmarshal(got["messages"], &msgs); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("messages len = %d, want 1", len(msgs))
	}
}

// TestBuildAnthropicRequestDefaults verifies that an empty model falls back to
// the defaultModel argument and that max_tokens defaults to 8192.
func TestBuildAnthropicRequestDefaults(t *testing.T) {
	req := Request{
		// Model and MaxTokens intentionally omitted.
		Messages: []Message{NewUserMessage("hello")},
	}

	raw, err := buildAnthropicRequest(req, "claude-fallback")
	if err != nil {
		t.Fatalf("buildAnthropicRequest() error: %v", err)
	}

	var got struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "claude-fallback" {
		t.Errorf("model = %q, want %q", got.Model, "claude-fallback")
	}
	if got.MaxTokens != 8192 {
		t.Errorf("max_tokens = %d, want 8192", got.MaxTokens)
	}
}

// TestAnthropicProviderMetadata verifies Name() and Models() return the
// expected values.
func TestAnthropicProviderMetadata(t *testing.T) {
	p := NewAnthropicProvider("key", "claude-sonnet-4-6")

	if got := p.Name(); got != "anthropic" {
		t.Errorf("Name() = %q, want %q", got, "anthropic")
	}

	models := p.Models()
	if len(models) == 0 {
		t.Fatal("Models() returned empty slice")
	}

	wantIDs := map[string]bool{
		"claude-opus-4-6":   true,
		"claude-sonnet-4-6": true,
		"claude-haiku-4-5":  true,
	}
	for _, m := range models {
		if !wantIDs[m.ID] {
			t.Errorf("unexpected model ID %q in Models()", m.ID)
		}
		delete(wantIDs, m.ID)
	}
	for id := range wantIDs {
		t.Errorf("expected model ID %q not found in Models()", id)
	}
}

// TestAnthropicOAuthHeaders verifies that Stream swaps to OAuth-style headers
// when the API key is a Claude Code OAuth access token (sk-ant-oat01- prefix):
// Authorization: Bearer, anthropic-beta claude-code flag, user-agent, x-app,
// and no x-api-key.
func TestAnthropicOAuthHeaders(t *testing.T) {
	var capturedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		msgStop := mustJSON(map[string]any{"type": "message_stop"})
		fmt.Fprint(w, anthropicSSE(sseEvent("message_stop", msgStop)))
	}))
	t.Cleanup(srv.Close)

	const oauthToken = "sk-ant-oat01-dummy-oauth-token"
	p := NewAnthropicProvider(oauthToken, "claude-test", WithAnthropicBaseURL(srv.URL))
	_, err := collectEvents(t, p, Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	if got := capturedHeaders.Get("Authorization"); got != "Bearer "+oauthToken {
		t.Errorf("Authorization = %q, want %q", got, "Bearer "+oauthToken)
	}
	if got := capturedHeaders.Get("Anthropic-Beta"); got != anthropicOAuthBeta {
		t.Errorf("anthropic-beta = %q, want %q", got, anthropicOAuthBeta)
	}
	if got := capturedHeaders.Get("User-Agent"); got != anthropicOAuthUserAgent {
		t.Errorf("user-agent = %q, want %q", got, anthropicOAuthUserAgent)
	}
	if got := capturedHeaders.Get("X-App"); got != "cli" {
		t.Errorf("x-app = %q, want %q", got, "cli")
	}
	if got := capturedHeaders.Get("X-Api-Key"); got != "" {
		t.Errorf("x-api-key = %q, want empty (OAuth mode must not send x-api-key)", got)
	}
}

// TestAnthropicRequestHeaders verifies that Stream sends the required HTTP
// headers: x-api-key, anthropic-version, and Content-Type.
func TestAnthropicRequestHeaders(t *testing.T) {
	var capturedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		// Return a minimal valid SSE response so Stream doesn't block.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		msgStop := mustJSON(map[string]any{"type": "message_stop"})
		fmt.Fprint(w, anthropicSSE(sseEvent("message_stop", msgStop)))
	}))
	t.Cleanup(srv.Close)

	const testKey = "sk-ant-test-12345"
	p := NewAnthropicProvider(testKey, "claude-test", WithAnthropicBaseURL(srv.URL))
	_, err := collectEvents(t, p, Request{Messages: []Message{NewUserMessage("hi")}})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	checks := []struct {
		header string
		want   string
	}{
		{"X-Api-Key", testKey},
		{"Anthropic-Version", anthropicAPIVersion},
		{"Content-Type", "application/json"},
	}
	for _, c := range checks {
		got := capturedHeaders.Get(c.header)
		if got != c.want {
			t.Errorf("header %q = %q, want %q", c.header, got, c.want)
		}
	}
}
