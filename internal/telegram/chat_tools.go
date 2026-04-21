package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/llm"
)

const maxChatToolIterations = 5

// kstZone fixes Korea Standard Time at UTC+9 (no DST) for the chat-time
// header. FixedZone avoids tzdata dependency at the cost of locking the
// chat surface to KST — acceptable while Telegram is the partner's primary
// surface and the daemon runs locally in Korea.
var kstZone = time.FixedZone("KST", 9*60*60)

// chatTimeHeader formats the current wall-clock time for inclusion at the
// top of the chat system prompt. Anchors the model's notion of "now" so
// time/date questions don't need a tool round-trip and so cutoff-bound
// reasoning ("today", "this week") aligns with the user's actual context.
func chatTimeHeader(now time.Time) string {
	return fmt.Sprintf("현재 시간 (KST): %s\n", now.In(kstZone).Format("2006-01-02 15:04"))
}

// chatToolGuideHeader is appended to the system prompt only when the tool
// loop is active. Codex/gpt-5.4 in chat mode is conservative about emitting
// tool_use without an explicit cue; without this nudge the model answers
// from its knowledge cutoff (the FU-CR2b loop never fires). The phrasing
// names the actual allowlisted verbs so the model has concrete vocabulary
// to plan around.
func chatToolGuideHeader() string {
	return "외부 정보 (실시간 데이터, 뉴스, 특정 URL 내용, 현재 환경)가 필요하면 추측하지 말고 사용 가능한 도구 (web_fetch, web_search, read_file, glob, grep)를 호출해서 답하세요. 도구 결과를 받은 뒤 사용자에게 자연스럽게 한국어로 정리해 답하세요.\n"
}

// prependChatHeaders prepends the time header (always) and the tool guide
// (only when the chat tool loop is active) to whatever systemPrompt the
// builder/legacy fallback produced. Pre-pending keeps the headers at the
// most salient position; the builder's persona/identity/wiki content
// follows.
func (c *ChatResponder) prependChatHeaders(systemPrompt string) string {
	header := chatTimeHeader(c.nowFunc())
	if c.useToolLoop() {
		header += chatToolGuideHeader()
	}
	if systemPrompt == "" {
		return strings.TrimRight(header, "\n")
	}
	return header + "\n" + systemPrompt
}

// DefaultChatToolAllowlist is the read-only subset of tool names safe to expose
// in the Telegram chat path. Destructive tools (bash, write_file, edit_file,
// git, create_skill) are deliberately excluded — chat bypasses the agent loop's
// permission gate, so only side-effect-free tools may be invoked here. Any
// addition to this list MUST be reviewed for chat-context safety.
var DefaultChatToolAllowlist = []string{
	"read_file",
	"glob",
	"grep",
	"web_fetch",
	"web_search",
}

// FilterChatToolDefs returns the subset of defs whose names appear in
// allowlist. Input order is preserved.
func FilterChatToolDefs(defs []llm.ToolDef, allowlist []string) []llm.ToolDef {
	if len(defs) == 0 || len(allowlist) == 0 {
		return nil
	}
	permit := make(map[string]struct{}, len(allowlist))
	for _, n := range allowlist {
		permit[n] = struct{}{}
	}
	out := make([]llm.ToolDef, 0, len(defs))
	for _, d := range defs {
		if _, ok := permit[d.Name]; ok {
			out = append(out, d)
		}
	}
	return out
}

// runStreamWithTools drives the chat-only agent-lite loop:
//
//  1. Stream the provider with current messages + curated ToolDefs.
//  2. If the model emits tool_use blocks, execute each via ToolExecutor and
//     append a tool_result; then re-stream with the updated history.
//  3. Stop when no tool_use is requested OR maxChatToolIterations is reached.
//
// The caller owns sc lifecycle; this helper writes deltas via sc.Send but
// never calls sc.Finish — Respond closes the consumer after we return.
func (c *ChatResponder) runStreamWithTools(
	ctx context.Context,
	initialMessages []llm.Message,
	systemPrompt string,
	sc *StreamConsumer,
) (string, error) {
	messages := make([]llm.Message, 0, len(initialMessages)+2*maxChatToolIterations)
	messages = append(messages, initialMessages...)

	var fullText strings.Builder

	for iter := 0; iter < maxChatToolIterations; iter++ {
		req := llm.ChatRequest{
			Messages:    messages,
			MaxTokens:   1024,
			Temperature: 0.7,
			System:      systemPrompt,
			Tools:       c.pipeline.ToolDefs,
		}

		stepText, toolCalls, err := c.streamOneStep(ctx, req, sc)
		if err != nil {
			return fullText.String(), err
		}
		fullText.WriteString(stepText)

		if len(toolCalls) == 0 {
			return fullText.String(), nil
		}

		var textParts []string
		if stepText != "" {
			textParts = []string{stepText}
		}
		messages = append(messages, llm.BuildAssistantMessage(textParts, toolCalls))

		for _, tc := range toolCalls {
			content, isError := c.executeChatTool(ctx, tc)
			messages = llm.AppendToolResult(messages, tc.ID, content, isError)
		}
	}

	return fullText.String(), fmt.Errorf("chat tool loop exceeded max iterations (%d)", maxChatToolIterations)
}

func (c *ChatResponder) streamOneStep(ctx context.Context, req llm.ChatRequest, sc *StreamConsumer) (string, []llm.CompletedToolCall, error) {
	var (
		stepText     strings.Builder
		toolCalls    []llm.CompletedToolCall
		pendingTools = map[string]*llm.CompletedToolCall{}
	)

	err := c.provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		switch ev.Type {
		case llm.EventTextDelta:
			if ev.Content != "" {
				sc.Send(ev.Content)
				stepText.WriteString(ev.Content)
			}
		case llm.EventToolUseStart:
			if ev.ToolCall != nil {
				pendingTools[ev.ToolCall.ID] = &llm.CompletedToolCall{
					ID:   ev.ToolCall.ID,
					Name: ev.ToolCall.Name,
				}
			}
		case llm.EventToolUseDelta:
			if ev.ToolCall != nil {
				if tc, ok := pendingTools[ev.ToolCall.ID]; ok {
					tc.Input += ev.ToolCall.Input
				}
			}
		case llm.EventToolUseDone:
			if ev.ToolCall != nil {
				if tc, ok := pendingTools[ev.ToolCall.ID]; ok {
					if ev.ToolCall.Input != "" {
						tc.Input = ev.ToolCall.Input
					}
					toolCalls = append(toolCalls, *tc)
					delete(pendingTools, ev.ToolCall.ID)
				}
			}
		}
	})
	return stepText.String(), toolCalls, err
}

func (c *ChatResponder) executeChatTool(ctx context.Context, tc llm.CompletedToolCall) (string, bool) {
	input := json.RawMessage(tc.Input)
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	result, err := c.pipeline.ToolExecutor.Execute(ctx, tc.Name, input)
	if err != nil {
		return fmt.Sprintf("tool %q failed: %v", tc.Name, err), true
	}
	if result == nil {
		return "", false
	}
	return result.Output, result.IsError
}
