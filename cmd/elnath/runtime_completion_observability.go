package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/orchestrator"
)

type completionContractSummary struct {
	VerificationHint        bool
	VerificationObserved    *bool
	VerificationCommand     string
	CompletionWarning       string
	EditIntent              bool
	EditObserved            *bool
	ReasoningEffort         string
	ReasoningEffortMode     string
	ReasoningEffortReason   string
	ProviderName            string
	ProviderEffort          string
	ProviderEffortNote      string
	LoadedDeferredTools     []string
	ConditionalSkillMatches []completionConditionalSkillMatch
	SkillCatalogReceipts    []completionSkillCatalogReceipt
	CommandCatalogReceipts  []completionCommandCatalogReceipt
	CorrectionAttempted     bool
	CorrectionAttempts      int
	CorrectionMaxAttempts   int
	CorrectionDecision      string
	CorrectionReason        string
	CorrectionStatus        string
	CorrectionFailureFamily string
	RetryDecision           string
	RetryReason             string
}

type completionConditionalSkillMatch struct {
	SkillName  string `json:"skill_name"`
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Source     string `json:"source,omitempty"`
	TrustLevel string `json:"trust_level,omitempty"`
	External   bool   `json:"external"`
}

type completionSkillCatalogReceipt struct {
	Tool               string   `json:"tool"`
	Action             string   `json:"action"`
	ReadOnly           bool     `json:"read_only"`
	RegistryAvailable  bool     `json:"registry_available"`
	TotalSkills        int      `json:"total_skills"`
	ReturnedSkills     int      `json:"returned_skills,omitempty"`
	ReturnedMatches    int      `json:"returned_matches,omitempty"`
	TrustFilterApplied bool     `json:"trust_filter_applied"`
	AllowTrustLevels   []string `json:"allow_trust_levels,omitempty"`
	MaxResults         int      `json:"max_results,omitempty"`
	Query              string   `json:"query,omitempty"`
	Skill              string   `json:"skill,omitempty"`
	PathCount          int      `json:"path_count,omitempty"`
	CWDSet             bool     `json:"cwd_set,omitempty"`
	IncludePrompt      bool     `json:"include_prompt,omitempty"`
}

type completionCommandCatalogReceipt struct {
	Tool               string `json:"tool"`
	Action             string `json:"action"`
	ReadOnly           bool   `json:"read_only"`
	RegistryAvailable  bool   `json:"registry_available"`
	ExecutionAvailable bool   `json:"execution_available"`
	ExecutionPolicy    string `json:"execution_policy"`
	TotalCommands      int    `json:"total_commands"`
	ReturnedCommands   int    `json:"returned_commands"`
	IncludeHidden      bool   `json:"include_hidden"`
	MaxResults         int    `json:"max_results,omitempty"`
	Query              string `json:"query,omitempty"`
	Command            string `json:"command,omitempty"`
}

const (
	completionRetryDecisionRunVerification   = "run_verification"
	completionRetryDecisionRetrySmallerScope = "retry_smaller_scope"
)

var verificationCommandRE = regexp.MustCompile(`(?i)(^|[;&|()\s])((go\s+test|go\s+vet|git\s+diff\s+--check|bash\s+-n|make\s+(test|lint|vet)|npm\s+(test|run\s+test|run\s+lint)|pnpm\s+(test|run\s+test|run\s+lint)|yarn\s+(test|run\s+test|run\s+lint)|bun\s+test|pytest|python\d*(\.\d+)?\s+-m\s+pytest|ruff\s+check|cargo\s+test|mvn\s+test|gradle\s+test))([;&|()\s]|$)`)

var mutatingBashCommandRE = regexp.MustCompile(`(?i)(^|[;&|()\s])((apply_patch|gofmt\s+-w|sed\s+-i|perl\s+-pi|tee\s+|touch\s+|mkdir\s+|rm\s+|mv\s+|cp\s+|cat\s+>|python\d*(\.\d+)?\s+(-c|-)\b))`)

