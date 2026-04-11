package telegram

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/llm"
)

const chatSystemPrompt = "You are a personal AI assistant. Respond naturally in the user's language.\n" +
	"Be concise, helpful, and conversational. Use 한국어 when the user speaks Korean."

type ChatResponder struct {
	provider llm.Provider
	bot      BotClient
	chatID   string
	logger   *slog.Logger
	system   string
}

func NewChatResponder(provider llm.Provider, bot BotClient, chatID string, logger *slog.Logger) *ChatResponder {
	if logger == nil {
		logger = slog.Default()
	}
	return &ChatResponder{
		provider: provider,
		bot:      bot,
		chatID:   chatID,
		logger:   logger,
		system:   chatSystemPrompt,
	}
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

	err := c.provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		switch ev.Type {
		case llm.EventTextDelta:
			sc.Send(ev.Content)
		case llm.EventDone:
			sc.Finish()
		}
	})
	if err != nil {
		sc.Finish()
		sc.Wait()
		logger.Warn("chat responder: stream failed", "error", err)
		sendErr := c.bot.SendMessage(ctx, c.chatID, fmt.Sprintf("⚠️ Error: %s", err.Error()))
		if sendErr != nil {
			return fmt.Errorf("chat responder: send error message: %w", sendErr)
		}
		return fmt.Errorf("chat responder: stream: %w", err)
	}

	sc.Wait()
	return nil
}
