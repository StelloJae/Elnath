package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
)

// chatRunStats accumulates per-turn diagnostic counters (iterations, tool
// calls, token usage) so recordChatOutcome can populate the same fields the
// workflow path already records. Without this, chat outcomes show
// iterations=0 / tool_stats=null even when the tool loop actually fired —
// which is exactly the observability gap that made the FU-CR2b polish
// smoke ambiguous.
type chatRunStats struct {
	iterations   int
	inputTokens  int
	outputTokens int
	toolCalls    map[string]*chatToolStatAcc
}

type chatToolStatAcc struct {
	calls  int
	errors int
	total  time.Duration
}

func newChatRunStats() *chatRunStats {
	return &chatRunStats{toolCalls: map[string]*chatToolStatAcc{}}
}

func (s *chatRunStats) recordTool(name string, dur time.Duration, hadErr bool) {
	acc := s.toolCalls[name]
	if acc == nil {
		acc = &chatToolStatAcc{}
		s.toolCalls[name] = acc
	}
	acc.calls++
	if hadErr {
		acc.errors++
	}
	acc.total += dur
}

func (s *chatRunStats) recordUsage(u llm.UsageStats) {
	s.inputTokens += u.InputTokens
	s.outputTokens += u.OutputTokens
}

func (s *chatRunStats) toolStatsList() []learning.AgentToolStat {
	if len(s.toolCalls) == 0 {
		return nil
	}
	out := make([]learning.AgentToolStat, 0, len(s.toolCalls))
	for name, acc := range s.toolCalls {
		out = append(out, learning.AgentToolStat{
			Name:      name,
			Calls:     acc.calls,
			Errors:    acc.errors,
			TotalTime: acc.total,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// maxChatToolIterations bounds the chat-only agent-lite loop. Audit
// 2026-04-21 cell A3 showed 5 × large web_fetch chaining pushed a single
// chat turn to 38.9s — conversational territory where partners read
// silence as a stall. Tightened to 2: every realistic chat-tool scenario
// observed in dogfood (timezone lookup, stock ticker, URL read, single
// web_search + follow-up) fits in 1–2 iterations. Requests that
// legitimately need more chained tool steps belong on the task path
// (simple_task / team), which has proper progress bubbles and the
// permission gate.
//
// Explicit trade-off: a request that actually needs 3+ tool hops now
// trips "exceeded max iterations" → ⚠️ friendly error. That's the
// designed behavior — chat is the fast lane, not the deep-research lane.
const maxChatToolIterations = 2

// chatToolResultCap bounds each tool_result's content before it re-enters
// the provider history. Audit 2026-04-21 showed web_fetch / web_search
// outputs accumulating verbatim across iterations, driving chat_direct
// input_tokens to ~31x the task-path baseline (360k vs 11k) and
// stretching single turns to 38.9s. 64 KiB (~16k tokens) is wide enough
// to keep the meaningful extract of a typical web page intact — partner
// preference is detailed answers, so we cap above the 8–16 KiB bound
// where aggressive summarisation would start trimming substance — while
// still halving worst-case input_tokens when two big fetches chain.
//
// When truncation happens, capChatToolResult appends a Korean-language
// ellipsis marker that names the original byte count so the model and
// any future observer know the result was not the whole document.
const chatToolResultCap = 64 * 1024

// capChatToolResult truncates content to at most chatToolResultCap bytes
// and appends an ellipsis marker when truncation occurred. Byte-level
// (not rune-level) to keep the cap predictable for the provider; the
// marker text itself is plain ASCII+Hangul so it survives a mid-rune cut
// when the cut lands inside a multi-byte sequence (the marker follows
// the cut, and any torn rune at the boundary reads as replacement-char
// noise — acceptable since this is context for the model, not display
// text).
func capChatToolResult(content string) string {
	if len(content) <= chatToolResultCap {
		return content
	}
	original := len(content)
	return content[:chatToolResultCap] + fmt.Sprintf("\n\n[… tool_result 중략, 원본 %d bytes (cap=%d) …]", original, chatToolResultCap)
}

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

// prependChatHeaders prepends the time header to whatever systemPrompt
// the builder produced. Pre-L3.3 this also prepended the chat tool
// guide via a now-deleted chatToolGuideHeader() helper; since L3.3 the
// guide has a single source — prompt.ChatToolGuideNode — which renders
// inside the Builder output when state.IsChat=true and
// state.AvailableTools is non-empty. Keeping the time header as a
// prepend (rather than another node) preserves the sub-minute
// deterministic value that only Respond can supply.
func (c *ChatResponder) prependChatHeaders(systemPrompt string) string {
	header := chatTimeHeader(c.nowFunc())
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
//  2. If the model emits tool_use blocks, execute each tool via ToolExecutor
//     and append a tool_result; then re-stream with the updated history.
//  3. Stop when no tool_use is requested OR maxChatToolIterations is reached.
//
// The ✍ "working" reaction is set by Respond at chat-path entry (P1,
// FU-ChatEntryWorking) so every chat turn has a consistent in-progress
// signal regardless of tool use. Respond replaces ✍ with 👍 / 😢 on
// terminal outcome. replyToMsgID is threaded through only for potential
// future tool-lifecycle reactions; the entry-level ✍ alone satisfies
// today's UX parity target.
//
// progress is the Phase L2.2 emission hook: the chat tool loop reports
// each tool invocation through progress.ReportTool so the partner sees
// a batched edit-bubble of "tool name + target" rows while the turn
// is running, instead of 6-10s of silence. Respond owns progress's
// lifecycle (Finish + Wait) — this helper only emits.
//
// The caller owns sc lifecycle; this helper writes deltas via sc.Send but
// never calls sc.Finish — Respond closes the consumer after we return.
func (c *ChatResponder) runStreamWithTools(
	ctx context.Context,
	initialMessages []llm.Message,
	systemPrompt string,
	sc *StreamConsumer,
	replyToMsgID int64,
	progress ProgressRenderer,
) (string, []llm.Message, *chatRunStats, error) {
	_ = replyToMsgID // reserved for future tool-lifecycle reactions; entry-side ✍ handles the current UX target.
	if progress == nil {
		progress = noopProgressRenderer{}
	}
	turnStart := len(initialMessages)
	messages := make([]llm.Message, 0, len(initialMessages)+2*maxChatToolIterations)
	messages = append(messages, initialMessages...)
	stats := newChatRunStats()

	var fullText strings.Builder

	// turnDelta peels off just the messages this run appended — everything
	// past `turnStart` in the working `messages` slice. Returned alongside
	// fullText so the persist path (Respond → buildChatPersistTurn) can
	// write the complete block sequence (assistant[text+tool_use] →
	// user[tool_result] → ... → assistant[final text]) with Source="chat".
	turnDelta := func() []llm.Message {
		if len(messages) <= turnStart {
			return nil
		}
		delta := make([]llm.Message, len(messages)-turnStart)
		copy(delta, messages[turnStart:])
		return delta
	}

	for iter := 0; iter < maxChatToolIterations; iter++ {
		stats.iterations++
		req := llm.ChatRequest{
			Messages:    messages,
			MaxTokens:   chatMaxTokens,
			Temperature: 0.7,
			System:      systemPrompt,
			Tools:       c.pipeline.ToolDefs,
		}

		stepText, toolCalls, err := c.streamOneStep(ctx, req, sc, stats)
		if err != nil {
			return fullText.String(), turnDelta(), stats, err
		}
		fullText.WriteString(stepText)

		var textParts []string
		if stepText != "" {
			textParts = []string{stepText}
		}

		if len(toolCalls) == 0 {
			// Terminal text step: record the final assistant message in
			// the turn delta so persist sees the answer text, not just the
			// provider-visible transcript.
			if stepText != "" {
				messages = append(messages, llm.BuildAssistantMessage(textParts, nil))
			}
			return fullText.String(), turnDelta(), stats, nil
		}

		messages = append(messages, llm.BuildAssistantMessage(textParts, toolCalls))

		for _, tc := range toolCalls {
			// Phase L2.2: emit the tool invocation into the ProgressRenderer
			// before the display-only banner. The renderer batches / dedups
			// / throttles internally; bubble creation is lazy (first
			// ReportTool is what triggers the initial SendMessage), so
			// zero-tool chat turns stay bubble-free per plan OQ#1.
			toolPreview := chatToolProgressPreview(tc.Name, tc.Input)
			progress.ReportTool(tc.Name, toolPreview)
			// Phase L2.3: optional structured observer — same envelope the
			// task path uses (daemon.ToolProgressEvent) so scorecard / audit
			// can subscribe to chat tool timing without a second wire
			// format. Off by default (ProgressObserver nil) per plan OQ #5.
			if obs := c.pipeline.ProgressObserver; obs != nil {
				obs(daemon.ToolProgressEvent(tc.Name, toolPreview))
			}
			if note := chatToolProgressNote(tc.Name, tc.Input); note != "" {
				// Display-only: note is a "working on it" banner that goes to
				// the partner's stream bubble while the tool runs. We do NOT
				// append it to fullText or to the assistant message in the
				// turn delta because fullText lands in the session JSONL
				// (via persistChatTurn) and in learning outcomes (via
				// output-tokens inference). Dogfood 2026-04-21 17:46 KST
				// showed that persisting notes pollutes chat history (low
				// signal once the turn is done) and, worst case, leaves a
				// stored assistantText consisting only of progress banners
				// when the final-step LLM text isn't captured for unrelated
				// reasons. Session should mirror the model's answer.
				sc.Send(note)
			}
			toolStart := time.Now()
			content, isError := c.executeChatTool(ctx, tc)
			stats.recordTool(tc.Name, time.Since(toolStart), isError)
			messages = llm.AppendToolResult(messages, tc.ID, capChatToolResult(content), isError)
		}
	}

	return fullText.String(), turnDelta(), stats, fmt.Errorf("chat tool loop exceeded max iterations (%d)", maxChatToolIterations)
}

func (c *ChatResponder) streamOneStep(ctx context.Context, req llm.ChatRequest, sc *StreamConsumer, stats *chatRunStats) (string, []llm.CompletedToolCall, error) {
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
		case llm.EventDone:
			if ev.Usage != nil && stats != nil {
				stats.recordUsage(*ev.Usage)
			}
		}
	})
	return stepText.String(), toolCalls, err
}

// chatToolProgressPreview extracts the most informative single-value
// preview for a tool invocation — the URL for web_fetch, the query
// for web_search, the pattern for glob/grep, the path for read_file.
// Used by Phase L2.2 ProgressRenderer.ReportTool so the edit-bubble
// shows "🌐 web_fetch: https://example.com/..." rather than the raw
// JSON arguments. Silent fallback to an empty string when the tool
// name is unfamiliar or the JSON is unparseable — the ProgressReporter
// renderer tolerates blank previews.
func chatToolProgressPreview(name, inputJSON string) string {
	var args map[string]any
	if inputJSON != "" {
		_ = json.Unmarshal([]byte(inputJSON), &args)
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch name {
	case "web_search":
		return pick("query", "q")
	case "web_fetch":
		return pick("url")
	case "read_file":
		return pick("path", "file_path")
	case "glob":
		return pick("pattern")
	case "grep":
		return pick("pattern")
	}
	return ""
}

// chatToolProgressNote produces a one-line "I'm doing X" note that the chat
// stream emits just before blocking on a tool call (FU-ChatProgressNote). The
// chat path does not flow through sink.go / progress_reporter, so without
// this the partner sees only a ✍ reaction and silence during the 2–4s
// web_fetch / web_search window — then text suddenly appears once the
// second stream produces its first delta. This keeps the text pipe warm
// with a visible hint so silence never exceeds the tool's own latency.
//
// Returns "" when the tool name is unknown so we never ship a blank note;
// the caller treats empty as "skip".
func chatToolProgressNote(name, inputJSON string) string {
	emoji := chatToolNoteEmoji(name)
	hint := chatToolNoteHint(name, inputJSON)
	if emoji == "" && hint == "" {
		return ""
	}
	return fmt.Sprintf("%s `%s` %s\n\n", emoji, name, hint)
}

func chatToolNoteEmoji(name string) string {
	switch name {
	case "web_search":
		return "🔍"
	case "web_fetch", "read_file":
		return "📄"
	case "glob", "grep":
		return "🔎"
	default:
		return "🔧"
	}
}

// chatToolNoteHint peeks at the tool arguments to include a short, partner-
// facing snippet of what's being looked up (URL, query, file path, pattern).
// Silent fallback to a generic verb when arguments are missing or
// un-parseable — progress notes are UX polish, not structured data.
func chatToolNoteHint(name, inputJSON string) string {
	var args map[string]any
	if inputJSON != "" {
		_ = json.Unmarshal([]byte(inputJSON), &args)
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch name {
	case "web_search":
		if q := pick("query", "q"); q != "" {
			return fmt.Sprintf("로 `%s` 검색 중…", chatSnippet(q, 40))
		}
		return "로 최신 정보 확인 중…"
	case "web_fetch":
		if u := pick("url"); u != "" {
			return fmt.Sprintf("로 `%s` 읽는 중…", chatSnippet(u, 60))
		}
		return "로 URL 읽는 중…"
	case "read_file":
		if p := pick("path", "file_path"); p != "" {
			return fmt.Sprintf("로 `%s` 읽는 중…", chatSnippet(p, 60))
		}
		return "로 파일 읽는 중…"
	case "glob":
		if p := pick("pattern"); p != "" {
			return fmt.Sprintf("로 `%s` 탐색 중…", chatSnippet(p, 40))
		}
		return "로 파일 탐색 중…"
	case "grep":
		if p := pick("pattern"); p != "" {
			return fmt.Sprintf("로 `%s` 검색 중…", chatSnippet(p, 40))
		}
		return "로 내용 검색 중…"
	default:
		return "실행 중…"
	}
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
