package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
)

const chatSystemPrompt = "You are a personal AI assistant. Respond naturally in the user's language.\n" +
	"Be concise, helpful, and conversational. Use 한국어 when the user speaks Korean."

// OutcomeAppender is the minimum surface of learning.OutcomeStore required
// to record chat outcomes. Keeping the interface small lets tests substitute
// a fake without pulling in the full store.
type OutcomeAppender interface {
	Append(learning.OutcomeRecord) error
}

type ChatResponder struct {
	provider     llm.Provider
	bot          BotClient
	chatID       string
	logger       *slog.Logger
	system       string
	outcomeStore OutcomeAppender
}

// ChatResponderOption configures optional dependencies of ChatResponder.
type ChatResponderOption func(*ChatResponder)

// WithOutcomeStore enables outcome recording for each Respond call.
// Without this option, ChatResponder runs without touching the outcome store.
func WithOutcomeStore(store OutcomeAppender) ChatResponderOption {
	return func(c *ChatResponder) { c.outcomeStore = store }
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
	_ = replyToMsgID
	logger := c.logger.With(
		"principal_user_id", principal.UserID,
		"principal_project_id", principal.ProjectID,
		"principal_surface", principal.Surface,
	)
	sc := NewStreamConsumer(c.bot, c.chatID, logger)
	sc.Run()

	req := llm.ChatRequest{
		Messages:    []llm.Message{llm.NewUserMessage(userMessage)},
		MaxTokens:   1024,
		Temperature: 0.7,
		System:      c.system,
	}

	start := time.Now()
	streamErr := c.provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		switch ev.Type {
		case llm.EventTextDelta:
			sc.Send(ev.Content)
		case llm.EventDone:
			sc.Finish()
		}
	})
	if streamErr != nil {
		sc.Finish()
		sc.Wait()
		elapsed := time.Since(start)
		c.recordChatOutcome(principal, userMessage, false, "error", elapsed)
		logger.Warn("chat responder: stream failed", "error", streamErr)
		if sendErr := c.bot.SendMessage(ctx, c.chatID, fmt.Sprintf("⚠️ Error: %s", streamErr.Error())); sendErr != nil {
			return fmt.Errorf("chat responder: send error message: %w", sendErr)
		}
		return fmt.Errorf("chat responder: stream: %w", streamErr)
	}

	sc.Wait()
	c.recordChatOutcome(principal, userMessage, true, "stop", time.Since(start))
	return nil
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

// chatSnippet truncates the message at n runes (not bytes) so multi-byte
// characters are preserved intact.
func chatSnippet(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
