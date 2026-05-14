package completion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"
)

const (
	ModeObserve      = "observe"
	ModeVerification = "verification"
)

var (
	ErrNilStore      = errors.New("completion gate: nil store")
	ErrMissingTaskID = errors.New("completion gate: agentic task id is required")
)

type Store interface {
	CreateCompletionGate(context.Context, agentic.CompletionGate) (*agentic.CompletionGate, error)
	ListVerificationRunsByTask(context.Context, int64) ([]agentic.VerificationRun, error)
	ListToolActionReceiptsByTask(context.Context, int64) ([]agentic.ToolActionReceipt, error)
}

type CompletionContext struct {
	VerificationHint         bool
	VerificationObserved     *bool
	VerificationCommand      string
	VerificationClass        string
	VerificationOwnership    string
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
	SkillCatalogReceipts     []SkillCatalogReceipt
	SkillExecutionReceipts   []SkillExecutionReceipt
	CommandCatalogReceipts   []CommandCatalogReceipt
	ShellCommandReceipts     []ShellCommandReceipt
	ToolSearchReceipts       []ToolSearchReceipt
	ControlToolReceipts      []ControlToolReceipt
	ConditionalSkillMatches  []ConditionalSkillMatch
	CorrectionAttempted      bool
	CorrectionAttempts       int
	CorrectionMaxAttempts    int
	CorrectionDecision       string
	CorrectionReason         string
	CorrectionStatus         string
	CorrectionFailureFamily  string
	CorrectionAttemptDetails []CorrectionAttemptReceipt
	RetryDecision            string
	RetryReason              string
	RecoveryScopeLabel       string
	AllowedRecoveryPaths     []string
	ForbiddenRecoveryPaths   []string
	MutatedPaths             []string
	OutOfScopeChangedFiles   []string
}

