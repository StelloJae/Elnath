package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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

// chatToolGuideHeader is appended to the system prompt only when the tool
// loop is active. Codex/gpt-5.4 in chat mode is conservative about emitting
// tool_use without an explicit cue; without this nudge the model answers
// from its knowledge cutoff (the FU-CR2b loop never fires).
//
// FU-ChatToolGuideStrong expanded this from a one-liner nudge into a
// structured instruction block (triggers, tool catalog, execution rules) so
// the model has concrete decision rules for when to emit tool_use and what
// to do with the results. Markdown H2 heading anchors the section visually
// for both Anthropic and Codex/OpenAI providers.
//
// FU-ChatGuideFactFence (Fix C-P1) added rule 5 (refuse prior-knowledge
// fact injection when tool_result lacks concrete rows) and an alternate-
// sources section after 2026-04-21 dogfood showed hedged-hallucination
// on sparse Yahoo most-active scrapes. Evidence:
// .omc/research/fix-c-factcheck.md.
//
// TODO(L3): both fact-fence anchors (rule 5 + alternate sources) are
// chat-specific today; relocate to a universal prompt.Builder node so
// task and chat paths share the discipline. Plan:
// .omc/plans/l1-universal-message-schema.md.
func chatToolGuideHeader() string {
	return `
## 도구 사용 지침

아래 상황에서는 반드시 도구를 호출하세요 (추측·지식 cutoff 답변 금지):
- "지금/오늘/최근/최신" 등 현재 시점 정보 (시세·뉴스·릴리즈·트렌드)
- 특정 URL의 내용 확인이 필요한 질문
- 로컬 파일·코드 내용 확인이 필요한 질문
- 외부 사실 검증이 필요한 주장

사용 가능한 도구:
- web_search: 최신 정보 검색 (뉴스·가격·트렌드)
- web_fetch: 주어진 URL 내용 가져오기
- read_file: 프로젝트 파일 내용 읽기
- glob: 파일 경로 패턴 매칭
- grep: 파일 내용에서 문자열 검색

실행 규칙:
1. 위 상황에 해당하면 먼저 도구를 호출한 뒤 답한다.
2. 서로 독립적인 조회 여러 개는 한 번에 병렬 tool_use 블록으로 발행한다.
3. 도구 결과를 받으면 한국어로 자연스럽게 요약·정리해 답한다.
4. 일반 지식·간단한 대화처럼 도구 없이 답할 수 있으면 그대로 답한다.
5. tool_result 가 요청한 대상의 **구체 수치 rows** (종목명·가격·거래량·건수 등) 를 반환하지 못했다면, 사전지식으로 이름·수치를 지어내지 말 것. 대신 (a) 무엇이 추출됐고 무엇이 비어있는지 명시, (b) 아래 대안 소스 시도 또는 파트너에게 재질문.

대안 소스 (primary scrape 가 sparse 일 때 순차 시도):
- US 거래량 상위: https://finance.yahoo.com/most-active → https://query1.finance.yahoo.com/v1/finance/trending/US (JSON) → https://finviz.com/screener.ashx?v=111&s=ta_mostactive
- 한국 시장: https://finance.naver.com/sise/sise_quant.naver (코스피) · https://finance.naver.com/sise/sise_quant.naver?sosok=1 (코스닥)
`
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
// The caller owns sc lifecycle; this helper writes deltas via sc.Send but
// never calls sc.Finish — Respond closes the consumer after we return.
func (c *ChatResponder) runStreamWithTools(
	ctx context.Context,
	initialMessages []llm.Message,
	systemPrompt string,
	sc *StreamConsumer,
	replyToMsgID int64,
) (string, *chatRunStats, error) {
	_ = replyToMsgID // reserved for future tool-lifecycle reactions; entry-side ✍ handles the current UX target.
	messages := make([]llm.Message, 0, len(initialMessages)+2*maxChatToolIterations)
	messages = append(messages, initialMessages...)
	stats := newChatRunStats()

	var fullText strings.Builder

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
			return fullText.String(), stats, err
		}
		fullText.WriteString(stepText)

		if len(toolCalls) == 0 {
			return fullText.String(), stats, nil
		}

		var textParts []string
		if stepText != "" {
			textParts = []string{stepText}
		}
		messages = append(messages, llm.BuildAssistantMessage(textParts, toolCalls))

		for _, tc := range toolCalls {
			if note := chatToolProgressNote(tc.Name, tc.Input); note != "" {
				// Display-only: note is a "working on it" banner that goes to
				// the partner's stream bubble while the tool runs. We do NOT
				// append it to fullText because fullText is what lands in
				// session JSONL via persistChatTurn and in learning outcomes
				// via output-tokens inference. Dogfood 2026-04-21 17:46 KST
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

	return fullText.String(), stats, fmt.Errorf("chat tool loop exceeded max iterations (%d)", maxChatToolIterations)
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
