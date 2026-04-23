package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
)

const defaultMaxAttempts = 5

type VerifyVerdict int

const (
	VerdictPass VerifyVerdict = iota
	VerdictNeedsRevision
	VerdictFail
	// VerdictInconclusive — Phase 8.1a Fix 2 (GPT G5/G6): output plausibly
	// completes the task but evidence is insufficient. Handled by dispatch:
	// if canAcceptUnverifiedInline(prompt, evidence), accept immediately as
	// unverified_inline; else retry once; else hard fail. Guard prevents
	// file-modification-required tasks from passing on inline-only output.
	VerdictInconclusive
)

// FinishReasonUnverifiedInline marks a ralph completion that passed the
// inline-artifact guard but produced no mutating tool_use. HARD PASS in
// benchmark scoring (partner Q1: A), success in learning store (partner M3).
const FinishReasonUnverifiedInline = "unverified_inline"

// RalphWorkflow runs the SingleWorkflow in a verify-and-retry loop.
// After each execution it asks the LLM whether the result is complete and
// correct. If the verifier says yes, it returns. Otherwise it appends the
// feedback to the message history and retries, up to MaxAttempts times.
type RalphWorkflow struct {
	MaxAttempts int
	logger      *slog.Logger
}

// NewRalphWorkflow returns a RalphWorkflow with the default attempt limit.
func NewRalphWorkflow() *RalphWorkflow {
	return &RalphWorkflow{
		MaxAttempts: defaultMaxAttempts,
		logger:      slog.Default(),
	}
}

// Name implements Workflow.
func (w *RalphWorkflow) Name() string { return "ralph" }

