package learning

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

type OutcomeRecord struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	Intent         string    `json:"intent"`
	Workflow       string    `json:"workflow"`
	FinishReason   string    `json:"finish_reason"`
	Success        bool      `json:"success"`
	Duration       float64   `json:"duration_s"`
	Cost           float64   `json:"cost"`
	Iterations     int       `json:"iterations"`
	InputSnippet   string    `json:"input_snippet,omitempty"`
	EstimatedFiles int       `json:"estimated_files,omitempty"`
	ExistingCode   bool      `json:"existing_code,omitempty"`
	PreferenceUsed bool      `json:"preference_used,omitempty"`
	Timestamp      time.Time `json:"timestamp"`

	// FU-LearningObservability schema extension (2026-04-20). All fields are
	// omitempty so older scorecard code and legacy records continue to read
	// cleanly; the daemon self-analysis lens relies on them being populated
	// going forward.
	MaxIterations int             `json:"max_iterations,omitempty"`
	InputTokens   int             `json:"input_tokens,omitempty"`
	OutputTokens  int             `json:"output_tokens,omitempty"`
	ToolStats     []AgentToolStat `json:"tool_stats,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`

	// Completion observability is intentionally advisory. These fields let the
	// runtime record verification/completion gaps before any blocking retry
	// policy is introduced.
	VerificationHint         bool                       `json:"verification_hint,omitempty"`
	VerificationObserved     *bool                      `json:"verification_observed,omitempty"`
	VerificationCommand      string                     `json:"verification_command,omitempty"`
	CompletionWarning        string                     `json:"completion_warning,omitempty"`
	ReasoningEffort          string                     `json:"reasoning_effort,omitempty"`
	ReasoningEffortMode      string                     `json:"reasoning_effort_mode,omitempty"`
	ReasoningEffortReason    string                     `json:"reasoning_effort_reason,omitempty"`
	ProviderName             string                     `json:"provider_name,omitempty"`
	ProviderEffort           string                     `json:"provider_effort,omitempty"`
	ProviderEffortNote       string                     `json:"provider_effort_note,omitempty"`
	LoadedDeferredTools      []string                   `json:"loaded_deferred_tools,omitempty"`
	SkillCatalogReceipts     []SkillCatalogReceipt      `json:"skill_catalog_receipts,omitempty"`
	SkillExecutionReceipts   []SkillExecutionReceipt    `json:"skill_execution_receipts,omitempty"`
	CommandCatalogReceipts   []CommandCatalogReceipt    `json:"command_catalog_receipts,omitempty"`
	ToolSearchReceipts       []ToolSearchReceipt        `json:"tool_search_receipts,omitempty"`
	ControlToolReceipts      []ControlToolReceipt       `json:"control_tool_receipts,omitempty"`
	ConditionalSkillMatches  []ConditionalSkillMatch    `json:"conditional_skill_matches,omitempty"`
	CorrectionAttempted      bool                       `json:"correction_attempted,omitempty"`
	CorrectionAttempts       int                        `json:"correction_attempts,omitempty"`
	CorrectionMaxAttempts    int                        `json:"correction_max_attempts,omitempty"`
	CorrectionDecision       string                     `json:"correction_decision,omitempty"`
	CorrectionReason         string                     `json:"correction_reason,omitempty"`
	CorrectionStatus         string                     `json:"correction_status,omitempty"`
	CorrectionFailureFamily  string                     `json:"correction_failure_family,omitempty"`
	CorrectionAttemptDetails []CorrectionAttemptReceipt `json:"correction_attempt_details,omitempty"`
	RetryDecision            string                     `json:"retry_decision,omitempty"`
	RetryReason              string                     `json:"retry_reason,omitempty"`
}

