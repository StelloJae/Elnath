package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/prompt"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

// chatPromptBuildFailureMessage is the partner-facing reassurance shown
// when prompt.Builder fails (pipeline not wired, build error, or empty
// result). Phase L3.2 made the Builder path mandatory — the old
// hardcoded chatSystemPrompt fallback that used to paper over these
// failures is gone, so this message is the single surface the partner
// sees when the chat prompt cannot be constructed.
const chatPromptBuildFailureMessage = "대화 준비 중 문제가 생겨서 답을 드리지 못했어요. 잠시 후 다시 시도해 주세요."

// defaultChatHistoryTurns caps how many past turns are hydrated from the
// bound session into the chat prompt. Kept small so the chat path stays
// "immediate" (no queue) and stays under the provider context window.
const defaultChatHistoryTurns = 20

// chatMaxTokens caps the provider's per-step max_tokens for chat requests.
// The original 1024 left no headroom for a tool-loop step that has to both
// narrate the tool_result and finish the answer in one turn; dogfood
// showed replies truncating mid-sentence after web_fetch results. 4096
// matches Anthropic/Codex sane defaults for conversational surfaces without
// blowing context on routine chat.
const chatMaxTokens = 4096

// OutcomeAppender is the minimum surface of learning.OutcomeStore required
// to record chat outcomes. Keeping the interface small lets tests substitute
// a fake without pulling in the full store.
type OutcomeAppender interface {
	Append(learning.OutcomeRecord) error
}

// ChatPromptBuilder is the minimum surface of prompt.Builder needed by the
// chat path. Using an interface lets tests inject a stub without pulling in
// the full node registry.
type ChatPromptBuilder interface {
	Build(ctx context.Context, state *prompt.RenderState) (string, error)
}

// ChatHistoryLoader loads past messages for a given session. conversation.Manager
// satisfies this via its GetHistory method.
type ChatHistoryLoader interface {
	GetHistory(ctx context.Context, sessionID string) ([]llm.Message, error)
}

// ChatSessionLookup resolves a chatID+userID pair to a sessionID.
// *ChatSessionBinder satisfies this via its Lookup method.
type ChatSessionLookup interface {
	Lookup(chatID, userID string) (string, bool)
}

// ChatSessionPersister creates a new chat-bound session (when one is not yet
// bound) and appends completed chat turns to the session transcript.
// conversation.Manager satisfies this via EnsureChatSession / AppendChatTurn.
//
// AppendChatTurn takes a slice so the chat path can persist a full turn
// (user message + assistant text/tool_use blocks + paired user tool_result
// blocks + final assistant text) as a single atomic call. Each message's
// Source field tags its origin so load-side sanitisers can preserve the
// chat-owned tool blocks while still stripping task-origin blocks bleeding
// in from the shared session JSONL.
type ChatSessionPersister interface {
	EnsureChatSession(ctx context.Context, principal identity.Principal) (string, error)
	AppendChatTurn(ctx context.Context, sessionID string, messages []llm.Message) error
}

// ChatSessionRemember records a newly-created chat→session binding so future
// Lookup calls return the same session ID. *ChatSessionBinder satisfies this.
type ChatSessionRemember interface {
	Remember(chatID, userID, sessionID string) error
}

// ChatPipelineDeps bundles the prompt-pipeline dependencies injected by the
// runtime so ChatResponder can build system prompts via prompt.Builder and
// hydrate history from the bound session. Since Phase L3.2, Builder is
// effectively mandatory — a nil pipeline (or nil Builder) makes
// buildPrompt surface an error, which Respond turns into the partner-
// facing ⚠️ chatPromptBuildFailureMessage instead of silently streaming
// a degraded prompt.
type ChatPipelineDeps struct {
	Builder      ChatPromptBuilder
	Self         *self.SelfState
	WikiIdx      *wiki.Index
	History      ChatHistoryLoader
	Lookup       ChatSessionLookup
	Persister    ChatSessionPersister
	BindRecorder ChatSessionRemember
	PersonaExtra string
	ProviderName string
	Model        string
	WorkDir      string
	DaemonMode   bool
	MaxHistory   int
	// ToolDefs, when non-empty, is forwarded to the provider as ChatRequest.Tools
	// so the chat path exposes a curated tool subset. Filtering lives at the wire
	// site; ChatResponder trusts the caller to supply only safe, chat-appropriate
	// defs.
	ToolDefs []llm.ToolDef
	// ToolExecutor, when set together with non-empty ToolDefs, activates the
	// chat tool_use → tool_result loop (FU-CR2b). Without an executor, ToolDefs
	// are still forwarded but the model's tool_use blocks are silently dropped —
	// useful only for measuring whether the model would have wanted tools.
	// Chat bypasses the agent loop's permission gate, so the executor MUST be
	// fed only allowlisted, side-effect-free tools (see FilterChatToolDefs).
	ToolExecutor tools.Executor
}

