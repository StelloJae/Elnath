package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	randv2 "math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/userfacingerr"
)

const (
	defaultMaxIterations = 50
	defaultMaxTokens     = 8192

	retryMaxAttempts = 3
	retryBaseDelay   = time.Second
	retryMaxDelay    = 30 * time.Second
)

var (
	jitterRandMu sync.Mutex
	jitterRand   = randv2.New(randv2.NewPCG(uint64(time.Now().UnixNano()), 0))
)

// Agent runs the message→LLM→tools→repeat loop.
// The message array is the ONLY state — no hidden state machines.
type Agent struct {
	provider      llm.Provider
	tools         *tools.Registry
	executor      tools.Executor
	readTracker   *tools.ReadTracker
	permission    *Permission
	hooks         *HookRegistry
	model         string
	systemPrompt  string
	maxIterations int
	logger        *slog.Logger
}

// Option configures an Agent.
type Option func(*Agent)

// WithModel overrides the model string sent in each request.
func WithModel(model string) Option {
	return func(a *Agent) { a.model = model }
}

// WithSystemPrompt sets the system prompt for every request.
func WithSystemPrompt(prompt string) Option {
	return func(a *Agent) { a.systemPrompt = prompt }
}

// WithMaxIterations overrides the default iteration cap.
func WithMaxIterations(n int) Option {
	return func(a *Agent) { a.maxIterations = n }
}

// WithLogger sets a custom structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(a *Agent) { a.logger = l }
}

// WithPermission attaches a permission engine.
func WithPermission(p *Permission) Option {
	return func(a *Agent) { a.permission = p }
}

// WithHooks attaches a hook registry for tool execution lifecycle events.
func WithHooks(h *HookRegistry) Option {
	return func(a *Agent) { a.hooks = h }
}

func WithReadTracker(tracker *tools.ReadTracker) Option {
	return func(a *Agent) { a.readTracker = tracker }
}

func WithToolExecutor(exec tools.Executor) Option {
	return func(a *Agent) {
		a.executor = exec
	}
}

// New creates an Agent with the given provider and tool registry.
func New(provider llm.Provider, reg *tools.Registry, opts ...Option) *Agent {
	a := &Agent{
		provider:      provider,
		tools:         reg,
		maxIterations: defaultMaxIterations,
		logger:        slog.Default(),
	}
	for _, o := range opts {
		o(a)
	}
	if a.permission == nil {
		a.permission = NewPermission()
	}
	if a.executor == nil {
		a.executor = reg
	}
	if a.readTracker == nil && reg != nil {
		a.readTracker = reg.ReadTracker()
	}
	return a
}

func (a *Agent) ResetReadTrackerDedup() {
	if a.readTracker != nil {
		a.readTracker.ResetDedup()
	}
}

// RunResult is returned after the agent loop completes.
type RunResult struct {
	Messages     []llm.Message
	Usage        llm.UsageStats
	ToolStats    []ToolStat
	Iterations   int
	FinishReason FinishReason
}

// ToolStat aggregates execution outcomes for a single tool across one Run.
type ToolStat struct {
	Name      string
	Calls     int
	Errors    int
	TotalTime time.Duration
}

type FinishReason string

const (
	FinishReasonStop           FinishReason = "stop"
	FinishReasonBudgetExceeded FinishReason = "budget_exceeded"
	FinishReasonAckLoop        FinishReason = "ack_loop"
	FinishReasonError          FinishReason = "error"
)

type toolStatAcc struct {
	calls  int
	errors int
	total  time.Duration
}