// Run implements Workflow.
// Each iteration:
//  1. Runs SingleWorkflow.Run with the current message history.
//  2. Calls verify() to ask the LLM whether the result is satisfactory.
//  3. If verified, returns the result.
//  4. If not, appends the verification feedback as a user message and retries.
func (w *RalphWorkflow) Run(ctx context.Context, input WorkflowInput) (*WorkflowResult, error) {
	if input.Sink == nil {
		input.Sink = event.NopSink{}
	}
	single := NewSingleWorkflow()
	totalUsage := llm.UsageStats{}
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}

	current := input
	current.Learning = nil

	var accToolStatSlices [][]learning.AgentToolStat
	var lastFinishReason string
	totalIter := 0
	attemptsRun := 0
	verified := false
	// Phase 8.1a Fix 2 (GPT G5/G6): INCONCLUSIVE handling gets at most one
	// retry — guard decides immediate accept vs retry vs hard-fail.
	inconclusiveRetries := 0
	var finalResult *WorkflowResult

	for a := 1; a <= maxAttempts; a++ {
		attemptsRun = a
		w.logger.Info("ralph workflow: attempt", "attempt", a, "max", maxAttempts)

		result, err := single.Run(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("ralph workflow attempt %d: %w", a, err)
		}

		totalUsage.InputTokens += result.Usage.InputTokens
		totalUsage.OutputTokens += result.Usage.OutputTokens
		totalUsage.CacheRead += result.Usage.CacheRead
		totalUsage.CacheWrite += result.Usage.CacheWrite
		accToolStatSlices = append(accToolStatSlices, toAgentToolStats(result.ToolStats))
		totalIter += result.Iterations
		lastFinishReason = result.FinishReason
		finalResult = result

		verdict, feedback, verifyUsage, err := w.verify(ctx, input, result)
		if err != nil {
			return nil, fmt.Errorf("ralph workflow verify attempt %d: %w", a, err)
		}

		totalUsage.InputTokens += verifyUsage.InputTokens
		totalUsage.OutputTokens += verifyUsage.OutputTokens
		totalUsage.CacheRead += verifyUsage.CacheRead
		totalUsage.CacheWrite += verifyUsage.CacheWrite

		switch verdict {
		case VerdictPass:
			w.logger.Info("ralph workflow: verified", "attempt", a)
			verified = true
		case VerdictFail:
			w.logger.Warn("ralph workflow: verifier rejected", "attempt", a, "feedback", feedback)
			if input.Learning != nil {
				info := learning.AgentResultInfo{
					Topic:         firstMessageSnippet(input.Message, 80),
					FinishReason:  "ralph_fail",
					Iterations:    totalIter,
					MaxIterations: input.Config.MaxIterations * a,
					OutputTokens:  totalUsage.OutputTokens,
					InputTokens:   totalUsage.InputTokens,
					ToolStats:     learning.MergeAgentToolStats(accToolStatSlices...),
					RetryCount:    a - 1,
					Workflow:      "ralph",
				}
				var resultMessages []llm.Message
				if finalResult != nil {
					resultMessages = finalResult.Messages
				}
				mergedStats := toWorkflowToolStats(learning.MergeAgentToolStats(accToolStatSlices...))
				applyAgentLearning(prepareLearningDeps(input.Learning, input.Session, resultMessages, len(input.Messages), mergedStats), info)
			}
			return nil, fmt.Errorf("ralph workflow: verifier rejected task as fundamentally incorrect: %s", feedback)
		case VerdictNeedsRevision:
			w.logger.Info("ralph workflow: needs revision, retrying", "attempt", a, "feedback", feedback)
		case VerdictInconclusive:
			// GPT G5/G6 dispatch: guard-gated immediate accept, else retry once,
			// else hard fail. Prevents file-modification-required tasks from
			// slipping through on inline-only output.
			evidence := buildVerificationEvidence(result.Messages)
			if canAcceptUnverifiedInline(input.Message, evidence) {
				w.logger.Info("ralph workflow: unverified_inline accepted", "attempt", a, "retries", inconclusiveRetries)
				verified = true
				lastFinishReason = FinishReasonUnverifiedInline
			} else if inconclusiveRetries < 1 {
				inconclusiveRetries++
				w.logger.Info("ralph workflow: inconclusive, retrying once", "attempt", a, "feedback", feedback)
				// fall through to retry path below
			} else {
				w.logger.Warn("ralph workflow: inconclusive after retry and guard blocked accept", "feedback", feedback)
				if input.Learning != nil {
					info := learning.AgentResultInfo{
						Topic:         firstMessageSnippet(input.Message, 80),
						FinishReason:  "ralph_inconclusive",
						Iterations:    totalIter,
						MaxIterations: input.Config.MaxIterations * a,
						OutputTokens:  totalUsage.OutputTokens,
						InputTokens:   totalUsage.InputTokens,
						ToolStats:     learning.MergeAgentToolStats(accToolStatSlices...),
						RetryCount:    a - 1,
						Workflow:      "ralph",
					}
					var resultMessages []llm.Message
					if finalResult != nil {
						resultMessages = finalResult.Messages
					}
					mergedStats := toWorkflowToolStats(learning.MergeAgentToolStats(accToolStatSlices...))
					applyAgentLearning(prepareLearningDeps(input.Learning, input.Session, resultMessages, len(input.Messages), mergedStats), info)
				}
				return nil, fmt.Errorf("ralph workflow: inconclusive verdict and inline-accept guard blocked: %s", feedback)
			}
		}

		if verified {
			break
		}

		feedbackMsg := buildRecoveryPrompt(input.Message, feedback)
		current = WorkflowInput{
			Message:  feedbackMsg,
			Messages: sanitizeRetryMessages(result.Messages),
			Session:  input.Session,
			Tools:    input.Tools,
			Provider: input.Provider,
			Config:   input.Config,
			Learning: nil,
		}
	}

	mergedToolStats := toWorkflowToolStats(learning.MergeAgentToolStats(accToolStatSlices...))
	if finalResult != nil {
		finalResult.Usage = totalUsage
		finalResult.ToolStats = mergedToolStats
		finalResult.Iterations = totalIter
		finalResult.FinishReason = lastFinishReason
		finalResult.Workflow = w.Name()
	}

	if input.Learning != nil {
		finishReason := lastFinishReason
		if !verified {
			finishReason = "ralph_cap_exceeded"
		}
		info := learning.AgentResultInfo{
			Topic:         firstMessageSnippet(input.Message, 80),
			FinishReason:  finishReason,
			Iterations:    totalIter,
			MaxIterations: input.Config.MaxIterations * attemptsRun,
			OutputTokens:  totalUsage.OutputTokens,
			InputTokens:   totalUsage.InputTokens,
			ToolStats:     learning.MergeAgentToolStats(accToolStatSlices...),
			RetryCount:    attemptsRun - 1,
			Workflow:      "ralph",
		}
		var resultMessages []llm.Message
		if finalResult != nil {
			resultMessages = finalResult.Messages
		}
		applyAgentLearning(prepareLearningDeps(input.Learning, input.Session, resultMessages, len(input.Messages), mergedToolStats), info)
	}

	if !verified {
		return nil, fmt.Errorf("ralph workflow: task not verified after %d attempts", maxAttempts)
	}
	return finalResult, nil
}

