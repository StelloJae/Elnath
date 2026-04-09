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

Decision rules:
- Prefer "wiki_query" over "question" when the user is asking about remembered project history, prior sessions, stored notes, or "what changed / what do we know".
- Prefer "research" over "question" when the user wants investigation, comparison, analysis, or evidence gathering rather than a direct factual answer.
- Prefer "project" over "complex_task" when the user is kicking off a larger initiative, release, or multi-phase build. Creation verbs ("만들어", "만들어줘", "build", "create", "새로", "new", "make", "start") combined with scope nouns ("project", "프로젝트", "app", "application", "tool", "system", "service", "API", "platform", "CLI") strongly signal "project" intent.
- Use "complex_task" for bounded execution work inside an existing project (e.g., refactoring a module, fixing a bug with multiple steps).
- Prefer "simple_task" only for clearly bounded one-step edits or commands.
- Prefer "question" for direct factual/conceptual questions that do not require stored-history lookup.

Examples:
- "프로젝트 만들어줘" → {"intent": "project", "confidence": 0.95}
- "Create a new REST API" → {"intent": "project", "confidence": 0.95}
- "Build me a CLI tool" → {"intent": "project", "confidence": 0.95}
- "Refactor the auth module" → {"intent": "complex_task", "confidence": 0.9}
- "Fix the bug in login" → {"intent": "simple_task", "confidence": 0.85}
- "What is X?" → {"intent": "question", "confidence": 0.95}
- "Search wiki for Y" → {"intent": "wiki_query", "confidence": 0.9}

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
	// Filter to text-only messages — tool_use/tool_result blocks break the
	// API when the slice boundary splits a call/result pair.
	textOnly := filterTextMessages(history)
	if len(textOnly) > 8 {
		textOnly = textOnly[len(textOnly)-8:]
	}

	messages := make([]llm.Message, 0, len(textOnly)+1)
	messages = append(messages, textOnly...)
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

// filterTextMessages returns only messages whose content blocks are all text.
// This strips tool_use/tool_result turns so the classifier never sends
// orphaned tool results to the LLM provider.
func filterTextMessages(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		textOnly := true
		for _, b := range m.Content {
			if _, ok := b.(llm.TextBlock); !ok {
				textOnly = false
				break
			}
		}
		if textOnly && len(m.Content) > 0 {
			out = append(out, m)
		}
	}
	return out
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
		IntentProject, IntentResearch, IntentWikiQuery, IntentUnclear, IntentChat:
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