// Run executes the agent loop starting with the provided messages.
// It streams output via the onText callback and returns when the model
// stops requesting tool calls or maxIterations is reached.
func (a *Agent) Run(ctx context.Context, messages []llm.Message, onText func(string)) (*RunResult, error) {
	// Build tool definitions from the registry.
	toolDefs := buildToolDefs(a.tools)

	totalUsage := llm.UsageStats{}
	ackRetries := 0
	iterations := 0
	finishReason := FinishReasonBudgetExceeded
	toolStats := map[string]*toolStatAcc{}
	var toolStatsMu sync.Mutex

	for iter := 0; iter < a.maxIterations; iter++ {
		if a.hooks != nil {
			if err := a.hooks.RunOnIterationStart(ctx, iter+1, a.maxIterations); err != nil {
				return nil, fmt.Errorf("iteration hook: %w", err)
			}
		}
		iterations++

		// Budget pressure injection.
		if pct := float64(iter) / float64(a.maxIterations); pct >= 0.9 {
			messages = append(messages, llm.NewUserMessage(fmt.Sprintf(
				"[BUDGET WARNING: Only %d iterations remaining. Provide your final response NOW. Do not start new explorations.]",
				a.maxIterations-iter)))
		} else if pct >= 0.7 {
			messages = append(messages, llm.NewUserMessage(fmt.Sprintf(
				"[BUDGET: Iteration %d/%d. %d remaining. Start consolidating your work.]",
				iter, a.maxIterations, a.maxIterations-iter)))
		}

		req := llm.Request{
			Model:       a.model,
			Messages:    messages,
			Tools:       toolDefs,
			System:      a.systemPrompt,
			MaxTokens:   defaultMaxTokens,
			EnableCache: a.provider.Name() == "anthropic",
		}
		if a.hooks != nil {
			if err := a.hooks.RunPreLLMCall(ctx, &req); err != nil {
				return nil, fmt.Errorf("pre-llm hook: %w", err)
			}
		}

		assistantMsg, finalReq, usage, err := a.streamWithRetry(ctx, req, onText)
		if err != nil {
			return nil, err
		}
		if a.hooks != nil {
			if err := a.hooks.RunPostLLMCall(ctx, finalReq, responseFromAssistantMessage(assistantMsg), usage); err != nil {
				return nil, fmt.Errorf("post-llm hook: %w", err)
			}
		}

		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		totalUsage.CacheRead += usage.CacheRead
		totalUsage.CacheWrite += usage.CacheWrite

		messages = append(messages, assistantMsg)

		// Collect tool calls from the assistant message.
		toolCalls := llm.ExtractToolUseBlocks(assistantMsg)
		if len(toolCalls) == 0 {
			// Ack-continuation: force execution when model only states intent.
			if isAckOnly(assistantMsg.Text()) {
				if ackRetries < 2 {
					ackRetries++
					messages = append(messages, llm.NewUserMessage(
						"[System: Continue now. Execute the required tool calls. Do not describe what you plan to do — do it.]"))
					continue
				}
				finishReason = FinishReasonAckLoop
				break
			}
			finishReason = FinishReasonStop
			break
		}
		ackRetries = 0

		// Execute each tool call and collect results.
		messages, err = a.executeToolsWithStats(ctx, messages, toolCalls, onText, toolStats, &toolStatsMu)
		if err != nil {
			return nil, err
		}

		// Truncate oversized tool results to keep context manageable.
		truncateToolResults(messages)
		attenuateHistoricalToolResults(messages, countToolResultTurns(messages)-1)
	}

	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if last.Role == llm.RoleAssistant && len(llm.ExtractToolUseBlocks(last)) > 0 {
			return nil, fmt.Errorf("%w: model kept requesting tools after %d iterations",
				core.ErrMaxIterations, a.maxIterations)
		}
	}

	// Run on-stop hooks.
	if a.hooks != nil {
		if err := a.hooks.RunOnStop(ctx); err != nil {
			a.logger.Warn("on-stop hook error", "error", err)
		}
	}

	return &RunResult{
		Messages:     messages,
		Usage:        totalUsage,
		ToolStats:    finalizeToolStats(toolStats),
		Iterations:   iterations,
		FinishReason: finishReason,
	}, nil
}