func (w *RalphWorkflow) verify(ctx context.Context, input WorkflowInput, result *WorkflowResult) (VerifyVerdict, string, llm.UsageStats, error) {
	evidence := buildVerificationEvidence(result.Messages)

	verifyPrompt := fmt.Sprintf(`You are an independent quality reviewer evaluating task completion.

Original task: %s

Execution evidence:
%s

Evaluate against these criteria:
1. CORRECTNESS: Does the output correctly address the task?
2. COMPLETENESS: Are all parts of the task addressed?
3. VERIFICATION: Did the agent verify its work?
   - If the task required runnable code or long-lived files, did the agent create/modify them and run checks?
   - If the task is an inline artifact (small code snippet, config YAML, test case < 50 lines), an in-message code block IS acceptable evidence.

Respond with exactly one of:
  PASS — all criteria satisfied
  NEEDS_REVISION: <specific feedback> — direction is right but needs fixes
  INCONCLUSIVE: <reason> — output plausibly completes the task but evidence
    is insufficient (e.g., no file ops but a reasonable inline answer is
    present). Use this instead of FAIL when retrying or inline-accepting
    would be more appropriate.
  FAIL: <reason> — fundamentally wrong approach, retrying won't help

Your response must start with PASS, NEEDS_REVISION, INCONCLUSIVE, or FAIL.`, input.Message, evidence)

	verifyMessages := []llm.Message{llm.NewUserMessage(verifyPrompt)}

	opts := agentOptions(WorkflowConfig{
		Model:         input.Config.Model,
		MaxIterations: 3,
		Hooks:         input.Config.Hooks,
		Permission:    input.Config.Permission,
		ToolExecutor:  input.Config.ToolExecutor,
	}, input.Session)
	a := agent.New(input.Provider, input.Tools, opts...)

	verifyResult, err := a.Run(ctx, verifyMessages, nil)
	if err != nil {
		return VerdictNeedsRevision, "", llm.UsageStats{}, fmt.Errorf("verifier agent: %w", err)
	}

	verdict := ""
	for i := len(verifyResult.Messages) - 1; i >= 0; i-- {
		if verifyResult.Messages[i].Role == llm.RoleAssistant {
			verdict = strings.TrimSpace(verifyResult.Messages[i].Text())
			break
		}
	}

	upper := strings.ToUpper(verdict)
	switch {
	case strings.HasPrefix(upper, "PASS"):
		return VerdictPass, "", verifyResult.Usage, nil
	case strings.HasPrefix(upper, "NEEDS_REVISION"):
		return VerdictNeedsRevision, extractFeedback(verdict), verifyResult.Usage, nil
	case strings.HasPrefix(upper, "INCONCLUSIVE"):
		return VerdictInconclusive, extractFeedback(verdict), verifyResult.Usage, nil
	case strings.HasPrefix(upper, "FAIL"):
		return VerdictFail, extractFeedback(verdict), verifyResult.Usage, nil
	default:
		return VerdictNeedsRevision, verdict, verifyResult.Usage, nil
	}
}

func extractFeedback(verdict string) string {
	if idx := strings.Index(verdict, ":"); idx >= 0 && idx < len(verdict)-1 {
		return strings.TrimSpace(verdict[idx+1:])
	}
	return verdict
}

func buildRecoveryPrompt(originalTask, feedback string) string {
	return fmt.Sprintf(`Your previous response was not satisfactory.

Original task:
%s

Verifier feedback:
%s

Recovery guidance:
- stay tightly scoped to the original task
- prefer the smallest correct change over broad rewrites
- use repo-native tests or verification commands when available
- end with a concise final answer that names the modified files and the verification command/result
- if missing information would materially change the outcome or is costly to reverse, ask instead of guessing

Please try again with a corrected answer.`, originalTask, feedback)
}

