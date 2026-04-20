package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

// Hypothesis is a testable claim generated from wiki knowledge.
type Hypothesis struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	Rationale string `json:"rationale"`
	TestPlan  string `json:"test_plan"`
	Priority  int    `json:"priority"`
}

// RoundResult captures one hypothesis-experiment cycle.
type RoundResult struct {
	Round      int
	Hypothesis Hypothesis
	Result     ExperimentResult
}

// ResearchResult is the final output of a multi-round research session.
type ResearchResult struct {
	Topic     string
	Rounds    []RoundResult
	Summary   string
	TotalCost float64
}

// HypothesisGenerator produces hypotheses from wiki knowledge and prior results.
type HypothesisGenerator struct {
	provider  llm.Provider
	model     string
	logger    *slog.Logger
	pipeline  PromptPrefixRenderer
	sessionID string
}

// NewHypothesisGenerator creates a HypothesisGenerator.
func NewHypothesisGenerator(provider llm.Provider, model string, logger *slog.Logger) *HypothesisGenerator {
	return &HypothesisGenerator{
		provider: provider,
		model:    model,
		logger:   logger,
	}
}

// WithPipeline wires a PromptPrefixRenderer and session scope so Generate
// prepends a base-prompt prefix to the hardcoded hypothesis instruction.
// Nil pipeline preserves the legacy behaviour. Returns g for chaining.
func (g *HypothesisGenerator) WithPipeline(p PromptPrefixRenderer, sessionID string) *HypothesisGenerator {
	g.pipeline = p
	g.sessionID = sessionID
	return g
}

const hypothesisSystemPrompt = `You are a research hypothesis generator. Given a topic and existing knowledge, generate 1-3 testable hypotheses. Each hypothesis must be specific, falsifiable, and include a concrete test plan.

Respond with ONLY a JSON array in this exact format, no other text:
[{"id":"H1","statement":"...","rationale":"...","test_plan":"...","priority":1}]

Priority 1 is highest. Assign priorities based on expected impact and feasibility.`

// Generate creates 1-3 hypotheses about the given topic.
// It uses wiki search results as existing knowledge and prior round results
// to avoid repeating failed approaches.
func (g *HypothesisGenerator) Generate(ctx context.Context, topic string, knowledge []wiki.SearchResult, prior []RoundResult) ([]Hypothesis, error) {
	userMsg := buildHypothesisPrompt(topic, knowledge, prior)

	g.logger.Debug("generating hypotheses",
		"topic", topic,
		"knowledge_count", len(knowledge),
		"prior_rounds", len(prior),
	)

	systemPrompt := hypothesisSystemPrompt
	if g.pipeline != nil {
		prefix, perr := g.pipeline.RenderPromptPrefix(ctx, Invocation{
			SessionID: g.sessionID,
			Stage:     StageHypothesis,
			Topic:     topic,
			UserInput: userMsg,
		})
		switch {
		case perr != nil:
			g.logger.Warn("research: hypothesis pipeline prefix render failed; using legacy fallback",
				"error", perr)
		case prefix != "":
			systemPrompt = prefix + "\n\n" + hypothesisSystemPrompt
		}
	}

	resp, err := g.provider.Chat(ctx, llm.ChatRequest{
		Model:       g.model,
		System:      systemPrompt,
		Messages:    []llm.Message{llm.NewUserMessage(userMsg)},
		MaxTokens:   2048,
		Temperature: 0.7,
	})
	if err != nil {
		return nil, fmt.Errorf("hypothesis: generate: %w", err)
	}

	hypotheses, err := parseHypotheses(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("hypothesis: parse response: %w", err)
	}

	if len(hypotheses) == 0 {
		return nil, fmt.Errorf("hypothesis: generate: LLM returned no hypotheses")
	}

	sort.Slice(hypotheses, func(i, j int) bool {
		return hypotheses[i].Priority < hypotheses[j].Priority
	})

	g.logger.Info("hypotheses generated",
		"topic", topic,
		"count", len(hypotheses),
	)

	return hypotheses, nil
}

func buildHypothesisPrompt(topic string, knowledge []wiki.SearchResult, prior []RoundResult) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Research Topic\n%s\n", topic)

	if len(knowledge) > 0 {
		b.WriteString("\n## Existing Knowledge\n")
		for i, sr := range knowledge {
			fmt.Fprintf(&b, "\n### Source %d: %s\n", i+1, sr.Page.Title)
			content := sr.Page.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			b.WriteString(content)
			b.WriteByte('\n')
		}
	}

	if len(prior) > 0 {
		b.WriteString("\n## Prior Research Rounds\n")
		b.WriteString("Avoid repeating approaches that failed. Build on successful findings.\n")
		for _, rr := range prior {
			fmt.Fprintf(&b, "\n### Round %d — %s\n", rr.Round, rr.Hypothesis.Statement)
			fmt.Fprintf(&b, "- Supported: %v\n", rr.Result.Supported)
			fmt.Fprintf(&b, "- Confidence: %s\n", rr.Result.Confidence)
			fmt.Fprintf(&b, "- Findings: %s\n", rr.Result.Findings)
		}
	}

	return b.String()
}

func parseHypotheses(text string) ([]Hypothesis, error) {
	text = strings.TrimSpace(text)

	// Strip markdown code fences if present.
	if strings.HasPrefix(text, "```") {
		lines := strings.SplitN(text, "\n", 2)
		if len(lines) == 2 {
			text = lines[1]
		}
		if idx := strings.LastIndex(text, "```"); idx >= 0 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}

	// Find the JSON array boundaries.
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON array found in response: %.200s", text)
	}
	text = text[start : end+1]

	var hypotheses []Hypothesis
	if err := json.Unmarshal([]byte(text), &hypotheses); err != nil {
		return nil, fmt.Errorf("unmarshal hypotheses: %w", err)
	}

	return hypotheses, nil
}