type ChatResponder struct {
	provider     llm.Provider
	bot          BotClient
	chatID       string
	logger       *slog.Logger
	outcomeStore OutcomeAppender
	pipeline     *ChatPipelineDeps
	nowFunc      func() time.Time
}

// ChatResponderOption configures optional dependencies of ChatResponder.
type ChatResponderOption func(*ChatResponder)

// WithChatNow injects a custom clock for the chat-time header. Tests use this
// to pin a deterministic timestamp in the system prompt.
func WithChatNow(f func() time.Time) ChatResponderOption {
	return func(c *ChatResponder) {
		if f != nil {
			c.nowFunc = f
		}
	}
}

// WithOutcomeStore enables outcome recording for each Respond call.
// Without this option, ChatResponder runs without touching the outcome store.
func WithOutcomeStore(store OutcomeAppender) ChatResponderOption {
	return func(c *ChatResponder) { c.outcomeStore = store }
}

// WithChatPipeline wires the prompt-pipeline + history hydrate path so chat
// messages benefit from Elnath identity, persona, lessons, wiki RAG, and
// past conversation context (Phase 7.1 GAP-TG-01 / GAP-HISTORY-01 fix).
func WithChatPipeline(deps ChatPipelineDeps) ChatResponderOption {
	return func(c *ChatResponder) {
		d := deps
		c.pipeline = &d
	}
}