func sanitizeRetryMessages(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if len(msg.Content) == 0 {
			continue
		}
		if msg.Role == llm.RoleAssistant && msg.Text() == "" && len(llm.ExtractToolUseBlocks(msg)) == 0 {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func buildVerificationEvidence(messages []llm.Message) string {
	const (
		maxAssistantChars = 6000
		maxToolChars      = 2000
		maxToolResults    = 8
	)

	assistantText := "(no assistant text returned)"
	toolEvidence := make([]string, 0, maxToolResults)

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if assistantText == "(no assistant text returned)" && msg.Role == llm.RoleAssistant {
			if text := strings.TrimSpace(msg.Text()); text != "" {
				assistantText = trimForEvidence(text, maxAssistantChars)
			}
		}
		if len(toolEvidence) >= maxToolResults {
			continue
		}
		for _, block := range msg.Content {
			tr, ok := block.(llm.ToolResultBlock)
			if !ok {
				continue
			}
			content := strings.TrimSpace(tr.Content)
			if content == "" {
				continue
			}
			label := "tool_result"
			if tr.IsError {
				label += " (error)"
			}
			toolEvidence = append(toolEvidence, fmt.Sprintf("- %s %s:\n%s", label, tr.ToolUseID, trimForEvidence(content, maxToolChars)))
			if len(toolEvidence) >= maxToolResults {
				break
			}
		}
	}

	reverseStrings(toolEvidence)

	// Phase 8.1a Fix 2 + Fix 4 (GPT G7): evidence tags scoped to final
	// assistant-authored output (no tool_result contamination). Tags help
	// verifier distinguish "inline artifact produced" from "no output".
	tags := computeEvidenceTags(messages, assistantText)

	var sb strings.Builder
	sb.WriteString("Final assistant answer:\n")
	sb.WriteString(assistantText)
	if len(toolEvidence) > 0 {
		sb.WriteString("\n\nRecent tool evidence:\n")
		sb.WriteString(strings.Join(toolEvidence, "\n\n"))
	}
	sb.WriteString("\n\n--- Evidence tags ---\n")
	sb.WriteString(tags.String())
	return sb.String()
}

func trimForEvidence(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

// Phase 8.1a Fix 2 + Fix 4 — evidence tag computation + guard rail helpers.

// evidenceTags captures structured metadata derived from agent messages so
// verifier + guard can reason about inline-artifact vs file-modification
// without relying on free-text heuristics alone.
type evidenceTags struct {
	toolUsesTotal   int
	mutatingFileOps int
	commandOps      int
	codeBlocks      int
	codeLines       int
	configBlocks    int
	configLines     int
	configLang      string
}

func (t evidenceTags) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "ToolUses: total=%d\n", t.toolUsesTotal)
	fmt.Fprintf(&sb, "FileOps: mutating_file_ops=%d\n", t.mutatingFileOps)
	fmt.Fprintf(&sb, "CommandOps: bash_or_shell_ops=%d\n", t.commandOps)
	if t.codeBlocks > 0 {
		fmt.Fprintf(&sb, "InlineCode: present, blocks=%d, lines=%d\n", t.codeBlocks, t.codeLines)
	} else {
		sb.WriteString("InlineCode: absent\n")
	}
	if t.configBlocks > 0 {
		fmt.Fprintf(&sb, "InlineConfig: present, language=%s, blocks=%d, lines=%d\n", t.configLang, t.configBlocks, t.configLines)
	} else {
		sb.WriteString("InlineConfig: absent\n")
	}
	return sb.String()
}

// computeEvidenceTags tallies tool_use kinds across assistant-authored
// messages (Read/Grep/Bash etc. count as ToolUses; Write/Edit/Create are
// mutating FileOps; Bash is CommandOps) and scans the final assistant text
// for fenced code/config blocks. GPT G7: inline-artifact detection is
// limited to final assistant-authored output to avoid tool_result contamination.
func computeEvidenceTags(messages []llm.Message, finalAssistantText string) evidenceTags {
	var t evidenceTags
	for _, msg := range messages {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, block := range msg.Content {
			tu, ok := block.(llm.ToolUseBlock)
			if !ok {
				continue
			}
			t.toolUsesTotal++
			name := strings.ToLower(tu.Name)
			switch {
			case strings.Contains(name, "write"),
				strings.Contains(name, "edit"),
				strings.Contains(name, "multi_edit"),
				strings.Contains(name, "multiedit"),
				strings.Contains(name, "create"):
				t.mutatingFileOps++
			case strings.Contains(name, "bash"),
				strings.Contains(name, "shell"):
				t.commandOps++
			}
		}
	}
	t.codeBlocks, t.codeLines, t.configBlocks, t.configLines, t.configLang = scanInlineArtifacts(finalAssistantText)
	return t
}