// streamWithRetry calls the provider with exponential backoff on 429/5xx errors.
func (a *Agent) streamWithRetry(ctx context.Context, req llm.Request, onText func(string)) (llm.Message, llm.Request, llm.UsageStats, error) {
	var lastErr error
	delay := retryBaseDelay
	currentReq := req

	for attempt := 0; attempt < retryMaxAttempts; attempt++ {
		reqForAttempt := currentReq
		if attempt > 0 {
			delay = nextJitterDelay(delay)
			a.logger.Warn("retrying after provider error",
				"attempt", attempt,
				"delay", delay,
				"error", lastErr,
			)
			select {
			case <-ctx.Done():
				return llm.Message{}, llm.Request{}, llm.UsageStats{}, ctx.Err()
			case <-time.After(delay):
			}
		}

		msg, usage, err := a.stream(ctx, reqForAttempt, onText)
		if err == nil {
			if isEmptyAssistantMessage(msg) {
				fallbackMsg, fallbackUsage, fbErr := a.chatFallback(ctx, reqForAttempt, onText)
				if fbErr == nil && !isEmptyAssistantMessage(fallbackMsg) {
					return fallbackMsg, reqForAttempt, fallbackUsage, nil
				}
				if fbErr == nil {
					fbErr = fmt.Errorf("empty assistant response")
				}
				currentReq.Messages = append(reqForAttempt.Messages, llm.NewUserMessage(
					"The previous attempt returned an empty response. You must either provide a concrete answer or call tools. Do not return empty content.",
				))
				lastErr = userfacingerr.Wrap(userfacingerr.ELN120, fbErr, "empty llm response")
				continue
			}
			return msg, reqForAttempt, usage, nil
		}

		if isRetryable(err) {
			lastErr = err
			continue
		}
		return llm.Message{}, llm.Request{}, llm.UsageStats{}, err
	}

	return llm.Message{}, llm.Request{}, llm.UsageStats{}, fmt.Errorf("%w: %w", core.ErrProviderError, lastErr)
}

func (a *Agent) chatFallback(ctx context.Context, req llm.Request, onText func(string)) (llm.Message, llm.UsageStats, error) {
	resp, err := a.provider.Chat(ctx, llm.ChatRequest(req))
	if err != nil {
		return llm.Message{}, llm.UsageStats{}, fmt.Errorf("chat fallback: %w", err)
	}

	if onText != nil && resp.Content != "" {
		onText(resp.Content)
	}

	toolCalls := make([]llm.CompletedToolCall, 0, len(resp.ToolCalls))
	for _, tc := range resp.ToolCalls {
		toolCalls = append(toolCalls, llm.CompletedToolCall{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: tc.Input,
		})
	}

	msg := llm.BuildAssistantMessage([]string{resp.Content}, toolCalls)
	return msg, llm.UsageStats{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CacheRead:    resp.Usage.CacheRead,
		CacheWrite:   resp.Usage.CacheWrite,
	}, nil
}

func isEmptyAssistantMessage(msg llm.Message) bool {
	return len(msg.Content) == 0 || (msg.Text() == "" && len(llm.ExtractToolUseBlocks(msg)) == 0)
}

func responseFromAssistantMessage(msg llm.Message) llm.ChatResponse {
	toolUses := llm.ExtractToolUseBlocks(msg)
	toolCalls := make([]llm.ToolCall, 0, len(toolUses))
	for _, tc := range toolUses {
		toolCalls = append(toolCalls, llm.ToolCall{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: string(tc.Input),
		})
	}
	return llm.ChatResponse{
		Content:   msg.Text(),
		ToolCalls: toolCalls,
	}
}