type CorrectionAttemptReceipt struct {
	Attempt             int      `json:"attempt"`
	Decision            string   `json:"decision,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	Status              string   `json:"status,omitempty"`
	FailureFamily       string   `json:"failure_family,omitempty"`
	VerificationCommand string   `json:"verification_command,omitempty"`
	CompletionWarning   string   `json:"completion_warning,omitempty"`
	ChangedFiles        []string `json:"changed_files,omitempty"`
	OutOfScopeFiles     []string `json:"out_of_scope_files,omitempty"`
}

type ConditionalSkillMatch struct {
	SkillName  string `json:"skill_name"`
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Source     string `json:"source,omitempty"`
	TrustLevel string `json:"trust_level,omitempty"`
	External   bool   `json:"external"`
}

type SkillCatalogReceipt struct {
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

type SkillExecutionReceipt struct {
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

type CommandCatalogReceipt struct {
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

type ShellCommandReceipt struct {
	Tool                  string `json:"tool"`
	Action                string `json:"action"`
	ExecutionPolicy       string `json:"execution_policy,omitempty"`
	CommandIntent         string `json:"command_intent,omitempty"`
	IntentSource          string `json:"intent_source,omitempty"`
	CommandClass          string `json:"command_class,omitempty"`
	Status                string `json:"status,omitempty"`
	Classification        string `json:"classification,omitempty"`
	TimedOut              bool   `json:"timed_out,omitempty"`
	Canceled              bool   `json:"canceled,omitempty"`
	IsError               bool   `json:"is_error,omitempty"`
	TimeoutMS             int    `json:"timeout_ms,omitempty"`
	WorkingDirSet         bool   `json:"working_dir_set,omitempty"`
	CommandLen            int    `json:"command_len,omitempty"`
	BackgroundRecommended bool   `json:"background_recommended,omitempty"`
}

type ToolSearchReceipt struct {
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

type ControlToolReceipt struct {
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
	CommandIntent           string   `json:"command_intent,omitempty"`
	IntentSource            string   `json:"intent_source,omitempty"`
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

type CompletionContextProvider interface {
	CompletionContext(context.Context, daemon.Task, int64) (CompletionContext, error)
}

type Option func(*Gate)

func WithCompletionContextProvider(provider CompletionContextProvider) Option {
	return func(g *Gate) { g.contextProvider = provider }
}

type Gate struct {
	store           Store
	mode            string
	now             func() time.Time
	contextProvider CompletionContextProvider
}

func NewGate(store Store, mode string, opts ...Option) *Gate {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeObserve
	}
	g := &Gate{store: store, mode: mode, now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

func (g *Gate) Validate(_ context.Context, task daemon.Task, agenticTaskID int64) error {
	requested := requestedGate(task)
	if requested == "" {
		return nil
	}
	if requested != ModeVerification {
		return fmt.Errorf("unsupported agentic completion gate mode %q", requested)
	}
	if g == nil || g.store == nil {
		return ErrNilStore
	}
	if g.mode != ModeVerification {
		return fmt.Errorf("completion gate requested but config maximum is %q", g.mode)
	}
	if agenticTaskID == 0 {
		return ErrMissingTaskID
	}
	return nil
}

func (g *Gate) Evaluate(ctx context.Context, task daemon.Task, agenticTaskID int64) (daemon.CompletionGateDecision, error) {
	if err := g.Validate(ctx, task, agenticTaskID); err != nil {
		return daemon.CompletionGateDecision{}, err
	}
	if requestedGate(task) == "" {
		return daemon.CompletionGateDecision{Passed: true, Status: agentic.CompletionGateStatusPassed, Reason: "completion gate not requested"}, nil
	}

	run, verifierReason, err := g.latestRelevantVerification(ctx, task, agenticTaskID)
	if err != nil {
		return daemon.CompletionGateDecision{}, fmt.Errorf("completion gate verifier lookup: %w", err)
	}
	receipts, err := g.store.ListToolActionReceiptsByTask(ctx, agenticTaskID)
	if err != nil {
		return daemon.CompletionGateDecision{}, fmt.Errorf("completion gate receipt lookup: %w", err)
	}
	summary := receiptSummary(receipts)
	completionContext := g.completionContext(ctx, task, agenticTaskID)
	summaryJSON := encodeReceiptSummary(summary, completionContext)

	if verifierReason != "" {
		return g.record(ctx, task, agenticTaskID, verificationID(run), agentic.CompletionGateStatusBlocked, verifierReason, summaryJSON)
	}
	if started := summary[agentic.ReceiptStatusStarted]; started > 0 {
		return g.record(ctx, task, agenticTaskID, run.ID, agentic.CompletionGateStatusBlocked, fmt.Sprintf("non-terminal receipt started=%d", started), summaryJSON)
	}
	if completionContext.CompletionWarning != "" {
		return g.record(ctx, task, agenticTaskID, run.ID, agentic.CompletionGateStatusBlocked, "completion warning: "+completionContext.CompletionWarning, summaryJSON)
	}
	return g.record(ctx, task, agenticTaskID, run.ID, agentic.CompletionGateStatusPassed, "verification passed", summaryJSON)
}

func (g *Gate) completionContext(ctx context.Context, task daemon.Task, agenticTaskID int64) CompletionContext {
	if g == nil || g.contextProvider == nil {
		return CompletionContext{}
	}
	completionContext, err := g.contextProvider.CompletionContext(ctx, task, agenticTaskID)
	if err != nil {
		return CompletionContext{}
	}
	return completionContext
}

func (g *Gate) latestRelevantVerification(ctx context.Context, task daemon.Task, taskID int64) (*agentic.VerificationRun, string, error) {
	runs, err := g.store.ListVerificationRunsByTask(ctx, taskID)
	if err != nil {
		return nil, "", err
	}
	if len(runs) == 0 {
		return nil, "missing verifier run", nil
	}
	var latestAny *agentic.VerificationRun
	var latestRelevant *agentic.VerificationRun
	for i := range runs {
		run := runs[i]
		if latestAny == nil || run.ID > latestAny.ID {
			latestAny = &run
		}
		if !task.StartedAt.IsZero() && run.CreatedAt.Before(task.StartedAt) {
			continue
		}
		if latestRelevant == nil || run.ID > latestRelevant.ID {
			latestRelevant = &run
		}
	}
	if latestRelevant == nil {
		return latestAny, "stale verifier run", nil
	}
	if latestRelevant.Verdict != agentic.VerificationVerdictPassed {
		return latestRelevant, "verifier " + latestRelevant.Verdict, nil
	}
	return latestRelevant, "", nil
}

func (g *Gate) record(ctx context.Context, task daemon.Task, taskID, runID int64, status, reason, summaryJSON string) (daemon.CompletionGateDecision, error) {
	now := g.now()
	gate, err := g.store.CreateCompletionGate(ctx, agentic.CompletionGate{
		TaskID:             taskID,
		QueueTaskID:        task.ID,
		VerificationRunID:  runID,
		Status:             status,
		Reason:             reason,
		ReceiptSummaryJSON: summaryJSON,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		return daemon.CompletionGateDecision{}, fmt.Errorf("completion gate ledger: %w", err)
	}
	return daemon.CompletionGateDecision{
		Passed:            status == agentic.CompletionGateStatusPassed,
		Status:            status,
		Reason:            reason,
		VerificationRunID: runID,
		GateID:            gate.ID,
	}, nil
}

func requestedGate(task daemon.Task) string {
	return strings.ToLower(strings.TrimSpace(daemon.ParseTaskPayload(task.Payload).AgenticCompletionGate))
}

func receiptSummary(receipts []agentic.ToolActionReceipt) map[string]int {
	summary := map[string]int{
		agentic.ReceiptStatusStarted:          0,
		agentic.ReceiptStatusSucceeded:        0,
		agentic.ReceiptStatusFailed:           0,
		agentic.ReceiptStatusDenied:           0,
		agentic.ReceiptStatusApprovalRequired: 0,
	}
	for _, receipt := range receipts {
		summary[receipt.Status]++
	}
	return summary
}

func encodeReceiptSummary(summary map[string]int, completionContext CompletionContext) string {
	payload := make(map[string]any, len(summary)+5)
	for status, count := range summary {
		payload[status] = count
	}
	if completionContext.VerificationHint {
		payload["verification_hint"] = true
	}
	if completionContext.VerificationObserved != nil {
		payload["verification_observed"] = *completionContext.VerificationObserved
	}
	if completionContext.VerificationCommand != "" {
		payload["verification_command"] = completionContext.VerificationCommand
	}
	if completionContext.VerificationClass != "" {
		payload["verification_class"] = completionContext.VerificationClass
	}
	if completionContext.VerificationOwnership != "" {
		payload["verification_ownership"] = completionContext.VerificationOwnership
	}
	if completionContext.CompletionWarning != "" {
		payload["completion_warning"] = completionContext.CompletionWarning
	}
	if completionContext.UserInputRequired {
		payload["user_input_required"] = true
	}
	if completionContext.EditIntent {
		payload["edit_intent"] = true
	}
	if completionContext.EditObserved != nil {
		payload["edit_observed"] = *completionContext.EditObserved
	}
	if completionContext.ReasoningEffort != "" {
		payload["reasoning_effort"] = completionContext.ReasoningEffort
	}
	if completionContext.ReasoningEffortMode != "" {
		payload["reasoning_effort_mode"] = completionContext.ReasoningEffortMode
	}
	if completionContext.ReasoningEffortReason != "" {
		payload["reasoning_effort_reason"] = completionContext.ReasoningEffortReason
	}
	if completionContext.ProviderName != "" {
		payload["provider_name"] = completionContext.ProviderName
	}
	if completionContext.ProviderEffort != "" {
		payload["provider_effort"] = completionContext.ProviderEffort
	}
	if completionContext.ProviderEffortNote != "" {
		payload["provider_effort_note"] = completionContext.ProviderEffortNote
	}
	if len(completionContext.LoadedDeferredTools) > 0 {
		payload["loaded_deferred_tools"] = completionContext.LoadedDeferredTools
	}
	if len(completionContext.SkillCatalogReceipts) > 0 {
		payload["skill_catalog_receipts"] = completionContext.SkillCatalogReceipts
	}
	if len(completionContext.SkillExecutionReceipts) > 0 {
		payload["skill_execution_receipts"] = completionContext.SkillExecutionReceipts
	}
	if len(completionContext.CommandCatalogReceipts) > 0 {
		payload["command_catalog_receipts"] = completionContext.CommandCatalogReceipts
	}
	if len(completionContext.ShellCommandReceipts) > 0 {
		payload["shell_command_receipts"] = completionContext.ShellCommandReceipts
	}
	if len(completionContext.ToolSearchReceipts) > 0 {
		payload["tool_search_receipts"] = completionContext.ToolSearchReceipts
	}
	if len(completionContext.ControlToolReceipts) > 0 {
		payload["control_tool_receipts"] = completionContext.ControlToolReceipts
	}
	if len(completionContext.ConditionalSkillMatches) > 0 {
		payload["conditional_skill_matches"] = completionContext.ConditionalSkillMatches
	}
	if completionContext.CorrectionAttempted {
		payload["correction_attempted"] = true
	}
	if completionContext.CorrectionAttempts > 0 {
		payload["correction_attempts"] = completionContext.CorrectionAttempts
	}
	if completionContext.CorrectionMaxAttempts > 0 {
		payload["correction_max_attempts"] = completionContext.CorrectionMaxAttempts
	}
	if completionContext.CorrectionDecision != "" {
		payload["correction_decision"] = completionContext.CorrectionDecision
	}
	if completionContext.CorrectionReason != "" {
		payload["correction_reason"] = completionContext.CorrectionReason
	}
	if completionContext.CorrectionStatus != "" {
		payload["correction_status"] = completionContext.CorrectionStatus
	}
	if completionContext.CorrectionFailureFamily != "" {
		payload["correction_failure_family"] = completionContext.CorrectionFailureFamily
	}
	if len(completionContext.CorrectionAttemptDetails) > 0 {
		payload["correction_attempt_details"] = completionContext.CorrectionAttemptDetails
	}
	if completionContext.RetryDecision != "" {
		payload["retry_decision"] = completionContext.RetryDecision
	}
	if completionContext.RetryReason != "" {
		payload["retry_reason"] = completionContext.RetryReason
	}
	if completionContext.RecoveryScopeLabel != "" {
		payload["recovery_scope_label"] = completionContext.RecoveryScopeLabel
	}
	if len(completionContext.AllowedRecoveryPaths) > 0 {
		payload["allowed_recovery_paths"] = completionContext.AllowedRecoveryPaths
	}
	if len(completionContext.ForbiddenRecoveryPaths) > 0 {
		payload["forbidden_recovery_paths"] = completionContext.ForbiddenRecoveryPaths
	}
	if len(completionContext.MutatedPaths) > 0 {
		payload["mutated_paths"] = completionContext.MutatedPaths
	}
	if len(completionContext.OutOfScopeChangedFiles) > 0 {
		payload["out_of_scope_changed_files"] = completionContext.OutOfScopeChangedFiles
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func verificationID(run *agentic.VerificationRun) int64 {
	if run == nil {
		return 0
	}
	return run.ID
}
