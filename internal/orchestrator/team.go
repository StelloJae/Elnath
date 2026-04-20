package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
)

// TeamWorkflow splits a task into parallel subtasks, runs each in its own
// Agent goroutine, and then synthesises the results with a final LLM call.
type TeamWorkflow struct {
	logger *slog.Logger
}

// NewTeamWorkflow returns a TeamWorkflow ready to use.
func NewTeamWorkflow() *TeamWorkflow {
	return &TeamWorkflow{logger: slog.Default()}
}

// Name implements Workflow.
func (w *TeamWorkflow) Name() string { return "team" }

// subtask is a single unit of parallel work produced by the planner.
type subtask struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Instruction string `json:"instruction"`
}

// subtaskResult carries the output of one goroutine.
type subtaskResult struct {
	subtask subtask
	result  *agent.RunResult
	stream  string
	err     error
}

// Run implements Workflow.
// 1. planSubtasks — ask LLM to split the main task into independent subtasks.
// 2. Launch one goroutine per subtask; each goroutine runs its own Agent.
// 3. Collect results via a channel; propagate any context cancellation.
// 4. synthesise — ask LLM to combine the subtask results into a final answer.
func (w *TeamWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
	if input.Sink == nil {
		input.Sink = event.NopSink{}
	}
	input.Sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: "[team] planning subtasks\n"})
	workflowInput := input
	workflowInput.Messages = append(workflowInput.Messages, llm.NewUserMessage(input.Message))

	subtasks, err := w.planSubtasks(ctx, input)
	if err != nil {
		w.logger.Warn("team workflow: planner failed, falling back to single workflow", "error", err)
		input.Sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: fmt.Sprintf("[team] planner recovery failed: %v\n", err)})
		input.Sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: "[team] falling back to single workflow\n"})
		return NewSingleWorkflow().Run(ctx, input)
	}

	if len(subtasks) == 0 {
		// Degenerate case: planner returned nothing — fall back to single.
		return NewSingleWorkflow().Run(ctx, input)
	}

	w.logger.Info("team workflow: subtasks planned", "count", len(subtasks))
	input.Sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: fmt.Sprintf("[team] planned %d subtasks\n", len(subtasks))})

	results, totalUsage, err := w.runSubtasks(ctx, workflowInput, subtasks)
	if err != nil {
		return nil, fmt.Errorf("team workflow: execute: %w", err)
	}

	finalMessages, summary, synthUsage, err := w.synthesise(ctx, workflowInput, results)
	if err != nil {
		return nil, fmt.Errorf("team workflow: synthesise: %w", err)
	}

	totalUsage.InputTokens += synthUsage.InputTokens
	totalUsage.OutputTokens += synthUsage.OutputTokens
	totalUsage.CacheRead += synthUsage.CacheRead
	totalUsage.CacheWrite += synthUsage.CacheWrite

	var toolStatSlices [][]learning.AgentToolStat
	finishReasons := make([]string, 0, len(results))
	totalIter := 0
	succeeded := 0
	failed := 0
	for _, r := range results {
		if r.err != nil {
			finishReasons = append(finishReasons, string(agent.FinishReasonError))
			failed++
			continue
		}
		toolStatSlices = append(toolStatSlices, toAgentToolStats(r.result.ToolStats))
		finishReasons = append(finishReasons, string(r.result.FinishReason))
		totalIter += r.result.Iterations
		succeeded++
	}
	mergedToolStats := learning.MergeAgentToolStats(toolStatSlices...)
	finishReason := aggregateFinishReason(finishReasons)
	if succeeded > 0 && failed > 0 {
		finishReason = string(agent.FinishReasonPartialSuccess)
	}
	workflowToolStats := toWorkflowToolStats(mergedToolStats)

	if input.Learning != nil {
		learningMessages := mergeTeamLearningMessages(results, finalMessages)
		info := learning.AgentResultInfo{
			Topic:         firstMessageSnippet(input.Message, 80),
			FinishReason:  finishReason,
			Iterations:    totalIter,
			MaxIterations: input.Config.MaxIterations * len(results),
			OutputTokens:  totalUsage.OutputTokens,
			InputTokens:   totalUsage.InputTokens,
			ToolStats:     mergedToolStats,
			Workflow:      "team",
		}
		applyAgentLearning(prepareLearningDeps(input.Learning, input.Session, learningMessages, len(input.Messages), workflowToolStats), info)
	}

	return &WorkflowResult{
		Messages:     finalMessages,
		Summary:      summary,
		Usage:        totalUsage,
		ToolStats:    workflowToolStats,
		Iterations:   totalIter,
		FinishReason: finishReason,
		Workflow:     w.Name(),
	}, nil
}

