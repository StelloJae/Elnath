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
	logger    *slog.Logger
	threshold float64 // fraction of maxTokens at which auto-compression triggers (0.0-1.0)
}

// NewContextWindow creates a new ContextWindow manager with the default threshold (80%).
func NewContextWindow() *ContextWindow {
	return &ContextWindow{
		logger:    slog.Default(),
		threshold: autoCompressThreshold,
	}
}

// NewContextWindowWithThreshold creates a ContextWindow with a custom compression threshold.
// Threshold is a fraction (0.0-1.0) of maxTokens at which auto-compression triggers.
func NewContextWindowWithThreshold(threshold float64) *ContextWindow {
	if threshold <= 0 || threshold > 1.0 {
		threshold = autoCompressThreshold
	}
	return &ContextWindow{
		logger:    slog.Default(),
		threshold: threshold,
	}
}

// EstimateTokens estimates the token count for a slice of messages.
// Uses a refined heuristic: ~4 chars/token for English prose, ~3.5 for code/JSON,
// plus per-message overhead for role formatting.
func (cw *ContextWindow) EstimateTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		// Role overhead (~4 tokens per message for formatting).
		total += 4
		for _, block := range m.Content {
			switch b := block.(type) {
			case llm.TextBlock:
				total += estimateTextTokens(b.Text)
			case llm.ToolUseBlock:
				// Tool name + JSON input (JSON is denser than prose).
				total += len(b.Name)/4 + len(b.Input)*2/7
			case llm.ToolResultBlock:
				total += estimateTextTokens(b.Content)
			case llm.ThinkingBlock:
				total += estimateTextTokens(b.Thinking)
			case llm.ImageBlock:
				// Images are roughly 1600 tokens for a standard resolution.
				total += 1600
			}
		}
	}
	return total
}

// estimateTextTokens applies a refined character-based heuristic.
// JSON/code-heavy text uses ~3.5 chars/token; prose uses ~4 chars/token.
func estimateTextTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	// Heuristic: count braces and brackets as a signal for JSON/code density.
	jsonChars := 0
	for _, c := range text {
		if c == '{' || c == '}' || c == '[' || c == ']' || c == '"' {
			jsonChars++
		}
	}
	ratio := float64(jsonChars) / float64(len(text))
	if ratio > 0.05 {
		// JSON/code-heavy: ~3.5 chars per token.
		return len(text) * 2 / 7
	}
	// Prose: ~4 chars per token.
	return len(text) / 4
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

		threshold := int(float64(maxTokens) * cw.threshold)
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

// messageImportance scores a message for compression priority.
// Higher scores mean the message should be preserved longer during compression.
func messageImportance(m llm.Message) int {
	score := 1 // baseline

	for _, block := range m.Content {
		switch b := block.(type) {
		case llm.ToolUseBlock:
			score += 3 // tool calls carry important context
		case llm.ToolResultBlock:
			score += 2
			if b.IsError {
				score += 3 // errors are critical to preserve
			}
		case llm.TextBlock:
			lower := strings.ToLower(b.Text)
			// Decisions and key findings get higher scores.
			for _, marker := range []string{"decision:", "important:", "error:", "warning:", "conclusion:", "plan:"} {
				if strings.Contains(lower, marker) {
					score += 2
					break
				}
			}
		}
	}
	return score
}

// topicSegment groups consecutive messages that belong to the same conversational topic.
type topicSegment struct {
	messages   []llm.Message
	importance int // sum of individual message importances
}

// segmentByTopic groups messages into topic segments.
// A new segment starts at each user message (the natural turn boundary).
func segmentByTopic(messages []llm.Message) []topicSegment {
	var segments []topicSegment
	var current topicSegment

	for _, m := range messages {
		if m.Role == llm.RoleUser && len(current.messages) > 0 {
			segments = append(segments, current)
			current = topicSegment{}
		}
		current.messages = append(current.messages, m)
		current.importance += messageImportance(m)
	}
	if len(current.messages) > 0 {
		segments = append(segments, current)
	}
	return segments
}