func NewChatResponder(provider llm.Provider, bot BotClient, chatID string, logger *slog.Logger, opts ...ChatResponderOption) *ChatResponder {
	if logger == nil {
		logger = slog.Default()
	}
	c := &ChatResponder{
		provider: provider,
		bot:      bot,
		chatID:   chatID,
		logger:   logger,
		nowFunc:  time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *ChatResponder) Respond(ctx context.Context, principal identity.Principal, userMessage string, replyToMsgID int64) error {
	logger := c.logger.With(
		"principal_user_id", principal.UserID,
		"principal_project_id", principal.ProjectID,
		"principal_surface", principal.Surface,
	)
	sc := NewStreamConsumer(c.bot, c.chatID, logger)
	sc.Run()

	// FU-ChatEntryWorking (P1): once the chat path commits to this turn,
	// show ✍ immediately — not only when a tool fires. Audit 2026-04-21
	// showed 87% of chat_direct turns never reach the tool loop, so the
	// prior tool-only ✍ left partners with 👀→침묵→👍 on plain Q&A. Entry
	// reaction keeps "working" feedback consistent across every chat turn.
	// setReaction is idempotent; the terminal 👍/😢 overwrites naturally.
	c.setReaction(ctx, replyToMsgID, "✍")

	systemPrompt, history, sessionID, promptErr := c.buildPrompt(ctx, principal, userMessage, logger)
	if promptErr != nil {
		sc.Finish()
		sc.Wait()
		c.recordChatOutcome(principal, userMessage, false, "error", 0, nil, sessionID)
		logger.Warn("chat responder: prompt build failed", "error", promptErr)
		c.setReaction(ctx, replyToMsgID, "😢")
		if sendErr := c.bot.SendMessage(ctx, c.chatID, "⚠️ "+chatPromptBuildFailureMessage); sendErr != nil {
			return fmt.Errorf("chat responder: send prompt error message: %w", sendErr)
		}
		return fmt.Errorf("chat responder: prompt build: %w", promptErr)
	}
	systemPrompt = c.prependChatHeaders(systemPrompt)
	messages := make([]llm.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, llm.NewUserMessage(userMessage))

	start := time.Now()
	var (
		assistantText string
		turnMessages  []llm.Message
		stats         *chatRunStats
		streamErr     error
	)

	if c.useToolLoop() {
		assistantText, turnMessages, stats, streamErr = c.runStreamWithTools(ctx, messages, systemPrompt, sc, replyToMsgID)
		sc.Finish()
	} else {
		assistantText, turnMessages, stats, streamErr = c.runLegacyStream(ctx, messages, systemPrompt, sc)
	}

	if streamErr != nil {
		sc.Finish()
		sc.Wait()
		elapsed := time.Since(start)
		c.recordChatOutcome(principal, userMessage, false, "error", elapsed, stats, sessionID)
		logger.Warn("chat responder: stream failed", "error", streamErr)
		c.setReaction(ctx, replyToMsgID, "😢")
		if sendErr := c.bot.SendMessage(ctx, c.chatID, "⚠️ "+friendlyChatError(streamErr)); sendErr != nil {
			return fmt.Errorf("chat responder: send error message: %w", sendErr)
		}
		return fmt.Errorf("chat responder: stream: %w", streamErr)
	}

	sc.Wait()
	c.recordChatOutcome(principal, userMessage, true, "stop", time.Since(start), stats, sessionID)
	c.setReaction(ctx, replyToMsgID, "👍")

	if assistantText != "" {
		turn := buildChatPersistTurn(userMessage, turnMessages)
		c.persistChatTurn(ctx, principal, turn, logger)
	}
	return nil
}

// buildChatPersistTurn composes the persist payload for a chat turn.
// The turn sequence is: a synthesised user message (the partner's
// prompt, since the stream path only tracks assistant-side deltas)
// followed by every message the run produced (legacy: one assistant
// text message; tool-loop: assistant[text+tool_use] → user[tool_result]
// → ... → assistant[final text]). Every message is stamped with
// Source="chat" so L1.3's source-aware sanitiser can preserve the
// chat-owned tool blocks instead of stripping them as foreign bleed
// from the shared session JSONL.
func buildChatPersistTurn(userText string, turnMessages []llm.Message) []llm.Message {
	userMsg := llm.NewUserMessage(userText)
	userMsg.Source = llm.SourceChat
	out := make([]llm.Message, 0, len(turnMessages)+1)
	out = append(out, userMsg)
	for _, m := range turnMessages {
		m.Source = llm.SourceChat
		out = append(out, m)
	}
	return out
}

func (c *ChatResponder) useToolLoop() bool {
	return c.pipeline != nil && c.pipeline.ToolExecutor != nil && len(c.pipeline.ToolDefs) > 0
}

// chatAvailableTools returns the tool names the chat tool loop will
// actually execute this turn — used by prompt.ChatToolGuideNode to gate
// its guide so the model doesn't see a tool menu it cannot act on. Nil
// when the legacy stream path is active (no executor wired).
func (c *ChatResponder) chatAvailableTools() []string {
	if !c.useToolLoop() {
		return nil
	}
	names := make([]string, 0, len(c.pipeline.ToolDefs))
	for _, def := range c.pipeline.ToolDefs {
		if def.Name != "" {
			names = append(names, def.Name)
		}
	}
	return names
}

func (c *ChatResponder) runLegacyStream(ctx context.Context, messages []llm.Message, systemPrompt string, sc *StreamConsumer) (string, []llm.Message, *chatRunStats, error) {
	req := llm.ChatRequest{
		Messages:    messages,
		MaxTokens:   chatMaxTokens,
		Temperature: 0.7,
		System:      systemPrompt,
	}
	if c.pipeline != nil && len(c.pipeline.ToolDefs) > 0 {
		req.Tools = c.pipeline.ToolDefs
	}

	stats := newChatRunStats()
	stats.iterations = 1

	var assistantText strings.Builder
	err := c.provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		switch ev.Type {
		case llm.EventTextDelta:
			sc.Send(ev.Content)
			assistantText.WriteString(ev.Content)
		case llm.EventDone:
			if ev.Usage != nil {
				stats.recordUsage(*ev.Usage)
			}
			sc.Finish()
		}
	})
	text := assistantText.String()
	var turn []llm.Message
	if text != "" {
		turn = []llm.Message{llm.NewAssistantMessage(text)}
	}
	return text, turn, stats, err
}