func summarizeCompletionContract(routeCtx *orchestrator.RoutingContext, cfg orchestrator.WorkflowConfig, result *orchestrator.WorkflowResult) completionContractSummary {
	summary := completionContractSummary{
		ReasoningEffort:     strings.TrimSpace(cfg.ReasoningEffort),
		ReasoningEffortMode: strings.TrimSpace(cfg.ReasoningEffortMode),
	}
	if routeCtx != nil {
		summary.VerificationHint = routeCtx.VerificationHint
	}
	if result == nil {
		return summary
	}
	if effort := strings.TrimSpace(result.ReasoningEffort); effort != "" {
		summary.ReasoningEffort = effort
	}
	if mode := strings.TrimSpace(result.ReasoningEffortMode); mode != "" {
		summary.ReasoningEffortMode = mode
	}
	summary.ReasoningEffortReason = strings.TrimSpace(result.ReasoningEffortReason)
	summary.LoadedDeferredTools = append([]string(nil), result.LoadedDeferredTools...)
	summary.ConditionalSkillMatches = observedConditionalSkillMatches(result.Messages)
	summary.SkillCatalogReceipts = observedSkillCatalogReceipts(result.Messages)
	summary.CommandCatalogReceipts = observedCommandCatalogReceipts(result.Messages)

	verificationCommand, verificationFailed := observedVerificationCommandStatus(result.Messages)
	observed := verificationCommand != ""
	if summary.VerificationHint || observed {
		summary.VerificationObserved = &observed
	}
	summary.VerificationCommand = verificationCommand
	editIntent := editIntentDetected(result.Messages)
	editObserved := mutationObservedInMessages(result.Messages)
	summary.EditIntent = editIntent
	if editIntent || editObserved {
		summary.EditObserved = &editObserved
	}
	if finalAssistantReportsIncomplete(result.Messages) {
		summary.CompletionWarning = "final_response_reports_incomplete"
	}
	if summary.CompletionWarning == "" && verificationFailed {
		summary.CompletionWarning = "verification_command_failed"
	}
	if summary.CompletionWarning == "" && verificationCommand == "" && finalAssistantClaimsVerificationSuccess(result.Messages) {
		summary.CompletionWarning = "unsupported_verification_success_claim"
	}
	if summary.CompletionWarning == "" && editIntent && !editObserved {
		summary.CompletionWarning = "edit_intent_without_mutation"
	}
	summary.RetryDecision, summary.RetryReason = completionRetryPlan(summary)
	return summary
}

func observedSkillCatalogReceipts(messages []llm.Message) []completionSkillCatalogReceipt {
	toolNamesByID := make(map[string]string)
	var receipts []completionSkillCatalogReceipt
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.ToolUseBlock:
				if b.ID != "" {
					toolNamesByID[b.ID] = b.Name
				}
			case llm.ToolResultBlock:
				if b.IsError || toolNamesByID[b.ToolUseID] != "skill_catalog" {
					continue
				}
				receipt, ok := skillCatalogReceiptFromOutput(b.Content)
				if ok {
					receipts = append(receipts, receipt)
				}
			}
		}
	}
	if len(receipts) == 0 {
		return nil
	}
	return receipts
}

func skillCatalogReceiptFromOutput(output string) (completionSkillCatalogReceipt, bool) {
	var parsed struct {
		Receipt completionSkillCatalogReceipt `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return completionSkillCatalogReceipt{}, false
	}
	parsed.Receipt.Tool = strings.TrimSpace(parsed.Receipt.Tool)
	parsed.Receipt.Action = strings.TrimSpace(parsed.Receipt.Action)
	parsed.Receipt.Query = strings.TrimSpace(parsed.Receipt.Query)
	parsed.Receipt.Skill = strings.TrimSpace(parsed.Receipt.Skill)
	if parsed.Receipt.Tool != "skill_catalog" || parsed.Receipt.Action == "" {
		return completionSkillCatalogReceipt{}, false
	}
	return parsed.Receipt, true
}

func observedCommandCatalogReceipts(messages []llm.Message) []completionCommandCatalogReceipt {
	toolNamesByID := make(map[string]string)
	var receipts []completionCommandCatalogReceipt
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.ToolUseBlock:
				if b.ID != "" {
					toolNamesByID[b.ID] = b.Name
				}
			case llm.ToolResultBlock:
				if b.IsError || toolNamesByID[b.ToolUseID] != "command_catalog" {
					continue
				}
				receipt, ok := commandCatalogReceiptFromOutput(b.Content)
				if ok {
					receipts = append(receipts, receipt)
				}
			}
		}
	}
	if len(receipts) == 0 {
		return nil
	}
	return receipts
}

func commandCatalogReceiptFromOutput(output string) (completionCommandCatalogReceipt, bool) {
	var parsed struct {
		Receipt completionCommandCatalogReceipt `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return completionCommandCatalogReceipt{}, false
	}
	parsed.Receipt.Tool = strings.TrimSpace(parsed.Receipt.Tool)
	parsed.Receipt.Action = strings.TrimSpace(parsed.Receipt.Action)
	parsed.Receipt.ExecutionPolicy = strings.TrimSpace(parsed.Receipt.ExecutionPolicy)
	parsed.Receipt.Query = strings.TrimSpace(parsed.Receipt.Query)
	parsed.Receipt.Command = strings.TrimSpace(parsed.Receipt.Command)
	if parsed.Receipt.Tool != "command_catalog" || parsed.Receipt.Action == "" {
		return completionCommandCatalogReceipt{}, false
	}
	return parsed.Receipt, true
}

