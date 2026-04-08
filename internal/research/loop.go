package research

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/wiki"
)

// WikiSearcher is satisfied by *wiki.Index. Defined here following the
// "accept interfaces, return structs" Go convention.
type WikiSearcher interface {
	Search(ctx context.Context, opts wiki.SearchOpts) ([]wiki.SearchResult, error)
}

// Loop orchestrates the research cycle:
// wiki search → hypothesize → experiment → evaluate → wiki write.
type Loop struct {
	hypothesizer *HypothesisGenerator
	experimenter *ExperimentRunner
	wikiIndex    WikiSearcher
	wikiStore    *wiki.Store
	usageTracker *llm.UsageTracker
	provider     llm.Provider
	model        string
	sessionID    string
	maxRounds    int
	costCapUSD   float64
	logger       *slog.Logger
}

// LoopOption configures a Loop.
type LoopOption func(*Loop)

// WithMaxRounds sets the maximum number of research rounds.
func WithMaxRounds(n int) LoopOption {
	return func(l *Loop) { l.maxRounds = n }
}

// WithCostCap sets the maximum USD spend before the loop stops.
func WithCostCap(usd float64) LoopOption {
	return func(l *Loop) { l.costCapUSD = usd }
}

// WithSessionID scopes usage tracking to a specific session.
func WithSessionID(id string) LoopOption {
	return func(l *Loop) { l.sessionID = id }
}

