package telegram

import (
	"context"
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

const chatSystemPrompt = "You are a personal AI assistant. Respond naturally in the user's language.\n" +
	"Be concise, helpful, and conversational. Use 한국어 when the user speaks Korean."

// defaultChatHistoryTurns caps how many past turns are hydrated from the
// bound session into the chat prompt. Kept small so the chat path stays
// "immediate" (no queue) and stays under the provider context window for
// the typical 1024-token max-output chat mode.
const defaultChatHistoryTurns = 20

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
type ChatSessionPersister interface {
	EnsureChatSession(ctx context.Context, principal identity.Principal) (string, error)
	AppendChatTurn(ctx context.Context, sessionID string, user, assistant llm.Message) error
}

// ChatSessionRemember records a newly-created chat→session binding so future
// Lookup calls return the same session ID. *ChatSessionBinder satisfies this.
type ChatSessionRemember interface {
	Remember(chatID, userID, sessionID string) error
}

// ChatPipelineDeps bundles the prompt-pipeline dependencies injected by the
// runtime so ChatResponder can build system prompts via prompt.Builder and
// hydrate history from the bound session. When nil, ChatResponder falls back
// to the legacy hardcoded chatSystemPrompt and a single-message array.
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
	system       string
	outcomeStore OutcomeAppender
	pipeline     *ChatPipelineDeps
}

// ChatResponderOption configures optional dependencies of ChatResponder.
type ChatResponderOption func(*ChatResponder)

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
		system:   chatSystemPrompt,
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

	systemPrompt, history := c.buildPrompt(ctx, principal, userMessage, logger)
	messages := make([]llm.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, llm.NewUserMessage(userMessage))

	start := time.Now()
	var (
		assistantText string
		streamErr     error
	)

	if c.useToolLoop() {
		assistantText, streamErr = c.runStreamWithTools(ctx, messages, systemPrompt, sc)
		sc.Finish()
	} else {
		assistantText, streamErr = c.runLegacyStream(ctx, messages, systemPrompt, sc)
	}

	if streamErr != nil {
		sc.Finish()
		sc.Wait()
		elapsed := time.Since(start)
		c.recordChatOutcome(principal, userMessage, false, "error", elapsed)
		logger.Warn("chat responder: stream failed", "error", streamErr)
		c.setCompletionReaction(ctx, replyToMsgID, "😢")
		if sendErr := c.bot.SendMessage(ctx, c.chatID, fmt.Sprintf("⚠️ Error: %s", streamErr.Error())); sendErr != nil {
			return fmt.Errorf("chat responder: send error message: %w", sendErr)
		}
		return fmt.Errorf("chat responder: stream: %w", streamErr)
	}

	sc.Wait()
	c.recordChatOutcome(principal, userMessage, true, "stop", time.Since(start))
	c.setCompletionReaction(ctx, replyToMsgID, "👍")

	if assistantText != "" {
		c.persistChatTurn(ctx, principal,
			llm.NewUserMessage(userMessage),
			llm.NewAssistantMessage(assistantText),
			logger,
		)
	}
	return nil
}

func (c *ChatResponder) useToolLoop() bool {
	return c.pipeline != nil && c.pipeline.ToolExecutor != nil && len(c.pipeline.ToolDefs) > 0
}

func (c *ChatResponder) runLegacyStream(ctx context.Context, messages []llm.Message, systemPrompt string, sc *StreamConsumer) (string, error) {
	req := llm.ChatRequest{
		Messages:    messages,
		MaxTokens:   1024,
		Temperature: 0.7,
		System:      systemPrompt,
	}
	if c.pipeline != nil && len(c.pipeline.ToolDefs) > 0 {
		req.Tools = c.pipeline.ToolDefs
	}

	var assistantText strings.Builder
	err := c.provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		switch ev.Type {
		case llm.EventTextDelta:
			sc.Send(ev.Content)
			assistantText.WriteString(ev.Content)
		case llm.EventDone:
			sc.Finish()
		}
	})
	return assistantText.String(), err
}

// setCompletionReaction updates the reaction on the user's original message to
// signal chat outcome. Skipped when replyToMsgID is 0 (no originating message
// to react to). Errors from the Telegram API are logged at debug level rather
// than propagated — reactions are UX polish, not load-bearing for chat flow.
func (c *ChatResponder) setCompletionReaction(ctx context.Context, replyToMsgID int64, emoji string) {
	if replyToMsgID <= 0 {
		return
	}
	if err := c.bot.SetReaction(ctx, c.chatID, replyToMsgID, emoji); err != nil {
		c.logger.Debug("chat responder: set reaction failed", "error", err, "emoji", emoji)
	}
}

// persistChatTurn writes the user+assistant pair to the session-bound JSONL
// transcript so subsequent chats can self-reference prior turns. Missing
// pipeline deps or append errors are logged but never fail the chat itself —
// Telegram UX already showed the reply.
func (c *ChatResponder) persistChatTurn(ctx context.Context, principal identity.Principal, userMsg, assistantMsg llm.Message, logger *slog.Logger) {
	if c.pipeline == nil || c.pipeline.Persister == nil {
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

	if err := c.pipeline.Persister.AppendChatTurn(ctx, sessionID, userMsg, assistantMsg); err != nil {
		logger.Warn("chat responder: append chat turn failed",
			"error", err,
			"session_id", sessionID,
		)
	}
}

// recordChatOutcome synthesises a learning outcome for the chat path. It
// mirrors the workflow-path outcome schema so Scorecard's outcome_recording
// axis sees chat events. ProjectID "" is treated as unknown and skipped, the
// same policy executionRuntime uses.
func (c *ChatResponder) recordChatOutcome(principal identity.Principal, userMessage string, success bool, finishReason string, elapsed time.Duration) {
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
	}
	if err := c.outcomeStore.Append(record); err != nil {
		c.logger.Warn("chat responder: outcome append failed", "error", err)
	}
}

// buildPrompt assembles the system prompt and hydrates session history when
// a ChatPipelineDeps is wired. Without the pipeline, returns the legacy
// hardcoded chatSystemPrompt and no history (caller adds the user message).
func (c *ChatResponder) buildPrompt(ctx context.Context, principal identity.Principal, userMessage string, logger *slog.Logger) (string, []llm.Message) {
	if c.pipeline == nil || c.pipeline.Builder == nil {
		return c.system, nil
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
			history = trimChatHistory(hist, c.pipeline.MaxHistory)
		} else {
			logger.Warn("chat responder: history load failed, continuing without", "error", err, "session_id", sessionID)
		}
	}

	state := &prompt.RenderState{
		SessionID:    sessionID,
		UserInput:    userMessage,
		Self:         c.pipeline.Self,
		Principal:    principal,
		Messages:     history,
		WikiIdx:      c.pipeline.WikiIdx,
		PersonaExtra: c.pipeline.PersonaExtra,
		Model:        c.pipeline.Model,
		Provider:     c.pipeline.ProviderName,
		WorkDir:      c.pipeline.WorkDir,
		DaemonMode:   c.pipeline.DaemonMode,
		ProjectID:    principal.ProjectID,
		MessageCount: len(history),
	}

	built, err := c.pipeline.Builder.Build(ctx, state)
	if err != nil {
		logger.Warn("chat responder: prompt build failed, using legacy fallback", "error", err)
		return c.system, history
	}
	if strings.TrimSpace(built) == "" {
		return c.system, history
	}
	return built, history
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

// chatSnippet truncates the message at n runes (not bytes) so multi-byte
// characters are preserved intact.
func chatSnippet(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