func observedConditionalSkillMatches(messages []llm.Message) []completionConditionalSkillMatch {
	toolNamesByID := make(map[string]string)
	var matches []completionConditionalSkillMatch
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.ToolUseBlock:
				if b.ID != "" {
					toolNamesByID[b.ID] = b.Name
				}
			case llm.ToolResultBlock:
				if b.IsError || toolNamesByID[b.ToolUseID] != "skill_catalog" {
					continue
				}
				matches = append(matches, conditionalSkillMatchesFromCatalogOutput(b.Content)...)
			}
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].SkillName != matches[j].SkillName {
			return matches[i].SkillName < matches[j].SkillName
		}
		if matches[i].Pattern != matches[j].Pattern {
			return matches[i].Pattern < matches[j].Pattern
		}
		return matches[i].Path < matches[j].Path
	})
	return matches
}

func conditionalSkillMatchesFromCatalogOutput(output string) []completionConditionalSkillMatch {
	var parsed struct {
		Action  string                            `json:"action"`
		Matches []completionConditionalSkillMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return nil
	}
	if parsed.Action != "match_paths" || len(parsed.Matches) == 0 {
		return nil
	}
	out := make([]completionConditionalSkillMatch, 0, len(parsed.Matches))
	for _, match := range parsed.Matches {
		match.SkillName = strings.TrimSpace(match.SkillName)
		match.Pattern = strings.TrimSpace(match.Pattern)
		match.Path = strings.TrimSpace(match.Path)
		match.Source = strings.TrimSpace(match.Source)
		match.TrustLevel = strings.TrimSpace(match.TrustLevel)
		if match.SkillName == "" || match.Pattern == "" || match.Path == "" {
			continue
		}
		out = append(out, match)
	}
	return out
}

func withProviderCapabilities(summary completionContractSummary, provider llm.Provider) completionContractSummary {
	caps := llm.CapabilitiesOf(provider)
	summary.ProviderName = caps.Name
	summary.ProviderEffort = caps.ReasoningEffort
	summary.ProviderEffortNote = caps.ReasoningEffortFallback
	return summary
}

func completionRetryPlan(summary completionContractSummary) (string, string) {
	if summary.CompletionWarning == "final_response_reports_incomplete" {
		return completionRetryDecisionRetrySmallerScope, summary.CompletionWarning
	}
	if summary.CompletionWarning == "edit_intent_without_mutation" {
		return completionRetryDecisionRetrySmallerScope, summary.CompletionWarning
	}
	if summary.CompletionWarning == "verification_command_failed" {
		return completionRetryDecisionRetrySmallerScope, summary.CompletionWarning
	}
	if summary.CompletionWarning == "unsupported_verification_success_claim" {
		return completionRetryDecisionRetrySmallerScope, summary.CompletionWarning
	}
	if summary.VerificationObserved != nil && !*summary.VerificationObserved {
		return completionRetryDecisionRunVerification, "verification_hint_not_observed"
	}
	return "", ""
}

func verificationObservedInMessages(messages []llm.Message) bool {
	return observedVerificationCommand(messages) != ""
}