// NewLoop creates a research Loop with sensible defaults.
func NewLoop(
	hypothesizer *HypothesisGenerator,
	experimenter *ExperimentRunner,
	wikiIndex WikiSearcher,
	wikiStore *wiki.Store,
	usageTracker *llm.UsageTracker,
	provider llm.Provider,
	model string,
	logger *slog.Logger,
	opts ...LoopOption,
) *Loop {
	l := &Loop{
		hypothesizer: hypothesizer,
		experimenter: experimenter,
		wikiIndex:    wikiIndex,
		wikiStore:    wikiStore,
		usageTracker: usageTracker,
		provider:     provider,
		model:        model,
		maxRounds:    5,
		costCapUSD:   5.0,
		logger:       logger,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Run executes the full research loop for the given topic.
func (l *Loop) Run(ctx context.Context, topic string) (*ResearchResult, error) {
	var rounds []RoundResult

	for round := 0; round < l.maxRounds; round++ {
		if l.usageTracker != nil {
			cost, err := l.usageTracker.TotalCost(ctx, l.sessionID)
			if err == nil && cost >= l.costCapUSD {
				l.logger.Warn("cost cap reached", "cost", cost, "cap", l.costCapUSD)
				break
			}
		}

		knowledge, _ := l.wikiIndex.Search(ctx, wiki.SearchOpts{Query: topic, Limit: 10})

		hypotheses, err := l.hypothesizer.Generate(ctx, topic, knowledge, rounds)
		if err != nil {
			return nil, fmt.Errorf("research: round %d: generate: %w", round, err)
		}

		for _, hyp := range hypotheses {
			expResult, err := l.experimenter.Run(ctx, hyp)
			if err != nil {
				l.logger.Error("experiment failed", "hypothesis", hyp.ID, "error", err)
				continue
			}

			rounds = append(rounds, RoundResult{
				Round:      round,
				Hypothesis: hyp,
				Result:     *expResult,
			})

			if l.usageTracker != nil {
				_ = l.usageTracker.Record(ctx, l.provider.Name(), l.model, l.sessionID, expResult.Usage)
			}
		}

		l.writeRoundToWiki(ctx, topic, round, rounds)

		if l.shouldStop(rounds) {
			l.logger.Info("convergence detected, stopping", "round", round)
			break
		}
	}

	summary := l.summarize(ctx, topic, rounds)

	var totalCost float64
	if l.usageTracker != nil {
		totalCost, _ = l.usageTracker.TotalCost(ctx, l.sessionID)
	}

	return &ResearchResult{
		Topic:     topic,
		Rounds:    rounds,
		Summary:   summary,
		TotalCost: totalCost,
	}, nil
}

// shouldStop returns true when the research has converged or stagnated.
func (l *Loop) shouldStop(rounds []RoundResult) bool {
	if len(rounds) < 2 {
		return false
	}

	last2 := rounds[len(rounds)-2:]

	allSupported := true
	allHighConf := true
	for _, rr := range last2 {
		if !rr.Result.Supported {
			allSupported = false
		}
		if rr.Result.Confidence != "high" {
			allHighConf = false
		}
	}
	if allSupported && allHighConf {
		return true
	}

	allLowConf := true
	for _, rr := range last2 {
		if rr.Result.Confidence != "low" {
			allLowConf = false
		}
	}
	return allLowConf
}

const summarizeSystemPrompt = `You are a research summarizer. Given the results of multiple research rounds, produce a concise research report. Focus on key findings, evidence, and confidence levels. Be brief and factual.`

// summarize asks the LLM to produce a brief report from all rounds.
// Falls back to concatenating findings if the LLM call fails.
func (l *Loop) summarize(ctx context.Context, topic string, rounds []RoundResult) string {
	if len(rounds) == 0 {
		return "No research rounds completed."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Research: %s\n\n", topic)
	for _, rr := range rounds {
		fmt.Fprintf(&b, "### Round %d — %s\n", rr.Round, rr.Hypothesis.Statement)
		fmt.Fprintf(&b, "- Supported: %v\n", rr.Result.Supported)
		fmt.Fprintf(&b, "- Confidence: %s\n", rr.Result.Confidence)
		fmt.Fprintf(&b, "- Findings: %s\n\n", rr.Result.Findings)
	}

	resp, err := l.provider.Chat(ctx, llm.ChatRequest{
		Model:       l.model,
		System:      summarizeSystemPrompt,
		Messages:    []llm.Message{llm.NewUserMessage(b.String())},
		MaxTokens:   1024,
		Temperature: 0.3,
	})
	if err != nil {
		l.logger.Warn("summarize LLM call failed, using fallback", "error", err)
		return b.String()
	}

	return resp.Content
}

// writeRoundToWiki persists the current round's results as a wiki page.
func (l *Loop) writeRoundToWiki(ctx context.Context, topic string, round int, rounds []RoundResult) {
	sanitized := sanitizeTopic(topic)
	path := fmt.Sprintf("research/%s/round-%d.md", sanitized, round)

	var b strings.Builder
	fmt.Fprintf(&b, "# Research Round %d: %s\n\n", round, topic)

	roundResults := filterByRound(rounds, round)
	for _, rr := range roundResults {
		fmt.Fprintf(&b, "## Hypothesis: %s\n", rr.Hypothesis.Statement)
		fmt.Fprintf(&b, "**Rationale:** %s\n\n", rr.Hypothesis.Rationale)
		fmt.Fprintf(&b, "**Supported:** %v | **Confidence:** %s\n\n", rr.Result.Supported, rr.Result.Confidence)
		fmt.Fprintf(&b, "**Findings:** %s\n\n", rr.Result.Findings)
		if rr.Result.Evidence != "" {
			fmt.Fprintf(&b, "**Evidence:** %s\n\n", rr.Result.Evidence)
		}
	}

	page := &wiki.Page{
		Path:       path,
		Title:      fmt.Sprintf("Research Round %d: %s", round, topic),
		Type:       wiki.PageTypeAnalysis,
		Tags:       []string{"research", topic},
		Content:    b.String(),
		Confidence: "medium",
	}

	if err := l.wikiStore.Upsert(page); err != nil {
		l.logger.Error("failed to write round to wiki", "path", path, "error", err)
	}
}

// sanitizeTopic converts a topic string into a filesystem-safe directory name.
func sanitizeTopic(topic string) string {
	s := strings.ToLower(topic)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1
	}, s)
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// filterByRound returns only the results from the specified round.
func filterByRound(rounds []RoundResult, round int) []RoundResult {
	var out []RoundResult
	for _, rr := range rounds {
		if rr.Round == round {
			out = append(out, rr)
		}
	}
	return out
}