// stream calls the provider once and accumulates the response into a Message.
func (a *Agent) stream(ctx context.Context, req llm.Request, onText func(string)) (llm.Message, llm.UsageStats, error) {
	var (
		textParts []string
		toolCalls []llm.CompletedToolCall
		usage     llm.UsageStats

		// in-progress tool call accumulation keyed by ID
		pendingTools = map[string]*llm.CompletedToolCall{}
	)

	err := a.provider.Stream(ctx, req, func(ev llm.StreamEvent) {
		switch ev.Type {
		case llm.EventTextDelta:
			if ev.Content != "" {
				textParts = append(textParts, ev.Content)
				if onText != nil {
					onText(ev.Content)
				}
			}

		case llm.EventToolUseStart:
			if ev.ToolCall != nil {
				pendingTools[ev.ToolCall.ID] = &llm.CompletedToolCall{
					ID:   ev.ToolCall.ID,
					Name: ev.ToolCall.Name,
				}
			}

		case llm.EventToolUseDelta:
			if ev.ToolCall != nil {
				if tc, ok := pendingTools[ev.ToolCall.ID]; ok {
					tc.Input += ev.ToolCall.Input
				}
			}

		case llm.EventToolUseDone:
			if ev.ToolCall != nil {
				if tc, ok := pendingTools[ev.ToolCall.ID]; ok {
					// Use the fully accumulated input from EventToolUseDone
					// if it carries the complete JSON (Anthropic sends the
					// final accumulated string here).
					if ev.ToolCall.Input != "" {
						tc.Input = ev.ToolCall.Input
					}
					toolCalls = append(toolCalls, *tc)
					delete(pendingTools, ev.ToolCall.ID)
				}
			}

		case llm.EventDone:
			if ev.Usage != nil {
				usage = *ev.Usage
			}
		}
	})

	if err != nil {
		return llm.Message{}, llm.UsageStats{}, err
	}

	msg := llm.BuildAssistantMessage(textParts, toolCalls)
	return msg, usage, nil
}

// toolExecResult holds the result of a single tool execution for ordered collection.
type toolExecResult struct {
	id       string
	name     string
	output   string
	isError  bool
	duration time.Duration
}

func mergeToolStat(m map[string]*toolStatAcc, mu *sync.Mutex, name string, dur time.Duration, hadErr bool) {
	mu.Lock()
	defer mu.Unlock()
	acc := m[name]
	if acc == nil {
		acc = &toolStatAcc{}
		m[name] = acc
	}
	acc.calls++
	if hadErr {
		acc.errors++
	}
	acc.total += dur
}