func observedVerificationCommand(messages []llm.Message) string {
	command, _ := observedVerificationCommandStatus(messages)
	return command
}

func observedVerificationCommandStatus(messages []llm.Message) (string, bool) {
	pending := make(map[string]string)
	lastCommand := ""
	lastFailed := false
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.ToolUseBlock:
				if b.Name != "bash" {
					continue
				}
				command := strings.TrimSpace(bashCommandFromToolInput(b.Input))
				if !isVerificationCommand(command) {
					continue
				}
				if b.ID == "" {
					return command, false
				}
				pending[b.ID] = command
				lastCommand = command
				lastFailed = false
			case llm.ToolResultBlock:
				command, ok := pending[b.ToolUseID]
				if !ok {
					continue
				}
				lastCommand = command
				lastFailed = b.IsError
				delete(pending, b.ToolUseID)
			}
		}
	}
	return lastCommand, lastFailed
}

func bashCommandFromToolInput(input json.RawMessage) string {
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	return payload.Command
}

func isVerificationCommand(command string) bool {
	return verificationCommandRE.MatchString(command)
}

func editIntentDetected(messages []llm.Message) bool {
	text := strings.ToLower(strings.TrimSpace(userMessageText(messages)))
	if text == "" {
		return false
	}
	return completionContainsAny(text, []string{
		"fix",
		"repair",
		"implement",
		"change",
		"modify",
		"update",
		"refactor",
		"patch",
		"write",
		"edit",
		"수정",
		"고쳐",
		"구현",
		"변경",
		"패치",
		"리팩터",
	})
}

func mutationObservedInMessages(messages []llm.Message) bool {
	mutatingToolUseIDs := make(map[string]struct{})
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.ToolUseBlock:
				if !mutatingToolUseObserved(b) {
					continue
				}
				if b.ID == "" {
					return true
				}
				mutatingToolUseIDs[b.ID] = struct{}{}
			case llm.ToolResultBlock:
				if _, ok := mutatingToolUseIDs[b.ToolUseID]; !ok {
					continue
				}
				if !b.IsError {
					return true
				}
				delete(mutatingToolUseIDs, b.ToolUseID)
			}
		}
	}
	return false
}

func mutatingToolUseObserved(toolUse llm.ToolUseBlock) bool {
	switch toolUse.Name {
	case "write_file", "edit_file", "wiki_write":
		return true
	case "worktree_run":
		return bashCommandLooksMutating(worktreeRunCommandFromToolInput(toolUse.Input))
	case "git":
		var payload struct {
			Subcommand string `json:"subcommand"`
		}
		if err := json.Unmarshal(toolUse.Input, &payload); err != nil {
			return false
		}
		return payload.Subcommand == "commit"
	case "bash":
		return bashCommandLooksMutating(bashCommandFromToolInput(toolUse.Input))
	default:
		return false
	}
}

func worktreeRunCommandFromToolInput(input json.RawMessage) string {
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	return payload.Command
}

func bashCommandLooksMutating(command string) bool {
	return mutatingBashCommandRE.MatchString(command)
}

func userMessageText(messages []llm.Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role != llm.RoleUser {
			continue
		}
		if text := strings.TrimSpace(msg.Text()); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func completionContainsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func finalAssistantReportsIncomplete(messages []llm.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleAssistant {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(messages[i].Text()))
		if text == "" {
			return false
		}
		for _, marker := range []string{
			"could not finish",
			"couldn't finish",
			"did not finish",
			"didn't finish",
			"not complete",
			"incomplete",
			"still need",
			"unable to complete",
			"완료하지 못",
			"아직 완료",
			"아직 남",
		} {
			if strings.Contains(text, marker) {
				return true
			}
		}
		return false
	}
	return false
}

func finalAssistantClaimsVerificationSuccess(messages []llm.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleAssistant {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(messages[i].Text()))
		if text == "" {
			return false
		}
		for _, marker := range []string{
			"all tests pass",
			"all tests passed",
			"tests pass",
			"tests passed",
			"test suite passes",
			"test suite passed",
			"verification passed",
			"verified successfully",
			"검증 통과",
			"테스트 통과",
		} {
			if strings.Contains(text, marker) {
				return true
			}
		}
		return false
	}
	return false
}
