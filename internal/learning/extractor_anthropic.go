package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stello/elnath/internal/llm"
)

const defaultAnthropicExtractorModel = "claude-haiku-4-5"

const extractionSystemPrompt = `You extract lessons from agent runs for Elnath's learning store.
Output STRICT JSON matching the lessons schema. No commentary, no code fences.

Rules:
- 0-3 lessons per run. Prefer emitting none over forcing a weak lesson.
- lesson.text <= 200 chars. lesson.rationale <= 200 chars.
- Update-not-duplicate: if your finding already exists in the provided existing-lessons manifest (by topic+text similarity), do not emit.
- confidence: "high" only when evidence is direct and repeatable. "medium" otherwise. "low" rarely.
- persona_param MUST be one of: caution | persistence | verbosity | curiosity.
- persona_direction MUST be one of: increase | decrease | neutral.
- persona_magnitude MUST be one of: small | medium | large.
- Include persona fields only when a behavior change is clearly supported.
- Never emit absolute numeric delta values.

Schema (top-level):
{ "lessons": [ { "topic": string, "text": string, "rationale": string,
                 "evidence": [string] (optional), "confidence": "high"|"medium"|"low",
                 "persona_param": string (optional),
                 "persona_direction": string (optional),
                 "persona_magnitude": string (optional) } ] }`

type AnthropicExtractor struct {
	provider     llm.Provider
	model        string
	systemPrefix string
}

// AnthropicExtractorOption configures an AnthropicExtractor.
type AnthropicExtractorOption func(*AnthropicExtractor)

// WithSystemPrefix prepends text to the extraction system prompt. Used as a
// last-resort workaround when the provider's OAuth scope rejects requests that
// do not start with a specific identity signature (e.g. Claude Code OAuth).
func WithSystemPrefix(prefix string) AnthropicExtractorOption {
	return func(e *AnthropicExtractor) { e.systemPrefix = prefix }
}

func NewAnthropicExtractor(p llm.Provider, model string, opts ...AnthropicExtractorOption) *AnthropicExtractor {
	// An empty model is passed through so the underlying provider uses its own
	// default (relevant when the extractor reuses the main provider — e.g.
	// Codex OAuth — whose default model is not claude-haiku-4-5).
	e := &AnthropicExtractor{provider: p, model: llm.ResolveModel(model)}
	for _, o := range opts {
		o(e)
	}
	return e
}

func (a *AnthropicExtractor) Extract(ctx context.Context, req ExtractRequest) ([]Lesson, error) {
	if a == nil || a.provider == nil {
		return nil, fmt.Errorf("anthropic extract: provider unavailable")
	}
	system, user := buildExtractionPrompt(req)
	if a.systemPrefix != "" {
		system = a.systemPrefix + system
	}
	resp, err := a.provider.Chat(ctx, llm.ChatRequest{
		Model:       a.model,
		System:      system,
		Messages:    []llm.Message{llm.NewUserMessage(user)},
		MaxTokens:   1024,
		Temperature: 0,
		EnableCache: true,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic extract: %w", err)
	}
	if dumpPath := os.Getenv("ELNATH_LESSON_DUMP"); dumpPath != "" && resp != nil {
		_ = os.WriteFile(dumpPath, []byte(fmt.Sprintf(
			"== SYSTEM ==\n%s\n\n== USER ==\n%s\n\n== MODEL ==\n%s\n\n== RESPONSE ==\n%s\n",
			system, user, a.model, resp.Content,
		)), 0o600)
	}
	return parseLessonResponse(resp.Content, req)
}

func buildExtractionPrompt(req ExtractRequest) (system string, user string) {
	manifestJSON := "[]"
	if len(req.ExistingLessons) > 0 {
		if data, err := json.Marshal(req.ExistingLessons); err == nil {
			manifestJSON = string(data)
		}
	}
	compactSummary := strings.TrimSpace(req.CompactSummary)
	if compactSummary == "" {
		compactSummary = "(none)"
	}
	user = fmt.Sprintf(`## Existing lessons (recent 50)
%s

## This run
Topic: %s
Workflow: %s
Finish reason: %s
Iterations: %d/%d
Retry count: %d

## Tool stats
%s

## Compact summary
%s

Return JSON only.`, manifestJSON, strings.TrimSpace(req.Topic), strings.TrimSpace(req.Workflow), strings.TrimSpace(req.FinishReason), req.Iterations, req.MaxIterations, req.RetryCount, formatExtractionToolStats(req.ToolStats), compactSummary)
	return extractionSystemPrompt, user
}

func parseLessonResponse(content string, _ ExtractRequest) ([]Lesson, error) {
	type lessonEnvelope struct {
		Lessons []struct {
			Topic            string   `json:"topic"`
			Text             string   `json:"text"`
			Rationale        string   `json:"rationale"`
			Evidence         []string `json:"evidence"`
			Confidence       string   `json:"confidence"`
			PersonaParam     string   `json:"persona_param"`
			PersonaDirection string   `json:"persona_direction"`
			PersonaMagnitude string   `json:"persona_magnitude"`
		} `json:"lessons"`
	}
	var envelope lessonEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &envelope); err != nil {
		return nil, fmt.Errorf("parse lesson response: %w", err)
	}
	out := make([]Lesson, 0, len(envelope.Lessons))
	for _, entry := range envelope.Lessons {
		topic := strings.TrimSpace(entry.Topic)
		text := strings.TrimSpace(entry.Text)
		rationale := strings.TrimSpace(entry.Rationale)
		confidence := normalizeConfidence(entry.Confidence)
		if topic == "" || text == "" || rationale == "" || confidence == "" {
			continue
		}
		param := strings.TrimSpace(entry.PersonaParam)
		direction := strings.TrimSpace(entry.PersonaDirection)
		magnitude := strings.TrimSpace(entry.PersonaMagnitude)
		if !validPersonaParam(param) || !validPersonaDirection(direction) || !validPersonaMagnitude(magnitude) {
			continue
		}
		lesson := Lesson{
			Topic:            topic,
			Text:             truncate(text, maxLessonTextLen),
			Rationale:        truncate(rationale, maxLessonTextLen),
			Confidence:       confidence,
			PersonaParam:     param,
			PersonaDirection: direction,
			PersonaMagnitude: magnitude,
			Created:          time.Now().UTC(),
		}
		if len(entry.Evidence) > 0 {
			lesson.Evidence = append([]string(nil), entry.Evidence...)
		}
		out = append(out, lesson)
	}
	return out, nil
}

func formatExtractionToolStats(stats []AgentToolStat) string {
	if len(stats) == 0 {
		return "name | calls | errors | total_ms\n(none) | 0 | 0 | 0"
	}
	var b strings.Builder
	b.WriteString("name | calls | errors | total_ms\n")
	for _, stat := range stats {
		fmt.Fprintf(&b, "%s | %d | %d | %d\n", stat.Name, stat.Calls, stat.Errors, stat.TotalTime.Milliseconds())
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func normalizeConfidence(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "high", "medium", "low":
		return v
	default:
		return ""
	}
}

func validPersonaParam(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	switch v {
	case "caution", "persistence", "verbosity", "curiosity":
		return true
	default:
		return false
	}
}

func validPersonaDirection(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	switch v {
	case "increase", "decrease", "neutral":
		return true
	default:
		return false
	}
}

func validPersonaMagnitude(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	switch v {
	case "small", "medium", "large":
		return true
	default:
		return false
	}
}