func finalizeToolStats(m map[string]*toolStatAcc) []ToolStat {
	out := make([]ToolStat, 0, len(m))
	for name, acc := range m {
		out = append(out, ToolStat{
			Name:      name,
			Calls:     acc.calls,
			Errors:    acc.errors,
			TotalTime: acc.total,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *Agent) executeTools(ctx context.Context, messages []llm.Message, calls []llm.ToolUseBlock, onText func(string)) ([]llm.Message, error) {
	return a.executeToolsWithStats(ctx, messages, calls, onText, nil, nil)
}

func (a *Agent) executeToolsWithStats(ctx context.Context, messages []llm.Message, calls []llm.ToolUseBlock, onText func(string), toolStats map[string]*toolStatAcc, toolStatsMu *sync.Mutex) ([]llm.Message, error) {
	results, approved, err := a.collectApprovedToolCalls(ctx, calls, onText)
	if err != nil {
		return nil, err
	}
	if err := a.executeApprovedToolCalls(ctx, approved, results, toolStats, toolStatsMu); err != nil {
		return nil, err
	}
	return a.appendToolResults(messages, results), nil
}

func (a *Agent) collectApprovedToolCalls(ctx context.Context, calls []llm.ToolUseBlock, onText func(string)) ([]toolExecResult, []approvedToolCall, error) {
	results := make([]toolExecResult, len(calls))
	approved := make([]approvedToolCall, 0, len(calls))

	for i, call := range calls {
		a.logger.Debug("checking tool", "name", call.Name, "id", call.ID)
		if onText != nil {
			preview := extractToolPreview(call.Name, string(call.Input))
			ev := daemon.ToolProgressEvent(call.Name, preview)
			onText(daemon.EncodeProgressEvent(ev))
		}

		allowed, err := a.permission.Check(ctx, call.Name, call.Input)
		if err != nil {
			return nil, nil, fmt.Errorf("permission check: %w", err)
		}
		if !allowed {
			results[i] = toolExecResult{id: call.ID, output: fmt.Sprintf("permission denied for tool %q", call.Name), isError: true}
			continue
		}

		if a.hooks != nil {
			hookResult, hookErr := a.hooks.RunPreToolUse(ctx, call.Name, call.Input)
			if hookErr != nil {
				return nil, nil, fmt.Errorf("pre-tool hook: %w", hookErr)
			}
			if hookResult.Action == HookDeny {
				results[i] = toolExecResult{id: call.ID, output: fmt.Sprintf("hook denied tool %q: %s", call.Name, hookResult.Message), isError: true}
				continue
			}
		}

		approved = append(approved, approvedToolCall{call: call, index: i})
	}

	return results, approved, nil
}

func (a *Agent) appendToolResults(messages []llm.Message, results []toolExecResult) []llm.Message {
	for _, r := range results {
		if r.id == "" {
			continue
		}
		a.logger.Debug("tool result", "id", r.id, "is_error", r.isError)
		messages = llm.AppendToolResult(messages, r.id, r.output, r.isError)
	}
	return messages
}

// buildToolDefs converts the tools.Registry into []llm.ToolDef.
func buildToolDefs(reg *tools.Registry) []llm.ToolDef {
	all := reg.List()
	defs := make([]llm.ToolDef, 0, len(all))
	for _, t := range all {
		defs = append(defs, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		})
	}
	return defs
}

// isRetryable returns true for errors that warrant an automatic retry.
// We treat provider errors conservatively: retry only when the error
// message suggests a rate-limit (429) or transient server error (5xx).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{"429", "500", "502", "503", "504", "rate limit", "rate_limit"} {
		for i := 0; i <= len(msg)-len(marker); i++ {
			if msg[i:i+len(marker)] == marker {
				return true
			}
		}
	}
	return false
}

func extractToolPreview(toolName, input string) string {
	input = strings.TrimSpace(input)
	if input == "" || input == "{}" {
		return ""
	}
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		return ""
	}
	var preview string
	switch toolName {
	case "bash":
		if v, ok := fields["command"].(string); ok {
			preview = v
		}
	case "read_file", "write_file", "edit_file":
		if v, ok := fields["file_path"].(string); ok {
			preview = v
		}
	case "glob", "grep":
		if v, ok := fields["pattern"].(string); ok {
			preview = v
		}
	case "web_fetch":
		if v, ok := fields["url"].(string); ok {
			preview = v
		}
	case "wiki_search", "conversation_search":
		if v, ok := fields["query"].(string); ok {
			preview = v
		}
	case "wiki_read", "wiki_write":
		if v, ok := fields["path"].(string); ok {
			preview = v
		}
	case "git":
		if v, ok := fields["subcommand"].(string); ok {
			preview = v
		}
	default:
		for _, key := range []string{"file_path", "path", "command", "query", "pattern", "url", "name", "subcommand"} {
			if v, ok := fields[key].(string); ok && v != "" {
				preview = v
				break
			}
		}
	}
	if len([]rune(preview)) > 40 {
		preview = string([]rune(preview)[:37]) + "..."
	}
	return preview
}

