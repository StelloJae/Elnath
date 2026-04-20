package reflection

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/llm"
)

// Strategy enumerates closed-form remediation hints the reflection engine may
// suggest. Storing a closed enum (instead of free-text) is the C2 mitigation:
// a Phase 1 retry executor can safely branch on these values without parsing
// adversarial model output.
type Strategy string

const (
	StrategyRetrySmallerScope Strategy = "retry_smaller_scope"
	StrategyFallbackProvider  Strategy = "fallback_provider"
	StrategyCompressContext   Strategy = "compress_context"
	StrategyAbort             Strategy = "abort"
	StrategyUnknown           Strategy = "unknown"
)

// validStrategy returns true iff s is one of the five closed enum values.
func validStrategy(s Strategy) bool {
	switch s {
	case StrategyRetrySmallerScope,
		StrategyFallbackProvider,
		StrategyCompressContext,
		StrategyAbort,
		StrategyUnknown:
		return true
	}
	return false
}

// TaskMeta carries non-LLM task identity for the observation record.
// Principal/ProjectID enrich the record without participating in trigger,
// skip, or strategy evaluation (spec §3.1 Phase 0 observe-only invariant).
type TaskMeta struct {
	TaskID    string
	SessionID string
	Principal string
	ProjectID string
}

// Report is the structured reflection output persisted to self_heal_attempts.jsonl.
type Report struct {
	Fingerprint       Fingerprint
	FinishReason      string
	ErrorCategory     string
	SuggestedStrategy Strategy
	Reasoning         string
	TaskSummary       string
}

// Input is the fully-populated reflection request payload.
type Input struct {
	Transcript    []llm.Message
	ErrorSummary  string
	TaskMeta      TaskMeta
	Fingerprint   Fingerprint
	FinishReason  string
	ErrorCategory string
}

// Engine performs one reflection call and returns a Report.
// Phase 0: the report is observed, never executed.
type Engine interface {
	Reflect(ctx context.Context, in Input) (Report, error)
}

// LLMEngine is the default Engine. It issues a single llm.Provider.Chat call
// with a prompt-embedded JSON contract (ChatRequest lacks a response_format
// field), then parses the reply into a Report. Schema or enum failures degrade
// to StrategyUnknown rather than returning an error — the observation is still
// recorded so Phase 1 can measure schema fail rate.
type LLMEngine struct {
	provider  llm.Provider
	model     string
	timeout   time.Duration
	maxTurns  int
	perTurnKB int
}

// LLMEngineOption configures an LLMEngine.
type LLMEngineOption func(*LLMEngine)

// WithEngineTimeout overrides the per-reflection call timeout.
func WithEngineTimeout(d time.Duration) LLMEngineOption {
	return func(e *LLMEngine) { e.timeout = d }
}

// WithEngineMaxTurns caps how many trailing transcript turns are included.
func WithEngineMaxTurns(n int) LLMEngineOption {
	return func(e *LLMEngine) { e.maxTurns = n }
}

// NewLLMEngine constructs an LLMEngine with spec defaults (timeout=15s, 20 turns).
func NewLLMEngine(provider llm.Provider, model string, opts ...LLMEngineOption) *LLMEngine {
	e := &LLMEngine{
		provider:  provider,
		model:     model,
		timeout:   15 * time.Second,
		maxTurns:  20,
		perTurnKB: 1,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

const reflectionSystemPrompt = `You are a post-mortem analyst for an autonomous coding agent.
Given a failed task transcript, you must pick exactly one remediation strategy from a closed list.
Your output MUST be a single JSON object — no prose, no markdown, no code fences.`

// Reflect issues the reflection call and returns a Report. A malformed or
// off-enum response yields Strategy=unknown (no error). Only provider/network
// errors propagate.
func (e *LLMEngine) Reflect(ctx context.Context, in Input) (Report, error) {
	callCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	prompt := buildReflectionUserPrompt(in, e.maxTurns, e.perTurnKB*1024)
	req := llm.ChatRequest{
		Model:     e.model,
		Messages:  []llm.Message{llm.NewUserMessage(prompt)},
		System:    reflectionSystemPrompt,
		MaxTokens: 512,
	}
	resp, err := e.provider.Chat(callCtx, req)
	if err != nil {
		return Report{}, fmt.Errorf("reflection: chat: %w", err)
	}
	return parseReflectionReport(in, resp.Content), nil
}

// buildReflectionUserPrompt formats the reflection request with the closed
// strategy enum and trailing transcript snippets.
func buildReflectionUserPrompt(in Input, maxTurns, perTurnBytes int) string {
	var b strings.Builder
	b.WriteString("Task ended with finish_reason=")
	b.WriteString(in.FinishReason)
	b.WriteString("\nerror_category=")
	b.WriteString(in.ErrorCategory)
	b.WriteString("\nerror_summary=")
	b.WriteString(in.ErrorSummary)
	b.WriteString("\n\nRecent transcript (trailing ")
	fmt.Fprintf(&b, "%d", maxTurns)
	b.WriteString(" turns, each truncated):\n")

	turns := in.Transcript
	if len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}
	for i, msg := range turns {
		snippet := compactTurn(msg, perTurnBytes)
		fmt.Fprintf(&b, "[%d:%s] %s\n", i, msg.Role, snippet)
	}

	b.WriteString(`
Choose exactly one suggested_strategy from:
- retry_smaller_scope
- fallback_provider
- compress_context
- abort
- unknown

Respond with JSON only:
{"suggested_strategy":"<one>","reasoning":"<<=1 sentence>","task_summary":"<what the task was attempting>"}`)
	return b.String()
}

// compactTurn renders a llm.Message as a single-line preview capped at limit bytes.
// Used purely for reflection input — not persisted verbatim.
func compactTurn(msg llm.Message, limit int) string {
	text := msg.Text()
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	if len(text) > limit && limit > 0 {
		text = text[:limit] + "…"
	}
	return text
}

// parseReflectionReport converts raw model output into a Report. Malformed or
// off-enum output still returns a valid Report with StrategyUnknown, so the
// observation layer records the failure mode (Phase 1 schema fail-rate KPI).
func parseReflectionReport(in Input, raw string) Report {
	report := Report{
		Fingerprint:       in.Fingerprint,
		FinishReason:      in.FinishReason,
		ErrorCategory:     in.ErrorCategory,
		SuggestedStrategy: StrategyUnknown,
	}

	body := extractJSONObject(raw)
	if body == "" {
		return report
	}

	var parsed struct {
		SuggestedStrategy string `json:"suggested_strategy"`
		Reasoning         string `json:"reasoning"`
		TaskSummary       string `json:"task_summary"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return report
	}

	candidate := Strategy(parsed.SuggestedStrategy)
	if validStrategy(candidate) {
		report.SuggestedStrategy = candidate
	}
	report.Reasoning = parsed.Reasoning
	report.TaskSummary = parsed.TaskSummary
	return report
}

// extractJSONObject scans for the first balanced {...} block. Handles models
// that wrap output in markdown fences or add leading/trailing prose despite
// the system prompt.
func extractJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if escape {
			escape = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}