// planSubtasks asks the LLM to decompose the user message into a JSON array of
// subtasks. The prompt instructs the model to return ONLY the JSON.
func (w *TeamWorkflow) planSubtasks(ctx context.Context, input WorkflowInput) ([]subtask, error) {
	planPrompt := fmt.Sprintf(`You are a task planner. Decompose the following task into 2-5 independent subtasks that can be executed in parallel.

Task: %s

Respond with ONLY a JSON array. Each element must have exactly these fields:
  "id": integer starting at 1
  "title": short title (≤10 words)
  "instruction": complete self-contained instruction for a separate agent

Planning rules:
- If the task is about changing an existing codebase, at least one subtask MUST modify code and at least one subtask MUST verify the change.
- For brownfield coding work, include explicit action-oriented subtasks such as: inspect the relevant code path, implement the bounded patch, verify with repo-native checks.
- Do not return analysis-only subtasks for every slot when the task explicitly asks for implementation.
- Benchmark tasks that request a code change require an actual working-tree diff; planning-only output is failure.
- Keep subtasks self-contained, but ensure the overall set can actually finish the task rather than only analyze it.

Example:
[
  {"id":1,"title":"Research API options","instruction":"List the top 3 REST API design patterns for this use case."},
  {"id":2,"title":"Draft data model","instruction":"Design a minimal data model for the given requirements."}
]`, input.Message)

	planMessages := []llm.Message{llm.NewUserMessage(planPrompt)}

	opts := agentOptions(input.Config)
	a := agent.New(input.Provider, input.Tools, opts...)

	result, err := a.Run(ctx, planMessages, nil)
	if err != nil {
		return nil, fmt.Errorf("planner agent: %w", err)
	}

	raw := ""
	for i := len(result.Messages) - 1; i >= 0; i-- {
		if result.Messages[i].Role == llm.RoleAssistant {
			raw = result.Messages[i].Text()
			break
		}
	}

	return parseSubtasks(raw)
}

// parseSubtasks extracts a []subtask from JSON that may be wrapped in a code fence.
func parseSubtasks(raw string) ([]subtask, error) {
	raw = strings.TrimSpace(raw)

	// Strip markdown code fences if present.
	if strings.HasPrefix(raw, "```") {
		lines := strings.SplitN(raw, "\n", 2)
		if len(lines) == 2 {
			raw = lines[1]
		}
		if idx := strings.LastIndex(raw, "```"); idx >= 0 {
			raw = raw[:idx]
		}
		raw = strings.TrimSpace(raw)
	}

	if !strings.Contains(raw, "[") {
		return nil, fmt.Errorf("no JSON array found in planner response")
	}

	var (
		lastValid []subtask
		found     bool
	)
	for _, candidate := range jsonArrayCandidates(raw) {
		var tasks []subtask
		if err := json.Unmarshal([]byte(candidate), &tasks); err != nil {
			continue
		}
		if !validSubtasks(tasks) {
			continue
		}
		lastValid = tasks
		found = true
	}
	if !found {
		return nil, fmt.Errorf("parse subtasks JSON: no valid subtask array found in planner response")
	}
	return lastValid, nil
}

func jsonArrayCandidates(raw string) []string {
	var candidates []string
	for i := 0; i < len(raw); i++ {
		if raw[i] != '[' {
			continue
		}
		candidate, ok := extractJSONArray(raw[i:])
		if !ok {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func extractJSONArray(raw string) (string, bool) {
	if raw == "" || raw[0] != '[' {
		return "", false
	}

	depth := 0
	inString := false
	escaped := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return raw[:i+1], true
			}
		}
	}

	return "", false
}

func validSubtasks(tasks []subtask) bool {
	for _, task := range tasks {
		if task.ID <= 0 || strings.TrimSpace(task.Title) == "" || strings.TrimSpace(task.Instruction) == "" {
			return false
		}
	}
	return true
}

func mergeTeamLearningMessages(results []subtaskResult, finalMessages []llm.Message) []llm.Message {
	total := len(finalMessages)
	for _, result := range results {
		if result.result != nil {
			total += len(result.result.Messages)
		}
	}
	merged := make([]llm.Message, 0, total)
	for _, result := range results {
		if result.result != nil {
			merged = append(merged, result.result.Messages...)
		}
	}
	merged = append(merged, finalMessages...)
	return merged
}

