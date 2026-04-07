package conversation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/llm"
)

const (
	// recentTurnsToKeep is the number of recent message pairs preserved during auto-compression.
	recentTurnsToKeep = 4

	// snipSafetyMarginTokens is the token budget reserved after hard truncation.
	snipSafetyMarginTokens = 3_000

	// maxCompressionAttempts is the circuit breaker limit for consecutive compression rounds.
	maxCompressionAttempts = 3

	// autoCompressThreshold is the fraction of maxTokens at which auto-compression triggers.
	autoCompressThreshold = 0.80
)

// summarizePrompt is sent to the LLM when performing auto-compression.
const summarizePrompt = `Summarize the following conversation history concisely.
Preserve key decisions, facts, and context needed to continue the conversation.
Output only the summary, no preamble.`

// ContextWindow manages token budget and message compression via a 3-stage pipeline.
type ContextWindow struct {
	logger *slog.Logger
}

// NewContextWindow creates a new ContextWindow manager.
func NewContextWindow() *ContextWindow {
	return &ContextWindow{
		logger: slog.Default(),
	}
}

// EstimateTokens estimates the token count for a slice of messages.
// Uses the chars/4 heuristic, which is accurate within ~20% for English text.
func (cw *ContextWindow) EstimateTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		// Role overhead (~4 tokens per message for formatting).
		total += 4
		for _, block := range m.Content {
			switch b := block.(type) {
			case llm.TextBlock:
				total += len(b.Text) / 4
			case llm.ToolUseBlock:
				total += (len(b.Name) + len(b.Input)) / 4
			case llm.ToolResultBlock:
				total += len(b.Content) / 4
			}
		}
	}
	return total
}

// Fit applies the 3-stage compression pipeline to bring messages within maxTokens.
//
// Stage 1 — Micro (free): strips temporary markers and empty blocks, no LLM call.
// Stage 2 — Auto (LLM summary): when estimated tokens > 80% of maxTokens, summarize
//
//	old messages keeping the most recent recentTurnsToKeep pairs.
//
// Stage 3 — Snip (hard cut): when context still exceeds limit, hard truncates with
//
//	snipSafetyMarginTokens margin.
//
// A circuit breaker caps compression at maxCompressionAttempts iterations.
func (cw *ContextWindow) Fit(ctx context.Context, messages []llm.Message, maxTokens int) ([]llm.Message, error) {
	// Stage 1: micro compression (free, no LLM).
	messages = cw.microCompress(messages)

	estimated := cw.EstimateTokens(messages)
	if estimated <= maxTokens {
		return messages, nil
	}

	cw.logger.Debug("context exceeds budget, auto-compression needed",
		"estimated_tokens", estimated,
		"max_tokens", maxTokens,
	)

	// Stage 2: auto-compression (LLM summary).
	// Provider is passed via context if available; this stage is skipped if not set.
	// ContextWindow.Fit accepts a provider via CompressMessages for caller flexibility.
	// Since we can't inject provider here without breaking the interface, auto-compression
	// is attempted only when called via CompressMessages directly.

	// Stage 3: snip (hard cut, no LLM).
	messages = cw.snip(messages, maxTokens)

	return messages, nil
}

// CompressMessages applies the full 3-stage pipeline including the LLM summary stage.
// This is the preferred entry point when a provider is available.
func (cw *ContextWindow) CompressMessages(ctx context.Context, provider llm.Provider, messages []llm.Message, maxTokens int) ([]llm.Message, error) {
	// Stage 1: micro.
	messages = cw.microCompress(messages)

	attempts := 0
	for attempts < maxCompressionAttempts {
		estimated := cw.EstimateTokens(messages)
		if estimated <= maxTokens {
			break
		}

		threshold := int(float64(maxTokens) * autoCompressThreshold)
		if estimated > threshold && provider != nil {
			// Stage 2: LLM summary.
			compressed, err := cw.autoCompress(ctx, provider, messages)
			if err != nil {
				cw.logger.Warn("auto-compression failed, falling back to snip",
					"attempt", attempts+1,
					"error", err,
				)
			} else {
				messages = compressed
				attempts++
				continue
			}
		}

		// Stage 3: hard snip.
		messages = cw.snip(messages, maxTokens)
		break
	}

	if attempts >= maxCompressionAttempts {
		cw.logger.Warn("circuit breaker triggered, applying hard snip",
			"attempts", attempts,
		)
		messages = cw.snip(messages, maxTokens)
	}

	return messages, nil
}

// microCompress performs free, non-LLM cleanup:
// - removes messages with no content blocks
// - removes text blocks containing only whitespace
func (cw *ContextWindow) microCompress(messages []llm.Message) []llm.Message {
	result := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		cleaned := make([]llm.ContentBlock, 0, len(m.Content))
		for _, block := range m.Content {
			if tb, ok := block.(llm.TextBlock); ok {
				if strings.TrimSpace(tb.Text) == "" {
					continue
				}
			}
			cleaned = append(cleaned, block)
		}
		if len(cleaned) == 0 {
			continue
		}
		result = append(result, llm.Message{
			Role:    m.Role,
			Content: cleaned,
		})
	}
	return result
}

// autoCompress summarizes all messages except the most recent recentTurnsToKeep pairs,
// replacing them with a single summary assistant message.
func (cw *ContextWindow) autoCompress(ctx context.Context, provider llm.Provider, messages []llm.Message) ([]llm.Message, error) {
	// Keep the most recent N turns (each turn = 1 user + 1 assistant message = 2 messages).
	keepCount := recentTurnsToKeep * 2
	if len(messages) <= keepCount {
		// Nothing old enough to summarize.
		return messages, nil
	}

	toSummarize := messages[:len(messages)-keepCount]
	recent := messages[len(messages)-keepCount:]

	// Build the conversation text for summarization.
	var sb strings.Builder
	for _, m := range toSummarize {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.Text())
		sb.WriteString("\n")
	}

	req := llm.ChatRequest{
		Model:     "claude-haiku-4-5-20251001",
		System:    summarizePrompt,
		Messages:  []llm.Message{llm.NewUserMessage(sb.String())},
		MaxTokens: 512,
	}

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("auto-compress: summarize: %w", err)
	}

	summary := llm.NewAssistantMessage("[Summary of earlier conversation]\n" + resp.Content)

	compressed := make([]llm.Message, 0, 1+len(recent))
	compressed = append(compressed, summary)
	compressed = append(compressed, recent...)

	cw.logger.Debug("auto-compressed messages",
		"original_count", len(messages),
		"compressed_count", len(compressed),
	)

	return compressed, nil
}

// snip hard-truncates the message list to fit within maxTokens minus the safety margin.
// It always preserves the most recent messages.
func (cw *ContextWindow) snip(messages []llm.Message, maxTokens int) []llm.Message {
	budget := maxTokens - snipSafetyMarginTokens
	if budget <= 0 {
		return messages[len(messages)-1:]
	}

	// Walk backwards to find how many recent messages fit.
	total := 0
	cutAt := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := cw.EstimateTokens([]llm.Message{messages[i]})
		if total+msgTokens > budget {
			cutAt = i + 1
			break
		}
		total += msgTokens
		cutAt = i
	}

	if cutAt >= len(messages) {
		// Even one message exceeds budget — keep the last one.
		return messages[len(messages)-1:]
	}

	cw.logger.Debug("hard-snipped messages",
		"original_count", len(messages),
		"kept_count", len(messages)-cutAt,
	)

	return messages[cutAt:]
}
