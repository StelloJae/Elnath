package conversation

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
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

	// DefaultMemoryLimitMB is the default process-Alloc budget used by
	// CompressMessages' belt-and-suspenders memory-pressure guard. Runtime
	// callers pass this via WithMemoryLimitContext (or Manager.WithMemoryLimitMB)
	// to force snip-fallback before the LLM summary call when the process is
	// already close to OOM.
	DefaultMemoryLimitMB = 512
)

// memoryLimitContextKey keys the memory-pressure budget on a context so that
// CompressMessages can opt into the snip-fallback path when the process is
// under memory pressure. The value is the limit in megabytes; <= 0 disables.
type memoryLimitContextKey struct{}

// WithMemoryLimitContext returns a derived context that carries an Alloc
// budget in megabytes. CompressMessages consults this budget before each
// LLM-backed compression attempt and forces a hard snip when process Alloc
// already exceeds it, avoiding a runaway summarizer call under memory
// pressure. A non-positive value is a no-op.
func WithMemoryLimitContext(ctx context.Context, mb int) context.Context {
	if mb <= 0 {
		return ctx
	}
	return context.WithValue(ctx, memoryLimitContextKey{}, mb)
}

func memoryLimitFromContext(ctx context.Context) int {
	v, _ := ctx.Value(memoryLimitContextKey{}).(int)
	return v
}

// CheckMemoryPressure reports whether the current process Alloc exceeds the
// given budget (in megabytes). A non-positive budget disables the check and
// always returns false.
func CheckMemoryPressure(maxAllocMB int) bool {
	if maxAllocMB <= 0 {
		return false
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Alloc > uint64(maxAllocMB)*1024*1024
}

// ContextWindow manages token budget and message compression via a 3-stage pipeline.
type ContextWindow struct {
	logger         *slog.Logger
	threshold      float64 // fraction of maxTokens at which auto-compression triggers (0.0-1.0)
	onAutoCompress func()
}

// OnAutoCompress registers a callback invoked after a successful Stage 2 LLM
// compression run. Used to reset read-dedup caches so the agent can re-examine
// files whose memory was summarized away. Safe to call with nil to clear.
func (cw *ContextWindow) OnAutoCompress(fn func()) {
	cw.onAutoCompress = fn
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

	memoryLimitMB := memoryLimitFromContext(ctx)

	attempts := 0
	for attempts < maxCompressionAttempts {
		estimated := cw.EstimateTokens(messages)
		if estimated <= maxTokens {
			break
		}

		// Break-glass: if process Alloc is already past the configured budget,
		// skip the LLM summary call (which allocates a full request copy) and
		// fall through to snip, which only reslices the existing backing array.
		if CheckMemoryPressure(memoryLimitMB) {
			cw.logger.Warn("memory pressure detected, forcing hard snip",
				"attempt", attempts+1,
				"limit_mb", memoryLimitMB,
			)
			messages = cw.snip(messages, maxTokens)
			break
		}

		threshold := int(float64(maxTokens) * cw.threshold)
		if estimated > threshold && provider != nil {
			// Stage 2: LLM summary.
			compressed, err := cw.autoCompress(ctx, provider, messages)
			switch {
			case err != nil:
				cw.logger.Warn("auto-compression failed, falling back to snip",
					"attempt", attempts+1,
					"error", err,
				)
			case !validCompressionResult(compressed):
				cw.logger.Warn("auto-compression produced invalid result, falling back to snip",
					"attempt", attempts+1,
					"result_count", len(compressed),
				)
			default:
				messages = compressed
				attempts++
				if cw.onAutoCompress != nil {
					cw.onAutoCompress()
				}
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

// validCompressionResult returns true when an LLM summary output preserves
// the post-compaction invariants: at least one message, the first message
// has a populated role, and its text content is non-empty. Invalid output
// (e.g. an empty completion from the summarizer) should fall through to the
// snip path rather than propagate an empty assistant shell.
func validCompressionResult(messages []llm.Message) bool {
	if len(messages) == 0 {
		return false
	}
	first := messages[0]
	if first.Role == "" {
		return false
	}
	return strings.TrimSpace(first.Text()) != ""
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

// autoCompress collapses older messages into one summary while preserving the
// most recent turns verbatim.
func (cw *ContextWindow) autoCompress(ctx context.Context, provider llm.Provider, messages []llm.Message) ([]llm.Message, error) {
	keepCount := recentTurnsToKeep * 2
	if len(messages) <= keepCount {
		return messages, nil
	}

	toCompress := messages[:len(messages)-keepCount]
	recent := messages[len(messages)-keepCount:]
	return cw.flatSummarize(ctx, provider, toCompress, recent)
}

// flatSummarize summarizes older messages into a single assistant message.
func (cw *ContextWindow) flatSummarize(ctx context.Context, provider llm.Provider, toSummarize, recent []llm.Message) ([]llm.Message, error) {
	request := newSessionSummaryRequest(toSummarize)
	if existingSummary, summaryIndex, ok := latestStructuredSummary(toSummarize); ok {
		newMessages := toSummarize[summaryIndex+1:]
		if len(newMessages) == 0 {
			return nil, fmt.Errorf("auto-compress: no new messages after structured summary")
		}
		request = iterativeSummaryRequest(existingSummary, newMessages)
	}

	resp, err := provider.Chat(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("auto-compress: summarize: %w", err)
	}

	summaryText := resp.Content
	if _, ok := parseStructuredSummary(summaryText); !ok {
		cw.logger.Warn("structured summary malformed, falling back to legacy summary")
		legacyResp, legacyErr := provider.Chat(ctx, legacySummaryRequest(toSummarize))
		if legacyErr != nil {
			return nil, fmt.Errorf("auto-compress: legacy fallback: %w", legacyErr)
		} else {
			summaryText = legacyResp.Content
		}
	}

	summary := llm.NewAssistantMessage(summaryText)

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
