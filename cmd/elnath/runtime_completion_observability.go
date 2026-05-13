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
	VerificationHint         bool
	VerificationObserved     *bool
	VerificationCommand      string
	CompletionWarning        string
	UserInputRequired        bool
	EditIntent               bool
	EditObserved             *bool
	ReasoningEffort          string
	ReasoningEffortMode      string
	ReasoningEffortReason    string
	ProviderName             string
	ProviderEffort           string
	ProviderEffortNote       string
	LoadedDeferredTools      []string
	ConditionalSkillMatches  []completionConditionalSkillMatch
	SkillCatalogReceipts     []completionSkillCatalogReceipt
	SkillExecutionReceipts   []completionSkillExecutionReceipt
	CommandCatalogReceipts   []completionCommandCatalogReceipt
	ToolSearchReceipts       []completionToolSearchReceipt
	ControlToolReceipts      []completionControlToolReceipt
	CorrectionAttempted      bool
	CorrectionAttempts       int
	CorrectionMaxAttempts    int
	CorrectionDecision       string
	CorrectionReason         string
	CorrectionStatus         string
	CorrectionFailureFamily  string
	CorrectionAttemptDetails []completionCorrectionAttemptReceipt
	RetryDecision            string
	RetryReason              string
}

type completionCorrectionAttemptReceipt struct {
	Attempt             int    `json:"attempt"`
	Decision            string `json:"decision,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Status              string `json:"status,omitempty"`
	FailureFamily       string `json:"failure_family,omitempty"`
	VerificationCommand string `json:"verification_command,omitempty"`
	CompletionWarning   string `json:"completion_warning,omitempty"`
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

type completionSkillExecutionReceipt struct {
	Tool                string   `json:"tool"`
	Action              string   `json:"action"`
	Skill               string   `json:"skill"`
	Status              string   `json:"status,omitempty"`
	Provider            string   `json:"provider,omitempty"`
	Model               string   `json:"model,omitempty"`
	ReasoningEffort     string   `json:"reasoning_effort,omitempty"`
	ReasoningEffortMode string   `json:"reasoning_effort_mode,omitempty"`
	PermissionMode      string   `json:"permission_mode,omitempty"`
	MaxIterations       int      `json:"max_iterations,omitempty"`
	RequiredTools       []string `json:"required_tools,omitempty"`
	AvailableTools      []string `json:"available_tools,omitempty"`
	ToolFilterApplied   bool     `json:"tool_filter_applied"`
	BaseDir             string   `json:"base_dir,omitempty"`
	Source              string   `json:"source,omitempty"`
	TrustLevel          string   `json:"trust_level,omitempty"`
	External            bool     `json:"external"`
	UserInvocable       bool     `json:"user_invocable"`
}

type completionCommandCatalogReceipt struct {
	Tool                  string `json:"tool"`
	Action                string `json:"action"`
	ReadOnly              bool   `json:"read_only"`
	RegistryAvailable     bool   `json:"registry_available"`
	ExecutionAvailable    bool   `json:"execution_available"`
	ExecutionPolicy       string `json:"execution_policy"`
	TotalCommands         int    `json:"total_commands"`
	ReturnedCommands      int    `json:"returned_commands"`
	ExecutableCommands    int    `json:"executable_commands,omitempty"`
	ModelCallableCommands int    `json:"model_callable_commands,omitempty"`
	IncludeHidden         bool   `json:"include_hidden"`
	MaxResults            int    `json:"max_results,omitempty"`
	Query                 string `json:"query,omitempty"`
	Command               string `json:"command,omitempty"`
	FollowupTool          string `json:"followup_tool,omitempty"`
}

type completionToolSearchReceipt struct {
	Tool               string `json:"tool"`
	Action             string `json:"action"`
	ReadOnly           bool   `json:"read_only"`
	RegistryAvailable  bool   `json:"registry_available"`
	ExecutionAvailable bool   `json:"execution_available"`
	ExecutionPolicy    string `json:"execution_policy"`
	TotalTools         int    `json:"total_tools"`
	ReturnedMatches    int    `json:"returned_matches"`
	DeferredMatches    int    `json:"deferred_matches"`
	MaxResults         int    `json:"max_results"`
	AllowNamesCount    int    `json:"allow_names_count"`
	Query              string `json:"query"`
}

type completionControlToolReceipt struct {
	Tool                    string   `json:"tool"`
	Action                  string   `json:"action"`
	ReadOnly                bool     `json:"read_only"`
	Persistent              bool     `json:"persistent"`
	RequestID               string   `json:"request_id,omitempty"`
	SessionID               string   `json:"session_id,omitempty"`
	QueueBacked             bool     `json:"queue_backed,omitempty"`
	RegistryBacked          bool     `json:"registry_backed,omitempty"`
	ExecutionAvailable      bool     `json:"execution_available,omitempty"`
	ExecutionPolicy         string   `json:"execution_policy,omitempty"`
	FollowupTool            string   `json:"followup_tool,omitempty"`
	TaskID                  int64    `json:"task_id,omitempty"`
	ParentTaskID            int64    `json:"parent_task_id,omitempty"`
	ChildTaskID             int64    `json:"child_task_id,omitempty"`
	QueueTaskID             int64    `json:"queue_task_id,omitempty"`
	ProcessID               int64    `json:"process_id,omitempty"`
	DecisionID              int64    `json:"decision_id,omitempty"`
	DecisionStatus          string   `json:"decision_status,omitempty"`
	Status                  string   `json:"status,omitempty"`
	PreviousStatus          string   `json:"previous_status,omitempty"`
	Terminal                bool     `json:"terminal,omitempty"`
	ExitCode                *int     `json:"exit_code,omitempty"`
	Found                   bool     `json:"found,omitempty"`
	TimeoutMS               int      `json:"timeout_ms,omitempty"`
	CWD                     string   `json:"cwd,omitempty"`
	TailBytes               int      `json:"tail_bytes,omitempty"`
	StdoutRawBytes          int64    `json:"stdout_raw_bytes,omitempty"`
	StderrRawBytes          int64    `json:"stderr_raw_bytes,omitempty"`
	StdoutTruncated         bool     `json:"stdout_truncated,omitempty"`
	StderrTruncated         bool     `json:"stderr_truncated,omitempty"`
	StopSignal              string   `json:"stop_signal,omitempty"`
	EdgeType                string   `json:"edge_type,omitempty"`
	Enqueued                bool     `json:"enqueued,omitempty"`
	Deduplicated            bool     `json:"deduplicated,omitempty"`
	TotalReturned           int      `json:"total_returned,omitempty"`
	Limit                   int      `json:"limit,omitempty"`
	Field                   string   `json:"field,omitempty"`
	RetrievalStatus         string   `json:"retrieval_status,omitempty"`
	Name                    string   `json:"name,omitempty"`
	Path                    string   `json:"path,omitempty"`
	Branch                  string   `json:"branch,omitempty"`
	RegistryPath            string   `json:"registry_path,omitempty"`
	Runner                  string   `json:"runner,omitempty"`
	IsError                 bool     `json:"is_error,omitempty"`
	Removed                 bool     `json:"removed,omitempty"`
	DryRun                  bool     `json:"dry_run,omitempty"`
	Total                   int      `json:"total,omitempty"`
	TaskName                string   `json:"task_name,omitempty"`
	TaskCountBefore         int      `json:"task_count_before,omitempty"`
	TaskCountAfter          int      `json:"task_count_after,omitempty"`
	PreviousMode            string   `json:"previous_mode,omitempty"`
	CurrentMode             string   `json:"current_mode,omitempty"`
	Restored                bool     `json:"restored,omitempty"`
	ReadOnlyAfterTransition bool     `json:"read_only_after_transition,omitempty"`
	FromActorID             int64    `json:"from_actor_id,omitempty"`
	ToActorID               int64    `json:"to_actor_id,omitempty"`
	ActorID                 int64    `json:"actor_id,omitempty"`
	HandoffID               int64    `json:"handoff_id,omitempty"`
	Box                     string   `json:"box,omitempty"`
	Delivered               bool     `json:"delivered,omitempty"`
	Command                 string   `json:"command,omitempty"`
	Args                    []string `json:"args,omitempty"`
	StateMutation           bool     `json:"state_mutation,omitempty"`
	Question                string   `json:"question,omitempty"`
	QuestionChars           int      `json:"question_chars,omitempty"`
	OptionCount             int      `json:"option_count,omitempty"`
	AllowFreeText           bool     `json:"allow_free_text,omitempty"`
	TimeoutSeconds          int      `json:"timeout_seconds,omitempty"`
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
	summary.SkillExecutionReceipts = observedSkillExecutionReceipts(result.Messages)
	summary.CommandCatalogReceipts = observedCommandCatalogReceipts(result.Messages)
	summary.ToolSearchReceipts = observedToolSearchReceipts(result.Messages)
	summary.ControlToolReceipts = observedControlToolReceipts(result.Messages)
	summary.UserInputRequired = controlToolReceiptsContain(summary.ControlToolReceipts, "ask_user_question", "request")

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
	if summary.CompletionWarning == "" && editIntent && strings.EqualFold(strings.TrimSpace(result.FinishReason), "budget_exceeded") {
		summary.CompletionWarning = "budget_exceeded_after_edit_intent"
	}
	summary.RetryDecision, summary.RetryReason = completionRetryPlan(summary)
	return summary
}

func controlToolReceiptsContain(receipts []completionControlToolReceipt, tool string, action string) bool {
	for _, receipt := range receipts {
		if receipt.Tool == tool && (action == "" || receipt.Action == action) {
			return true
		}
	}
	return false
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

func observedSkillExecutionReceipts(messages []llm.Message) []completionSkillExecutionReceipt {
	toolNamesByID := make(map[string]string)
	var receipts []completionSkillExecutionReceipt
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.ToolUseBlock:
				if b.ID != "" {
					toolNamesByID[b.ID] = b.Name
				}
			case llm.ToolResultBlock:
				if b.IsError || toolNamesByID[b.ToolUseID] != "skill" {
					continue
				}
				receipt, ok := skillExecutionReceiptFromOutput(b.Content)
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

func skillExecutionReceiptFromOutput(output string) (completionSkillExecutionReceipt, bool) {
	var parsed struct {
		Skill      string                          `json:"skill"`
		Status     string                          `json:"status"`
		Source     string                          `json:"source"`
		TrustLevel string                          `json:"trust_level"`
		External   bool                            `json:"external"`
		Receipt    completionSkillExecutionReceipt `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return completionSkillExecutionReceipt{}, false
	}
	receipt := parsed.Receipt
	receipt.Tool = strings.TrimSpace(receipt.Tool)
	receipt.Action = strings.TrimSpace(receipt.Action)
	receipt.Skill = strings.TrimSpace(receipt.Skill)
	receipt.Status = strings.TrimSpace(receipt.Status)
	receipt.Provider = strings.TrimSpace(receipt.Provider)
	receipt.Model = strings.TrimSpace(receipt.Model)
	receipt.ReasoningEffort = strings.TrimSpace(receipt.ReasoningEffort)
	receipt.ReasoningEffortMode = strings.TrimSpace(receipt.ReasoningEffortMode)
	receipt.PermissionMode = strings.TrimSpace(receipt.PermissionMode)
	receipt.BaseDir = strings.TrimSpace(receipt.BaseDir)
	receipt.Source = strings.TrimSpace(receipt.Source)
	receipt.TrustLevel = strings.TrimSpace(receipt.TrustLevel)
	if receipt.Tool == "" {
		receipt.Tool = "skill"
	}
	if receipt.Action == "" {
		receipt.Action = "execute"
	}
	if receipt.Skill == "" {
		receipt.Skill = strings.TrimSpace(parsed.Skill)
	}
	if receipt.Status == "" {
		receipt.Status = strings.TrimSpace(parsed.Status)
	}
	if receipt.Source == "" {
		receipt.Source = strings.TrimSpace(parsed.Source)
	}
	if receipt.TrustLevel == "" {
		receipt.TrustLevel = strings.TrimSpace(parsed.TrustLevel)
	}
	receipt.External = receipt.External || parsed.External
	if receipt.Tool != "skill" || receipt.Action != "execute" || receipt.Skill == "" {
		return completionSkillExecutionReceipt{}, false
	}
	return receipt, true
}