// setReaction updates the reaction on the user's original message. Used for
// both mid-flight progress signals (✍ when the chat tool loop starts
// executing a tool) and terminal outcomes (👍 on success, 😢 on failure).
// Skipped when replyToMsgID is 0 (no originating message to react to).
// Errors from the Telegram API are logged at debug level rather than
// propagated — reactions are UX polish, not load-bearing for chat flow.
func (c *ChatResponder) setReaction(ctx context.Context, replyToMsgID int64, emoji string) {
	if replyToMsgID <= 0 {
		return
	}
	if err := c.bot.SetReaction(ctx, c.chatID, replyToMsgID, emoji); err != nil {
		c.logger.Debug("chat responder: set reaction failed", "error", err, "emoji", emoji)
	}
}

// persistChatTurn writes the assembled turn messages to the session-bound
// JSONL transcript so subsequent chats can self-reference prior turns.
// Missing pipeline deps, an empty slice, or append errors are logged but
// never fail the chat itself — Telegram UX already showed the reply.
// Callers are expected to have stamped Source on each message before
// calling; persistChatTurn is intentionally policy-free about provenance
// to keep the provenance decision close to the write site.
func (c *ChatResponder) persistChatTurn(ctx context.Context, principal identity.Principal, messages []llm.Message, logger *slog.Logger) {
	if c.pipeline == nil || c.pipeline.Persister == nil {
		return
	}
	if len(messages) == 0 {
		return
	}

	sessionID := ""
	if c.pipeline.Lookup != nil {
		if sid, ok := c.pipeline.Lookup.Lookup(c.chatID, principal.UserID); ok {
			sessionID = sid
		}
	}

	if sessionID == "" {
		sid, err := c.pipeline.Persister.EnsureChatSession(ctx, principal)
		if err != nil {
			logger.Warn("chat responder: ensure session failed; skipping persist", "error", err)
			return
		}
		sessionID = sid
		if c.pipeline.BindRecorder != nil {
			if err := c.pipeline.BindRecorder.Remember(c.chatID, principal.UserID, sessionID); err != nil {
				logger.Warn("chat responder: binder remember failed",
					"error", err,
					"session_id", sessionID,
				)
			}
		}
	}

	if err := c.pipeline.Persister.AppendChatTurn(ctx, sessionID, messages); err != nil {
		logger.Warn("chat responder: append chat turn failed",
			"error", err,
			"session_id", sessionID,
		)
	}
}

// recordChatOutcome synthesises a learning outcome for the chat path. It
// mirrors the workflow-path outcome schema so Scorecard's outcome_recording
// axis sees chat events, and (post FU-ChatObs) carries iteration/tool/token
// counters so dogfood probes can tell at a glance whether the chat tool
// loop fired or the model answered from its knowledge cutoff. ProjectID
// "" is treated as unknown and skipped, the same policy executionRuntime
// uses.
//
// FU-ChatOutcomeSessionID (P3, 2026-04-21 audit C4): sessionID is populated
// so chat outcomes can be cross-referenced with the session JSONL end to
// end. Empty when the chat turn was the first one in this Telegram bind
// and no session had been allocated yet — subsequent turns fill it in.
func (c *ChatResponder) recordChatOutcome(principal identity.Principal, userMessage string, success bool, finishReason string, elapsed time.Duration, stats *chatRunStats, sessionID string) {
	if c.outcomeStore == nil || principal.ProjectID == "" {
		return
	}
	record := learning.OutcomeRecord{
		ProjectID:      principal.ProjectID,
		Intent:         "chat",
		Workflow:       "chat_direct",
		FinishReason:   finishReason,
		Success:        success,
		Duration:       elapsed.Seconds(),
		InputSnippet:   chatSnippet(userMessage, 100),
		PreferenceUsed: false,
		MaxIterations:  maxChatToolIterations,
		SessionID:      sessionID,
	}
	if stats != nil {
		record.Iterations = stats.iterations
		record.InputTokens = stats.inputTokens
		record.OutputTokens = stats.outputTokens
		record.ToolStats = stats.toolStatsList()
	}
	if err := c.outcomeStore.Append(record); err != nil {
		c.logger.Warn("chat responder: outcome append failed", "error", err)
	}
}

