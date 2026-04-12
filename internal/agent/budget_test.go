package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

func TestBudgetPressureAt70Percent(t *testing.T) {
	t.Parallel()

	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "loop_tool", schema: json.RawMessage(`{}`)})

	var requests []llm.ChatRequest
	callCount := 0
	p := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			requests = append(requests, req)
			callCount++
			if callCount <= 7 {
				toolID := "tool_" + strings.Repeat("x", 0)
				toolID = "tool_" + string(rune('0'+callCount))
				cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: toolID, Name: "loop_tool"}})
				cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: toolID, Name: "loop_tool", Input: `{}`}})
			} else {
				cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
			}
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
			return nil
		},
	}

	a := New(p, reg, WithMaxIterations(10))
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !messagesContainText(result.Messages, "[BUDGET:") {
		t.Fatalf("messages = %#v, want budget message", result.Messages)
	}
	if !requestContainsText(requests[len(requests)-1], "[BUDGET:") {
		t.Fatalf("last request missing budget pressure message")
	}
}

func TestBudgetPressureAt90Percent(t *testing.T) {
	t.Parallel()

	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "loop_tool", schema: json.RawMessage(`{}`)})

	var requests []llm.ChatRequest
	callCount := 0
	p := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			requests = append(requests, req)
			callCount++
			if callCount <= 9 {
				toolID := "tool_" + string(rune('0'+callCount))
				cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: toolID, Name: "loop_tool"}})
				cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: toolID, Name: "loop_tool", Input: `{}`}})
			} else {
				cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
			}
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
			return nil
		},
	}

	a := New(p, reg, WithMaxIterations(10))
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !messagesContainText(result.Messages, "[BUDGET WARNING:") {
		t.Fatalf("messages = %#v, want budget warning message", result.Messages)
	}
	if !requestContainsText(requests[len(requests)-1], "[BUDGET WARNING:") {
		t.Fatalf("last request missing budget warning message")
	}
}

func TestNoBudgetPressureBelow70(t *testing.T) {
	t.Parallel()

	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "loop_tool", schema: json.RawMessage(`{}`)})

	p := &mockProvider{}
	callCount := 0
	p.streamFn = func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
		callCount++
		if requestContainsText(req, "[BUDGET") {
			t.Fatalf("request %d unexpectedly contained budget message", callCount)
		}
		if callCount <= 5 {
			toolID := "tool_" + string(rune('0'+callCount))
			cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: toolID, Name: "loop_tool"}})
			cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: toolID, Name: "loop_tool", Input: `{}`}})
		} else {
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
		}
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
		return nil
	}

	a := New(p, reg, WithMaxIterations(10))
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if messagesContainText(result.Messages, "[BUDGET") {
		t.Fatalf("messages unexpectedly contained budget pressure: %#v", result.Messages)
	}
}

func TestAckContinuationDetected(t *testing.T) {
	t.Parallel()

	var requests []llm.ChatRequest
	callCount := 0
	p := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			requests = append(requests, req)
			callCount++
			if callCount == 1 {
				cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "I'll look into the file"})
			} else {
				cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "done"})
			}
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
			return nil
		},
	}

	a := New(p, tools.NewRegistry(), WithMaxIterations(10))
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("provider call count = %d, want 2", callCount)
	}
	if !messagesContainText(result.Messages, "[System: Continue now") {
		t.Fatalf("messages = %#v, want continuation injection", result.Messages)
	}
	if !requestContainsText(requests[1], "[System: Continue now") {
		t.Fatalf("second request missing continuation injection")
	}
}

func TestAckContinuationMaxRetries(t *testing.T) {
	t.Parallel()

	callCount := 0
	p := &mockProvider{
		streamFn: func(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
			callCount++
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: "I'll look into the file"})
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
			return nil
		},
	}

	a := New(p, tools.NewRegistry(), WithMaxIterations(10))
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("provider call count = %d, want 3", callCount)
	}
	if got := countMessagesContaining(result.Messages, "[System: Continue now"); got != 2 {
		t.Fatalf("continuation message count = %d, want 2", got)
	}
	last := result.Messages[len(result.Messages)-1]
	if last.Role != llm.RoleAssistant || last.Text() != "I'll look into the file" {
		t.Fatalf("last message = %#v, want final assistant ack", last)
	}
}

func TestLongResponseNotAck(t *testing.T) {
	t.Parallel()

	callCount := 0
	longText := strings.Repeat("x", 600)
	p := &mockProvider{
		streamFn: func(_ context.Context, req llm.ChatRequest, cb func(llm.StreamEvent)) error {
			callCount++
			if requestContainsText(req, "[System: Continue now") {
				t.Fatal("long response should not trigger continuation injection")
			}
			cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: longText})
			cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 1, OutputTokens: 1}})
			return nil
		},
	}

	a := New(p, tools.NewRegistry(), WithMaxIterations(10))
	result, err := a.Run(context.Background(), []llm.Message{llm.NewUserMessage("go")}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("provider call count = %d, want 1", callCount)
	}
	if messagesContainText(result.Messages, "[System: Continue now") {
		t.Fatalf("messages unexpectedly contained continuation injection: %#v", result.Messages)
	}
}

func TestToolResultTruncation(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("x", 60_000)
	messages := []llm.Message{llm.NewUserMessage("go")}
	messages = llm.AppendToolResult(messages, "tool-1", content, false)

	truncateToolResults(messages)

	block := messages[len(messages)-1].Content[0].(llm.ToolResultBlock)
	if len(block.Content) >= len(content) {
		t.Fatalf("tool result length = %d, want truncated from %d", len(block.Content), len(content))
	}
	if len(block.Content) < 2000 {
		t.Fatalf("tool result length = %d, want preview + notice", len(block.Content))
	}
	if !strings.Contains(block.Content, "[Output truncated. 60000 total characters.") {
		t.Fatalf("tool result = %q, want truncation notice", block.Content)
	}
}

func TestToolResultTotalCap(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("x", 80_000)
	messages := []llm.Message{llm.NewUserMessage("go")}
	for i := 0; i < 3; i++ {
		messages = llm.AppendToolResult(messages, "tool-"+string(rune('1'+i)), content, false)
	}

	truncateToolResults(messages)

	last := messages[len(messages)-1]
	total := 0
	for _, block := range last.Content {
		tr := block.(llm.ToolResultBlock)
		total += len(tr.Content)
	}
	if total > toolResultTotalLimit {
		t.Fatalf("total tool result length = %d, want <= %d", total, toolResultTotalLimit)
	}
}

func requestContainsText(req llm.ChatRequest, needle string) bool {
	return messagesContainText(req.Messages, needle)
}

func messagesContainText(messages []llm.Message, needle string) bool {
	return countMessagesContaining(messages, needle) > 0
}

func countMessagesContaining(messages []llm.Message, needle string) int {
	count := 0
	for _, msg := range messages {
		if strings.Contains(msg.Text(), needle) {
			count++
		}
	}
	return count
}