func observedToolSearchReceipts(messages []llm.Message) []completionToolSearchReceipt {
	toolNamesByID := make(map[string]string)
	var receipts []completionToolSearchReceipt
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.ToolUseBlock:
				if b.ID != "" {
					toolNamesByID[b.ID] = b.Name
				}
			case llm.ToolResultBlock:
				if b.IsError || toolNamesByID[b.ToolUseID] != "tool_search" {
					continue
				}
				receipt, ok := toolSearchReceiptFromOutput(b.Content)
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

func toolSearchReceiptFromOutput(output string) (completionToolSearchReceipt, bool) {
	var parsed struct {
		Receipt completionToolSearchReceipt `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return completionToolSearchReceipt{}, false
	}
	parsed.Receipt.Tool = strings.TrimSpace(parsed.Receipt.Tool)
	parsed.Receipt.Action = strings.TrimSpace(parsed.Receipt.Action)
	parsed.Receipt.ExecutionPolicy = strings.TrimSpace(parsed.Receipt.ExecutionPolicy)
	parsed.Receipt.Query = strings.TrimSpace(parsed.Receipt.Query)
	if parsed.Receipt.Tool != "tool_search" || parsed.Receipt.Action == "" {
		return completionToolSearchReceipt{}, false
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
	parsed.Receipt.FollowupTool = strings.TrimSpace(parsed.Receipt.FollowupTool)
	if parsed.Receipt.Tool != "command_catalog" || parsed.Receipt.Action == "" {
		return completionCommandCatalogReceipt{}, false
	}
	return parsed.Receipt, true
}

var completionControlToolReceiptNames = map[string]struct{}{
	"task_create":              {},
	"user_question_answer":     {},
	"task_list":                {},
	"task_get":                 {},
	"task_stop":                {},
	"task_output":              {},
	"task_monitor":             {},
	"task_update":              {},
	"schedule_create":          {},
	"schedule_list":            {},
	"schedule_delete":          {},
	"enter_plan_mode":          {},
	"exit_plan_mode":           {},
	"enter_worktree":           {},
	"exit_worktree":            {},
	"worktree_list":            {},
	"worktree_run":             {},
	"worktree_prune":           {},
	"process_start":            {},
	"process_monitor":          {},
	"process_stop":             {},
	"sleep":                    {},
	"agentic_delegate_create":  {},
	"agentic_delegate_list":    {},
	"agentic_delegate_status":  {},
	"agentic_delegate_enqueue": {},
	"agentic_message_send":     {},
	"agentic_message_list":     {},
	"runtime_command":          {},
	"ask_user_question":        {},
	"user_question_list":       {},
}

func observedControlToolReceipts(messages []llm.Message) []completionControlToolReceipt {
	toolNamesByID := make(map[string]string)
	var receipts []completionControlToolReceipt
	for _, msg := range messages {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case llm.ToolUseBlock:
				if b.ID != "" {
					toolNamesByID[b.ID] = b.Name
				}
			case llm.ToolResultBlock:
				toolName := toolNamesByID[b.ToolUseID]
				if b.IsError || !isCompletionControlTool(toolName) {
					continue
				}
				receipt, ok := controlToolReceiptFromOutput(toolName, b.Content)
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

func isCompletionControlTool(toolName string) bool {
	_, ok := completionControlToolReceiptNames[toolName]
	return ok
}

func controlToolReceiptFromOutput(toolName, output string) (completionControlToolReceipt, bool) {
	var parsed struct {
		Receipt completionControlToolReceipt `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return completionControlToolReceipt{}, false
	}
	receipt := parsed.Receipt
	receipt.Tool = strings.TrimSpace(receipt.Tool)
	receipt.Action = strings.TrimSpace(receipt.Action)
	receipt.ExecutionPolicy = strings.TrimSpace(receipt.ExecutionPolicy)
	receipt.FollowupTool = strings.TrimSpace(receipt.FollowupTool)
	receipt.RequestID = strings.TrimSpace(receipt.RequestID)
	receipt.Status = strings.TrimSpace(receipt.Status)
	receipt.PreviousStatus = strings.TrimSpace(receipt.PreviousStatus)
	receipt.SessionID = strings.TrimSpace(receipt.SessionID)
	receipt.CWD = strings.TrimSpace(receipt.CWD)
	receipt.StopSignal = strings.TrimSpace(receipt.StopSignal)
	receipt.DecisionStatus = strings.TrimSpace(receipt.DecisionStatus)
	receipt.EdgeType = strings.TrimSpace(receipt.EdgeType)
	receipt.Box = strings.TrimSpace(receipt.Box)
	receipt.Field = strings.TrimSpace(receipt.Field)
	receipt.RetrievalStatus = strings.TrimSpace(receipt.RetrievalStatus)
	receipt.Name = strings.TrimSpace(receipt.Name)
	receipt.Path = strings.TrimSpace(receipt.Path)
	receipt.Branch = strings.TrimSpace(receipt.Branch)
	receipt.RegistryPath = strings.TrimSpace(receipt.RegistryPath)
	receipt.Runner = strings.TrimSpace(receipt.Runner)
	receipt.TaskName = strings.TrimSpace(receipt.TaskName)
	receipt.PreviousMode = strings.TrimSpace(receipt.PreviousMode)
	receipt.CurrentMode = strings.TrimSpace(receipt.CurrentMode)
	receipt.Command = strings.TrimSpace(receipt.Command)
	receipt.Question = strings.TrimSpace(receipt.Question)
	if receipt.Tool != toolName || receipt.Action == "" {
		return completionControlToolReceipt{}, false
	}
	return receipt, true
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
	if summary.UserInputRequired {
		return "", ""
	}
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
	if summary.CompletionWarning == "budget_exceeded_after_edit_intent" {
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
				if !b.IsError && !mutatingToolResultLooksNoop(b.Content) {
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

func mutatingToolResultLooksNoop(output string) bool {
	text := strings.ToLower(strings.TrimSpace(output))
	if text == "" {
		return false
	}
	for _, marker := range []string{
		"no changes",
		"no files changed",
		"0 files changed",
		"file unchanged",
		"content already matches",
		"already matches",
		"old_string and new_string are identical",
		"nothing to commit",
		"working tree clean",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
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