// buildPrompt assembles the system prompt and hydrates session history via
// the wired prompt.Builder. Phase L3.2 removed the legacy hardcoded
// fallback — any one of (pipeline not wired, Builder error, empty result)
// now returns an error so Respond can surface a friendly Korean message
// to the partner instead of silently drifting into an identity-free
// fallback. The returned sessionID (may be empty) is threaded back to
// the caller so recordChatOutcome can cross-reference the learning
// record with the session JSONL (FU-ChatOutcomeSessionID / P3).
func (c *ChatResponder) buildPrompt(ctx context.Context, principal identity.Principal, userMessage string, logger *slog.Logger) (string, []llm.Message, string, error) {
	if c.pipeline == nil || c.pipeline.Builder == nil {
		return "", nil, "", errors.New("chat: prompt pipeline not wired")
	}

	sessionID := ""
	if c.pipeline.Lookup != nil {
		if sid, ok := c.pipeline.Lookup.Lookup(c.chatID, principal.UserID); ok {
			sessionID = sid
		}
	}

	var history []llm.Message
	if sessionID != "" && c.pipeline.History != nil {
		if hist, err := c.pipeline.History.GetHistory(ctx, sessionID); err == nil {
			history = sanitizeChatHistory(trimChatHistory(hist, c.pipeline.MaxHistory))
		} else {
			logger.Warn("chat responder: history load failed, continuing without", "error", err, "session_id", sessionID)
		}
	}

	state := &prompt.RenderState{
		SessionID:      sessionID,
		UserInput:      userMessage,
		Self:           c.pipeline.Self,
		Principal:      principal,
		Messages:       history,
		WikiIdx:        c.pipeline.WikiIdx,
		PersonaExtra:   c.pipeline.PersonaExtra,
		Model:          c.pipeline.Model,
		Provider:       c.pipeline.ProviderName,
		WorkDir:        c.pipeline.WorkDir,
		DaemonMode:     c.pipeline.DaemonMode,
		ProjectID:      principal.ProjectID,
		MessageCount:   len(history),
		IsChat:         true,
		AvailableTools: c.chatAvailableTools(),
	}

	built, err := c.pipeline.Builder.Build(ctx, state)
	if err != nil {
		logger.Error("chat responder: prompt build failed", "error", err, "session_id", sessionID)
		return "", history, sessionID, fmt.Errorf("chat: prompt build: %w", err)
	}
	if strings.TrimSpace(built) == "" {
		logger.Error("chat responder: prompt builder returned empty", "session_id", sessionID)
		return "", history, sessionID, errors.New("chat: prompt build empty")
	}
	return built, history, sessionID, nil
}

func trimChatHistory(msgs []llm.Message, maxTurns int) []llm.Message {
	if maxTurns <= 0 {
		maxTurns = defaultChatHistoryTurns
	}
	if len(msgs) <= maxTurns {
		return msgs
	}
	return msgs[len(msgs)-maxTurns:]
}