// runSubtasks launches one goroutine per subtask and collects results.
// Context cancellation propagates to all goroutines.
func (w *TeamWorkflow) runSubtasks(ctx context.Context, input WorkflowInput, subtasks []subtask) ([]subtaskResult, llm.UsageStats, error) {
	resultCh := make(chan subtaskResult, len(subtasks))

	safeInput := input
	safeInput.Sink = &syncSink{inner: input.Sink}

	// Limit concurrent LLM calls to avoid overwhelming the provider with
	// parallel requests that all hit rate limits simultaneously.
	const maxConcurrent = 2
	sem := make(chan struct{}, maxConcurrent)

	var wg sync.WaitGroup
	for _, st := range subtasks {
		wg.Add(1)
		go func(st subtask) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := w.runOne(ctx, safeInput, st)
			resultCh <- res
		}(st)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var results []subtaskResult
	totalUsage := llm.UsageStats{}
	successCount := 0
	for r := range resultCh {
		if r.err != nil {
			safeInput.Sink.Emit(event.TextDeltaEvent{
				Base:    event.NewBase(),
				Content: fmt.Sprintf("[team] subtask %d failed: %s — %v\n", r.subtask.ID, r.subtask.Title, r.err),
			})
			results = append(results, r)
			continue
		}
		safeInput.Sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: fmt.Sprintf("[team] completed subtask %d: %s\n", r.subtask.ID, r.subtask.Title)})
		if r.stream != "" {
			safeInput.Sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: r.stream})
			if !strings.HasSuffix(r.stream, "\n") {
				safeInput.Sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: "\n"})
			}
		}
		totalUsage.InputTokens += r.result.Usage.InputTokens
		totalUsage.OutputTokens += r.result.Usage.OutputTokens
		totalUsage.CacheRead += r.result.Usage.CacheRead
		totalUsage.CacheWrite += r.result.Usage.CacheWrite
		results = append(results, r)
		successCount++
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].subtask.ID < results[j].subtask.ID
	})

	// All subtasks failed — preserve original fail-fast surface so the user
	// sees the underlying error rather than a hollow synthesis. Partial
	// failures (≥1 success) flow through to synthesise as error context.
	if successCount == 0 && len(results) > 0 {
		first := results[0]
		return nil, llm.UsageStats{}, fmt.Errorf("subtask %d %q: %w", first.subtask.ID, first.subtask.Title, first.err)
	}

	return results, totalUsage, nil
}

// runOne executes a single subtask with its own Agent instance.
func (w *TeamWorkflow) runOne(ctx context.Context, input WorkflowInput, st subtask) subtaskResult {
	subtaskSystemPrompt := fmt.Sprintf(`You are a specialist agent working on subtask %d: %s

Original task context: %s

Execution rules:
- If the subtask requires implementation, you must directly use tools to inspect and modify the repository.
- If the overall task is a brownfield code-change request, analysis-only output is not sufficient.
- Prefer the smallest correct patch and verify it with repo-native commands when possible.
- If you are the verification subtask, run the verification and report concrete pass/fail evidence.`, st.ID, st.Title, input.Message)

	opts := agentOptions(WorkflowConfig{
		Model:         input.Config.Model,
		MaxIterations: input.Config.MaxIterations,
		SystemPrompt:  subtaskSystemPrompt,
		Hooks:         input.Config.Hooks,
		Permission:    input.Config.Permission,
		ToolExecutor:  input.Config.ToolExecutor,
	})
	a := agent.New(input.Provider, input.Tools, opts...)

	messages := []llm.Message{llm.NewUserMessage(st.Instruction)}
	var stream strings.Builder
	captureSink := event.OnTextToSink(func(text string) {
		stream.WriteString(text)
	})
	tee := &teeSink{a: captureSink, b: input.Sink}
	result, err := a.Run(ctx, messages, tee)
	return subtaskResult{subtask: st, result: result, stream: stream.String(), err: err}
}

// teeSink forwards each event to two sinks.
type teeSink struct {
	a, b event.Sink
}

func (t *teeSink) Emit(e event.Event) {
	t.a.Emit(e)
	t.b.Emit(e)
}

// syncSink wraps a Sink with a mutex for concurrent callers.
type syncSink struct {
	mu    sync.Mutex
	inner event.Sink
}

func (s *syncSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inner.Emit(e)
}

// synthesise asks the LLM to combine all subtask outputs into a coherent final answer.
func (w *TeamWorkflow) synthesise(ctx context.Context, input WorkflowInput, results []subtaskResult) ([]llm.Message, string, llm.UsageStats, error) {
	var sb strings.Builder
	sb.WriteString("You have been given the results of parallel subtasks. Synthesise them into a single coherent answer for the original task.\n\n")
	sb.WriteString(fmt.Sprintf("Original task: %s\n\n", input.Message))
	sb.WriteString("Subtask results:\n")

	for _, r := range results {
		sb.WriteString(fmt.Sprintf("\n--- Subtask %d: %s ---\n", r.subtask.ID, r.subtask.Title))
		if r.err != nil {
			sb.WriteString(fmt.Sprintf("(failed: %v)\n", r.err))
			continue
		}
		answer := extractSummary(r.result.Messages)
		if answer == "" {
			answer = "(no output)"
		}
		sb.WriteString(answer)
		sb.WriteString("\n")
	}

	synthMessages := append(input.Messages, llm.NewUserMessage(sb.String()))

	opts := agentOptions(input.Config)
	a := agent.New(input.Provider, input.Tools, opts...)

	result, err := a.Run(ctx, synthMessages, input.Sink)
	if err != nil {
		return nil, "", llm.UsageStats{}, fmt.Errorf("synthesiser agent: %w", err)
	}

	summary := extractSummary(result.Messages)
	return result.Messages, summary, result.Usage, nil
}
