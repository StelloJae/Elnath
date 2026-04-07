package research

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
)

// ExperimentResult captures the outcome of testing a hypothesis.
type ExperimentResult struct {
	HypothesisID string         `json:"hypothesis_id"`
	Findings     string         `json:"findings"`
	Evidence     string         `json:"evidence"`
	Confidence   string         `json:"confidence"`
	Supported    bool           `json:"supported"`
	Usage        llm.UsageStats `json:"-"`
}

// ExperimentRunner executes hypothesis test plans via the agent loop.
type ExperimentRunner struct {
	provider llm.Provider
	tools    *tools.Registry
	model    string
	logger   *slog.Logger
}

// NewExperimentRunner creates an ExperimentRunner.
func NewExperimentRunner(provider llm.Provider, toolReg *tools.Registry, model string, logger *slog.Logger) *ExperimentRunner {
	return &ExperimentRunner{
		provider: provider,
		tools:    toolReg,
		model:    model,
		logger:   logger,
	}
}

const experimentSystemPrompt = `You are a research experiment executor. Test the following hypothesis by gathering evidence using available tools. Investigate thoroughly, then provide your findings.

After investigation, you MUST end your final message with this exact JSON format on its own line:
{"findings":"...","evidence":"...","confidence":"high|medium|low","supported":true|false}

- findings: a clear summary of what you discovered
- evidence: specific data or observations that support your conclusion
- confidence: how confident you are (high, medium, or low)
- supported: whether the hypothesis was supported by the evidence`

// Run executes a single experiment for the given hypothesis.
// It creates an agent with the hypothesis test plan as the user prompt,
// runs the agent loop, and extracts structured results.
func (r *ExperimentRunner) Run(ctx context.Context, hyp Hypothesis) (*ExperimentResult, error) {
	r.logger.Info("running experiment",
		"hypothesis_id", hyp.ID,
		"statement", hyp.Statement,
	)

	a := agent.New(
		r.provider,
		r.tools,
		agent.WithModel(r.model),
		agent.WithSystemPrompt(experimentSystemPrompt),
		agent.WithMaxIterations(20),
		agent.WithLogger(r.logger),
	)

	userMsg := fmt.Sprintf("Hypothesis: %s\n\nTest Plan: %s\n\nExecute this test plan and report results.", hyp.Statement, hyp.TestPlan)
	messages := []llm.Message{llm.NewUserMessage(userMsg)}

	result, err := a.Run(ctx, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("experiment: run agent: %w", err)
	}

	lastText := extractLastAssistantText(result.Messages)

	expResult := parseExperimentResult(lastText, hyp.ID)
	expResult.Usage = result.Usage

	r.logger.Info("experiment complete",
		"hypothesis_id", hyp.ID,
		"supported", expResult.Supported,
		"confidence", expResult.Confidence,
	)

	return expResult, nil
}

func extractLastAssistantText(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleAssistant {
			return messages[i].Text()
		}
	}
	return ""
}

func parseExperimentResult(text string, hypothesisID string) *ExperimentResult {
	// Try to find a JSON object in the text.
	result := &ExperimentResult{
		HypothesisID: hypothesisID,
	}

	jsonStr := extractJSON(text)
	if jsonStr != "" {
		var parsed struct {
			Findings   string `json:"findings"`
			Evidence   string `json:"evidence"`
			Confidence string `json:"confidence"`
			Supported  bool   `json:"supported"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err == nil {
			result.Findings = parsed.Findings
			result.Evidence = parsed.Evidence
			result.Confidence = parsed.Confidence
			result.Supported = parsed.Supported
			return result
		}
	}

	// Fallback: use raw text as findings with low confidence.
	result.Findings = text
	result.Confidence = "low"
	result.Supported = false
	return result
}

func extractJSON(text string) string {
	// Search backwards for the last JSON object — the structured result
	// is expected at the end of the assistant's response.
	lastBrace := strings.LastIndex(text, "}")
	if lastBrace == -1 {
		return ""
	}

	// Walk backwards to find the matching opening brace.
	depth := 0
	for i := lastBrace; i >= 0; i-- {
		switch text[i] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				candidate := text[i : lastBrace+1]
				if json.Valid([]byte(candidate)) {
					return candidate
				}
			}
		}
	}
	return ""
}
