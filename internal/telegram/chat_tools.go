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
// from its knowledge cutoff (the FU-CR2b loop never fires).
//
// FU-ChatToolGuideStrong expanded this from a one-liner nudge into a
// structured instruction block (triggers, tool catalog, execution rules) so
// the model has concrete decision rules for when to emit tool_use and what
// to do with the results. Markdown H2 heading anchors the section visually
// for both Anthropic and Codex/OpenAI providers.
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
) (string, *chatRunStats, error) {
	messages := make([]llm.Message, 0, len(initialMessages)+2*maxChatToolIterations)
	messages = append(messages, initialMessages...)
	stats := newChatRunStats()

	var fullText strings.Builder

	for iter := 0; iter < maxChatToolIterations; iter++ {
		stats.iterations++
		req := llm.ChatRequest{
			Messages:    messages,
			MaxTokens:   1024,
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
			toolStart := time.Now()
			content, isError := c.executeChatTool(ctx, tc)
			stats.recordTool(tc.Name, time.Since(toolStart), isError)
			messages = llm.AppendToolResult(messages, tc.ID, content, isError)
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
