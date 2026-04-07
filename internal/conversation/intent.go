package conversation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/llm"
)

// Intent represents the classified intent of a user message.
type Intent string

const (
	IntentQuestion    Intent = "question"
	IntentSimpleTask  Intent = "simple_task"
	IntentComplexTask Intent = "complex_task"
	IntentProject     Intent = "project"
	IntentResearch    Intent = "research"
	IntentWikiQuery   Intent = "wiki_query"
	IntentUnclear     Intent = "unclear"
	IntentChat        Intent = "chat"
)

// IntentResult holds the classified intent and a confidence score.
type IntentResult struct {
	Intent     Intent
	Confidence float64 // 0.0 to 1.0
}

// classificationPrompt is the system prompt used to classify user intent.
const classificationPrompt = `You are an intent classifier. Analyze the user message and conversation history to determine the user's intent.

Respond with ONLY a JSON object in this exact format:
{"intent": "<category>", "confidence": <0.0-1.0>}

Intent categories:
- "question": User is asking a factual or conceptual question
- "simple_task": User wants a straightforward, single-step task (e.g., "rename this file")
- "complex_task": User wants a multi-step task requiring planning (e.g., "refactor this module")
- "project": User is starting or describing a larger project or initiative
- "research": User wants investigation, analysis, or exploration of a topic
- "wiki_query": User is asking about previously stored knowledge, past events, or project history (e.g., "what changed in Stella?", "what do we know about X?")
- "chat": User is making conversation, sharing thoughts, or giving feedback
- "unclear": Intent cannot be determined from the message

Be decisive. Default to "unclear" only when truly ambiguous.`

// LLMClassifier implements IntentClassifier using an LLM provider.
type LLMClassifier struct {
	logger *slog.Logger
}

// NewLLMClassifier creates a new LLM-based intent classifier.
func NewLLMClassifier() *LLMClassifier {
	return &LLMClassifier{
		logger: slog.Default(),
	}
}

// Classify sends the message and recent history to the LLM for intent classification.
// On any parse or provider error, it falls back to IntentUnclear.
func (c *LLMClassifier) Classify(ctx context.Context, provider llm.Provider, message string, history []llm.Message) (Intent, error) {
	// Build context from recent history (last 4 turns to keep prompt small).
	recentHistory := history
	if len(recentHistory) > 8 {
		recentHistory = recentHistory[len(recentHistory)-8:]
	}

	messages := make([]llm.Message, 0, len(recentHistory)+1)
	messages = append(messages, recentHistory...)
	messages = append(messages, llm.NewUserMessage(
		fmt.Sprintf("Classify the intent of this message: %q", message),
	))

	req := llm.ChatRequest{
		Messages:    messages,
		System:      classificationPrompt,
		MaxTokens:   64,
		Temperature: 0.0,
	}

	resp, err := provider.Chat(ctx, req)
	if err != nil {
		return IntentUnclear, fmt.Errorf("classify intent: provider chat: %w", err)
	}

	result, err := parseIntentResponse(resp.Content)
	if err != nil {
		c.logger.Warn("intent parse failed, using unclear",
			"response", resp.Content,
			"error", err,
		)
		return IntentUnclear, nil
	}

	c.logger.Debug("classified intent",
		"intent", result.Intent,
		"confidence", result.Confidence,
	)

	return result.Intent, nil
}

// parseIntentResponse parses the LLM's JSON response into an IntentResult.
// It is tolerant of surrounding whitespace and markdown code fences.
func parseIntentResponse(raw string) (IntentResult, error) {
	raw = strings.TrimSpace(raw)
	// Strip markdown code fences if present.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// Extract JSON object boundaries.
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end <= start {
		return IntentResult{}, fmt.Errorf("no JSON object found in response: %q", raw)
	}
	raw = raw[start : end+1]

	// Manual parse to avoid importing encoding/json for a tiny struct.
	intent := extractJSONString(raw, "intent")
	confidence := extractJSONFloat(raw, "confidence")

	if intent == "" {
		return IntentResult{}, fmt.Errorf("missing intent field in response: %q", raw)
	}

	parsed := Intent(intent)
	switch parsed {
	case IntentQuestion, IntentSimpleTask, IntentComplexTask,
		IntentProject, IntentResearch, IntentUnclear, IntentChat:
		// valid
	default:
		return IntentResult{}, fmt.Errorf("unknown intent category %q", intent)
	}

	return IntentResult{Intent: parsed, Confidence: confidence}, nil
}

// extractJSONString extracts a string value from a flat JSON object by key.
func extractJSONString(obj, key string) string {
	needle := fmt.Sprintf("%q:", key)
	// Also try without quotes around key for lenient parsing.
	idx := strings.Index(obj, needle)
	if idx == -1 {
		needle = fmt.Sprintf(`"%s":`, key)
		idx = strings.Index(obj, needle)
	}
	if idx == -1 {
		return ""
	}
	rest := strings.TrimSpace(obj[idx+len(needle):])
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	end := strings.Index(rest[1:], `"`)
	if end == -1 {
		return ""
	}
	return rest[1 : end+1]
}

// extractJSONFloat extracts a float64 value from a flat JSON object by key.
func extractJSONFloat(obj, key string) float64 {
	needle := fmt.Sprintf(`"%s":`, key)
	idx := strings.Index(obj, needle)
	if idx == -1 {
		return 0.0
	}
	rest := strings.TrimSpace(obj[idx+len(needle):])
	var f float64
	fmt.Sscanf(rest, "%f", &f)
	return f
}