// isAckOnly returns true when the model only states intent without executing.
func isAckOnly(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) > 500 || text == "" {
		return false
	}
	for _, p := range []string{
		"I'll ", "I will ", "Let me ", "I'm going to ",
		"I need to ", "First, I'll ", "I should ",
	} {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

const (
	toolResultPerToolLimit = 50_000
	toolResultTotalLimit   = 200_000

	toolResultHistoryStage1Limit = 10_000
	toolResultHistoryStage2Limit = 2_000
	attenuationMarker            = "[attenuated/"
)

// truncateToolResults caps oversized tool results in the message slice.
// It operates on the last user-role message (which carries tool results)
// and replaces any ToolResultBlock content that exceeds the per-tool limit.
// When the aggregate exceeds the total limit, the largest results are
// truncated first.
func truncateToolResults(messages []llm.Message) {
	if len(messages) == 0 {
		return
	}
	last := &messages[len(messages)-1]
	if last.Role != llm.RoleUser {
		return
	}

	type trRef struct {
		idx  int
		size int
	}
	var refs []trRef
	total := 0

	for i, block := range last.Content {
		tr, ok := block.(llm.ToolResultBlock)
		if !ok {
			continue
		}
		size := len(tr.Content)
		refs = append(refs, trRef{idx: i, size: size})
		total += size

		if size > toolResultPerToolLimit {
			tr.Content = tr.Content[:2000] + fmt.Sprintf(
				"\n\n[Output truncated. %d total characters. Read specific sections with offset/limit for details.]", size)
			last.Content[i] = tr
			total = total - size + len(tr.Content)
			refs[len(refs)-1].size = len(tr.Content)
		}
	}

	for total > toolResultTotalLimit && len(refs) > 0 {
		largest := 0
		for j, r := range refs {
			if r.size > refs[largest].size {
				largest = j
			}
		}
		r := refs[largest]
		tr := last.Content[r.idx].(llm.ToolResultBlock)
		oldSize := len(tr.Content)
		if oldSize <= 2000 {
			break
		}
		tr.Content = tr.Content[:2000] + fmt.Sprintf(
			"\n\n[Output truncated. %d total characters. Read specific sections with offset/limit for details.]", oldSize)
		last.Content[r.idx] = tr
		total = total - oldSize + len(tr.Content)
		refs[largest].size = len(tr.Content)
	}
}

func countToolResultTurns(messages []llm.Message) int {
	count := 0
	for _, msg := range messages {
		if hasToolResultBlocks(msg) {
			count++
		}
	}
	return count
}

func attenuateHistoricalToolResults(messages []llm.Message, currentTurnIdx int) {
	if currentTurnIdx < 0 {
		return
	}

	turnIdx := -1
	for i := range messages {
		if !hasToolResultBlocks(messages[i]) {
			continue
		}
		turnIdx++

		turnsAgo := currentTurnIdx - turnIdx
		switch {
		case turnsAgo <= 1:
			continue
		case turnsAgo == 2:
			attenuateToolResultMessage(&messages[i], turnsAgo, toolResultHistoryStage1Limit)
		case turnsAgo == 3:
			attenuateToolResultMessage(&messages[i], turnsAgo, toolResultHistoryStage2Limit)
		default:
			replaceWithStaleToolResultPlaceholder(&messages[i], turnsAgo)
		}
	}
}

func hasToolResultBlocks(msg llm.Message) bool {
	if msg.Role != llm.RoleUser {
		return false
	}
	for _, block := range msg.Content {
		if _, ok := block.(llm.ToolResultBlock); ok {
			return true
		}
	}
	return false
}

type attenuatedToolResult struct {
	preview      string
	originalSize int
	turnsAgo     int
	stale        bool
}

func attenuateToolResultMessage(msg *llm.Message, turnsAgo, limit int) {
	for i, block := range msg.Content {
		tr, ok := block.(llm.ToolResultBlock)
		if !ok {
			continue
		}

		source := tr.Content
		originalSize := len(tr.Content)
		if prior, ok := parseAttenuatedToolResult(tr.Content); ok {
			if prior.turnsAgo >= turnsAgo {
				continue
			}
			source = prior.preview
			originalSize = prior.originalSize
		}

		if len(source) <= limit {
			continue
		}

		tr.Content = buildAttenuatedToolResult(source, turnsAgo, limit, originalSize)
		msg.Content[i] = tr
	}
}

func replaceWithStaleToolResultPlaceholder(msg *llm.Message, turnsAgo int) {
	for i, block := range msg.Content {
		tr, ok := block.(llm.ToolResultBlock)
		if !ok {
			continue
		}

		originalSize := len(tr.Content)
		if prior, ok := parseAttenuatedToolResult(tr.Content); ok {
			if prior.stale && prior.turnsAgo >= turnsAgo {
				continue
			}
			originalSize = prior.originalSize
		}

		tr.Content = buildStaleToolResultPlaceholder(turnsAgo, originalSize)
		msg.Content[i] = tr
	}
}

func buildAttenuatedToolResult(content string, turnsAgo, limit, originalSize int) string {
	header := fmt.Sprintf("%sturns=%d original=%d]", attenuationMarker, turnsAgo, originalSize)
	if len(header) >= limit {
		return header[:limit]
	}

	previewLimit := limit - len(header)
	if previewLimit > 2 {
		header += "\n\n"
		previewLimit -= 2
	}
	if previewLimit > len(content) {
		previewLimit = len(content)
	}
	return header + content[:previewLimit]
}

func buildStaleToolResultPlaceholder(turnsAgo, originalSize int) string {
	return fmt.Sprintf("[stale tool result, turns=%d, original=%d]", turnsAgo, originalSize)
}

func parseAttenuatedToolResult(content string) (attenuatedToolResult, bool) {
	if strings.HasPrefix(content, "[stale tool result, turns=") {
		var parsed attenuatedToolResult
		if _, err := fmt.Sscanf(content, "[stale tool result, turns=%d, original=%d]", &parsed.turnsAgo, &parsed.originalSize); err != nil {
			return attenuatedToolResult{}, false
		}
		parsed.stale = true
		return parsed, true
	}

	if !strings.HasPrefix(content, attenuationMarker) {
		return attenuatedToolResult{}, false
	}

	if strings.HasPrefix(content, attenuationMarker+"stale tool result, turns=") {
		var parsed attenuatedToolResult
		if _, err := fmt.Sscanf(content, attenuationMarker+"stale tool result, turns=%d, original=%d]", &parsed.turnsAgo, &parsed.originalSize); err != nil {
			return attenuatedToolResult{}, false
		}
		parsed.stale = true
		return parsed, true
	}

	headerEnd := strings.Index(content, "]")
	if headerEnd == -1 {
		return attenuatedToolResult{}, false
	}

	var parsed attenuatedToolResult
	header := content[:headerEnd+1]
	if _, err := fmt.Sscanf(header, attenuationMarker+"turns=%d original=%d]", &parsed.turnsAgo, &parsed.originalSize); err != nil {
		return attenuatedToolResult{}, false
	}
	parsed.preview = strings.TrimPrefix(content[headerEnd+1:], "\n\n")
	return parsed, true
}

func nextJitterDelay(current time.Duration) time.Duration {
	jitterRandMu.Lock()
	defer jitterRandMu.Unlock()
	return nextJitterDelayWithRand(current, jitterRand)
}

func nextJitterDelayWithRand(current time.Duration, rng *randv2.Rand) time.Duration {
	if current < retryBaseDelay {
		current = retryBaseDelay
	}

	upper := retryMaxDelay
	if current <= retryMaxDelay/3 {
		upper = current * 3
	}
	if upper < retryBaseDelay {
		upper = retryBaseDelay
	}
	if upper == retryBaseDelay {
		return retryBaseDelay
	}

	span := upper - retryBaseDelay
	return retryBaseDelay + time.Duration(rng.Int64N(int64(span)+1))
}