// sanitizeChatHistory filters tool blocks out of reloaded session
// history before the chat path hands them to the provider. Telegram
// binds one session JSONL per chat ID, and that JSONL is shared with
// every workflow the partner triggers — agent.Loop (simple_task / team)
// persists its tool_use / tool_result blocks verbatim there. When
// chat_direct reloads that session as history, foreign-origin blocks
// ride along as orphans: the current conversation has no matching
// function_call for the tool_result's call_id, and the Codex Responses
// API rejects the request with HTTP 400 "No tool call found for
// function call output with call_id ...".
//
// Phase L1.3 makes the filter source-aware. Chat-origin messages
// (Source == llm.SourceChat) pass through untouched so the partner's
// own tool loop — which Phase L1.2 started persisting as a full turn —
// stays visible on the next reload. Non-chat origins (task / team /
// legacy "") still get their tool_use and tool_result blocks stripped,
// which keeps the Codex HTTP 400 protection in place for the shared
// session JSONL. Pre-L1 records read back with Source == "" and
// resolve to SourceTask, matching the only pre-L1 writer that produced
// tool blocks.
//
// Messages that become empty after stripping are dropped so the
// provider never sees zero-block turns.
func sanitizeChatHistory(msgs []llm.Message) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if resolveChatSource(m.Source) == llm.SourceChat {
			out = append(out, m)
			continue
		}
		filtered := filterToolBlocks(m.Content)
		if len(filtered) == 0 {
			continue
		}
		out = append(out, llm.Message{Role: m.Role, Source: m.Source, Content: filtered})
	}
	return out
}

// resolveChatSource maps an on-disk Source value onto the enum used
// for sanitize decisions. Pre-L1 JSONL records were written without a
// Source field and decode to "" — the only writer that emitted tool
// blocks before L1 was the task path (agent.Loop), so the conservative
// default treats empty as task. Any non-empty value is returned
// verbatim so future enum additions (e.g. team sub-surfaces) fall
// through without silently collapsing to chat.
func resolveChatSource(s string) string {
	if s == "" {
		return llm.SourceTask
	}
	return s
}

// filterToolBlocks returns a copy of blocks with every ToolUseBlock and
// ToolResultBlock removed. The ordering of the remaining blocks is
// preserved so any surrounding dialogue text still reads naturally on
// the next turn.
func filterToolBlocks(blocks []llm.ContentBlock) []llm.ContentBlock {
	out := make([]llm.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.(type) {
		case llm.ToolUseBlock, llm.ToolResultBlock:
			continue
		}
		out = append(out, b)
	}
	return out
}

// friendlyChatError maps a raw stream/provider error to a short Korean
// partner-facing message. Audit 2026-04-21 cell E1: the old path streamed
// streamErr.Error() verbatim into Telegram, which once produced the raw
// Codex JSON payload (`codex: status 400: {"error":{"message":"..."}}`)
// visible to the partner — provider internals leaking into chat UX.
//
// The mapping is intentionally coarse: only a handful of buckets the
// partner can actually act on (retry / simplify / wait / reauthorise).
// The full streamErr is still logged at Warn level in Respond so operators
// keep the diagnostic detail, only the user-facing string is sanitised.
func friendlyChatError(err error) string {
	if err == nil {
		return "알 수 없는 문제로 답변하지 못했어요."
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "요청이 취소됐어요."
	case errors.Is(err, context.DeadlineExceeded):
		return "응답이 너무 오래 걸려서 중단했어요. 다시 시도해 주세요."
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "exceeded max iterations"):
		return "작업이 너무 길어져서 중단했어요. 더 간단히 나눠서 물어봐 주세요."
	case strings.Contains(msg, "status 429"):
		return "요청이 몰렸어요 (rate limit). 잠시 후 다시 시도해 주세요."
	case strings.Contains(msg, "status 401"), strings.Contains(msg, "status 403"):
		return "인증 문제로 답변할 수 없어요. 관리자 확인이 필요해요."
	case strings.Contains(msg, "status 400"):
		return "요청 형식 문제로 실패했어요. 새 메시지로 다시 시도해 보세요."
	case strings.Contains(msg, "status 5"):
		return "모델 서버가 일시적으로 문제예요. 잠시 후 다시 시도해 주세요."
	case strings.Contains(msg, "codex:"), strings.Contains(msg, "anthropic:"), strings.Contains(msg, "openai:"):
		return "모델 쪽에서 예상치 못한 응답을 받았어요. 다시 시도해 주세요."
	}
	return "내부에서 문제가 발생했어요. 잠시 후 다시 시도해 주세요."
}

// chatSnippet truncates the message at n runes (not bytes) so multi-byte
// characters are preserved intact.
func chatSnippet(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