// autoCompress performs hierarchical summarization of old messages.
//
// Level 1 (topic-level): groups old messages into topic segments, summarizes
// low-importance segments individually, preserves high-importance segments.
// Level 2 (session-level): if the result is still too large, collapses all
// old summaries into a single session summary.
func (cw *ContextWindow) autoCompress(ctx context.Context, provider llm.Provider, messages []llm.Message) ([]llm.Message, error) {
	keepCount := recentTurnsToKeep * 2
	if len(messages) <= keepCount {
		return messages, nil
	}

	toCompress := messages[:len(messages)-keepCount]
	recent := messages[len(messages)-keepCount:]

	segments := segmentByTopic(toCompress)

	if len(segments) <= 1 {
		// Single segment: fall back to flat summarization.
		return cw.flatSummarize(ctx, provider, toCompress, recent)
	}

	// Determine importance threshold: segments below median importance get summarized.
	threshold := cw.importanceThreshold(segments)

	var compressed []llm.Message
	var toSummarize []topicSegment

	for _, seg := range segments {
		if seg.importance >= threshold {
			// Flush any pending low-importance segments as a summary first.
			if len(toSummarize) > 0 {
				summary, err := cw.summarizeSegments(ctx, provider, toSummarize)
				if err != nil {
					// On failure, keep original messages.
					for _, s := range toSummarize {
						compressed = append(compressed, s.messages...)
					}
				} else {
					compressed = append(compressed, summary)
				}
				toSummarize = nil
			}
			// Preserve high-importance segment as-is.
			compressed = append(compressed, seg.messages...)
		} else {
			toSummarize = append(toSummarize, seg)
		}
	}

	// Flush remaining low-importance segments.
	if len(toSummarize) > 0 {
		summary, err := cw.summarizeSegments(ctx, provider, toSummarize)
		if err != nil {
			for _, s := range toSummarize {
				compressed = append(compressed, s.messages...)
			}
		} else {
			compressed = append(compressed, summary)
		}
	}

	result := append(compressed, recent...)

	cw.logger.Debug("hierarchical compression",
		"original_count", len(messages),
		"segments", len(segments),
		"compressed_count", len(result),
	)

	return result, nil
}

// importanceThreshold returns the median importance across segments.
func (cw *ContextWindow) importanceThreshold(segments []topicSegment) int {
	if len(segments) == 0 {
		return 0
	}
	scores := make([]int, len(segments))
	for i, s := range segments {
		scores[i] = s.importance
	}
	// Simple selection: sort and take median.
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j] < scores[i] {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
	return scores[len(scores)/2]
}

// summarizeSegments collapses multiple topic segments into one summary message.
func (cw *ContextWindow) summarizeSegments(ctx context.Context, provider llm.Provider, segments []topicSegment) (llm.Message, error) {
	var sb strings.Builder
	for i, seg := range segments {
		fmt.Fprintf(&sb, "--- Topic %d ---\n", i+1)
		for _, m := range seg.messages {
			sb.WriteString(m.Role)
			sb.WriteString(": ")
			sb.WriteString(m.Text())
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		System:    summarizePrompt,
		Messages:  []llm.Message{llm.NewUserMessage(sb.String())},
		MaxTokens: 512,
	})
	if err != nil {
		return llm.Message{}, fmt.Errorf("summarize segments: %w", err)
	}

	return llm.NewAssistantMessage("[Summary of earlier topics]\n" + resp.Content), nil
}

// flatSummarize is the original single-pass summarization, used as fallback.
func (cw *ContextWindow) flatSummarize(ctx context.Context, provider llm.Provider, toSummarize, recent []llm.Message) ([]llm.Message, error) {
	var sb strings.Builder
	for _, m := range toSummarize {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.Text())
		sb.WriteString("\n")
	}

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		System:    summarizePrompt,
		Messages:  []llm.Message{llm.NewUserMessage(sb.String())},
		MaxTokens: 512,
	})
	if err != nil {
		return nil, fmt.Errorf("auto-compress: summarize: %w", err)
	}

	summary := llm.NewAssistantMessage("[Summary of earlier conversation]\n" + resp.Content)

	compressed := make([]llm.Message, 0, 1+len(recent))
	compressed = append(compressed, summary)
	compressed = append(compressed, recent...)

	cw.logger.Debug("flat-compressed messages",
		"original_count", len(toSummarize)+len(recent),
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