type CorrectionAttemptReceipt struct {
	Attempt             int    `json:"attempt"`
	Decision            string `json:"decision,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Status              string `json:"status,omitempty"`
	FailureFamily       string `json:"failure_family,omitempty"`
	VerificationCommand string `json:"verification_command,omitempty"`
	CompletionWarning   string `json:"completion_warning,omitempty"`
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
	Tool                    string `json:"tool"`
	Action                  string `json:"action"`
	ReadOnly                bool   `json:"read_only"`
	Persistent              bool   `json:"persistent"`
	QueueBacked             bool   `json:"queue_backed,omitempty"`
	RegistryBacked          bool   `json:"registry_backed,omitempty"`
	ExecutionAvailable      bool   `json:"execution_available,omitempty"`
	ExecutionPolicy         string `json:"execution_policy,omitempty"`
	FollowupTool            string `json:"followup_tool,omitempty"`
	TaskID                  int64  `json:"task_id,omitempty"`
	ParentTaskID            int64  `json:"parent_task_id,omitempty"`
	ChildTaskID             int64  `json:"child_task_id,omitempty"`
	QueueTaskID             int64  `json:"queue_task_id,omitempty"`
	ProcessID               int64  `json:"process_id,omitempty"`
	DecisionID              int64  `json:"decision_id,omitempty"`
	DecisionStatus          string `json:"decision_status,omitempty"`
	Status                  string `json:"status,omitempty"`
	PreviousStatus          string `json:"previous_status,omitempty"`
	Terminal                bool   `json:"terminal,omitempty"`
	ExitCode                *int   `json:"exit_code,omitempty"`
	Found                   bool   `json:"found,omitempty"`
	TimeoutMS               int    `json:"timeout_ms,omitempty"`
	CWD                     string `json:"cwd,omitempty"`
	TailBytes               int    `json:"tail_bytes,omitempty"`
	StdoutRawBytes          int64  `json:"stdout_raw_bytes,omitempty"`
	StderrRawBytes          int64  `json:"stderr_raw_bytes,omitempty"`
	StdoutTruncated         bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated         bool   `json:"stderr_truncated,omitempty"`
	StopSignal              string `json:"stop_signal,omitempty"`
	EdgeType                string `json:"edge_type,omitempty"`
	Enqueued                bool   `json:"enqueued,omitempty"`
	Deduplicated            bool   `json:"deduplicated,omitempty"`
	TotalReturned           int    `json:"total_returned,omitempty"`
	Limit                   int    `json:"limit,omitempty"`
	Field                   string `json:"field,omitempty"`
	RetrievalStatus         string `json:"retrieval_status,omitempty"`
	Name                    string `json:"name,omitempty"`
	Path                    string `json:"path,omitempty"`
	Branch                  string `json:"branch,omitempty"`
	RegistryPath            string `json:"registry_path,omitempty"`
	Runner                  string `json:"runner,omitempty"`
	IsError                 bool   `json:"is_error,omitempty"`
	Removed                 bool   `json:"removed,omitempty"`
	DryRun                  bool   `json:"dry_run,omitempty"`
	Total                   int    `json:"total,omitempty"`
	TaskName                string `json:"task_name,omitempty"`
	TaskCountBefore         int    `json:"task_count_before,omitempty"`
	TaskCountAfter          int    `json:"task_count_after,omitempty"`
	PreviousMode            string `json:"previous_mode,omitempty"`
	CurrentMode             string `json:"current_mode,omitempty"`
	Restored                bool   `json:"restored,omitempty"`
	ReadOnlyAfterTransition bool   `json:"read_only_after_transition,omitempty"`
	FromActorID             int64  `json:"from_actor_id,omitempty"`
	ToActorID               int64  `json:"to_actor_id,omitempty"`
	ActorID                 int64  `json:"actor_id,omitempty"`
	HandoffID               int64  `json:"handoff_id,omitempty"`
	Box                     string `json:"box,omitempty"`
	Delivered               bool   `json:"delivered,omitempty"`
}

// IsSuccessful returns true for workflow outcomes that count as completion in
// the learning store. Ralph's "unverified_inline" is included per Phase 8.1a
// Fix 2 + partner M3 decision: inline-artifact answers (guard-gated) are
// honest non-verification completions, not failures. Recording them as
// failures would train the router to avoid ralph for future inline tasks.
func IsSuccessful(finishReason string) bool {
	switch finishReason {
	case "stop", "partial_success", "unverified_inline":
		return true
	default:
		return false
	}
}

func ShouldRecord(finishReason string) bool {
	return finishReason != ""
}

func deriveOutcomeID(projectID, intent, workflow string, ts time.Time) string {
	sum := sha256.Sum256([]byte(projectID + intent + workflow + ts.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])[:8]
}