// scanInlineArtifacts walks the final assistant text line-by-line and
// tallies fenced code/config blocks. Language hint after the opening ``` is
// used to classify a block as config (yaml/json/toml/dockerfile/ini/xml/hcl)
// vs generic code. Multi-block output is supported; the first config
// language encountered is reported.
func scanInlineArtifacts(text string) (codeBlocks, codeLines, configBlocks, configLines int, configLang string) {
	configLangs := map[string]bool{
		"yaml": true, "yml": true, "json": true, "toml": true,
		"dockerfile": true, "ini": true, "xml": true, "hcl": true,
	}
	lines := strings.Split(text, "\n")
	inBlock := false
	currentLang := ""
	currentCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inBlock {
				inBlock = true
				currentLang = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
				currentCount = 0
			} else {
				inBlock = false
				if configLangs[currentLang] {
					configBlocks++
					configLines += currentCount
					if configLang == "" {
						configLang = currentLang
					}
				} else {
					codeBlocks++
					codeLines += currentCount
				}
			}
			continue
		}
		if inBlock {
			currentCount++
		}
	}
	return
}

// canAcceptUnverifiedInline returns true when the INCONCLUSIVE verdict may
// be treated as completion via the unverified_inline path. Two gates must
// both pass:
//  1. Prompt is inline-eligible (inlineFriendly phrases + no file-modification-
//     required phrases).
//  2. Evidence shows inline artifact present AND mutating_file_ops == 0.
//
// Guard blocks file-modification-required tasks from slipping through with
// only an inline explanation. Phase 8.1a Fix 2 GPT G5.
func canAcceptUnverifiedInline(prompt, evidence string) bool {
	if !isInlineEligibleTask(prompt) {
		return false
	}
	hasInlineArtifact := strings.Contains(evidence, "InlineCode: present") ||
		strings.Contains(evidence, "InlineConfig: present")
	noFileOps := strings.Contains(evidence, "FileOps: mutating_file_ops=0")
	return hasInlineArtifact && noFileOps
}

// isInlineEligibleTask decides whether a prompt can legitimately complete
// with an inline answer. File-modification-required phrases (explicit paths,
// "fix the existing", "update cmd/...") block eligibility. Inline-friendly
// phrases ("write a unit test", "author ci.yml", etc.) grant it. Default
// is false — unverified_inline requires positive signal, not absence of a
// block phrase. Phase 8.1a Fix 2 GPT G5 guard rail.
// Phase 8.2 Fix 5: phrase matching runs against NormalizeForPhraseMatch so
// backticked paths ("Author `.github/workflows/ci.yml`") still register as
// inline-friendly — the guard must stay in sync with the router classifier.
func isInlineEligibleTask(prompt string) bool {
	normalized := NormalizeForPhraseMatch(prompt)
	fileModRequired := []string{
		"modify cmd/", "modify internal/", "modify src/", "modify pkg/",
		"update cmd/", "update internal/", "update src/", "update pkg/",
		"edit cmd/", "edit internal/", "edit src/", "edit pkg/",
		"fix the existing", "add to cmd/", "add to internal/",
		"change the existing", "refactor the existing",
		"apply a patch to", "patch cmd/", "patch internal/",
	}
	for _, phrase := range fileModRequired {
		if strings.Contains(normalized, phrase) {
			return false
		}
	}
	inlineFriendly := []string{
		"write a test", "write a unit test", "write tests",
		"write unit tests", "write a reusable",
		"include a test", "include a unit test", "include unit tests",
		"add a test", "add tests", "add a unit test",
		"author ci.yml", "author .github", "author a",
		"draft a yaml", "draft .github", "draft a dockerfile",
		"new dockerfile", "new workflow", "set up workflow",
		"provide a snippet", "show me how",
	}
	for _, phrase := range inlineFriendly {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}
