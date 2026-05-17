package agentic

import (
	"database/sql"
	"time"
)

const (
	GoalStatusActive     = "active"
	AutonomyLevelObserve = "observe"

	SignalStatusNew     = "new"
	SignalStatusTriaged = "triaged"
	SignalStatusFailed  = "failed"

	TaskStatusProposed        = "proposed"
	TaskStatusPending         = "pending"
	TaskStatusRunning         = "running"
	TaskStatusSucceeded       = "succeeded"
	TaskStatusFailed          = "failed"
	TaskStatusCanceled        = "canceled"
	RiskLevelLow              = "low"
	RiskLevelMedium           = "medium"
	RiskLevelHigh             = "high"
	RiskLevelCritical         = "critical"
	PolicyDecisionObserve     = "observe"
	VerificationStatusPending = "pending"

	PolicyDecisionAutoAllowed      = "auto_allowed"
	PolicyDecisionApprovalRequired = "approval_required"
	PolicyDecisionHardlineDenied   = "hardline_denied"
	PolicyDecisionObserveOnly      = "observe_only"
	PolicyDecisionEscalated        = "escalated"

	PolicyDecisionAuto            = PolicyDecisionAutoAllowed
	PolicyDecisionDenied          = PolicyDecisionHardlineDenied
	PolicyDecisionRequireApproval = PolicyDecisionApprovalRequired

	ReceiptStatusStarted          = "started"
	ReceiptStatusSucceeded        = "succeeded"
	ReceiptStatusFailed           = "failed"
	ReceiptStatusApprovalRequired = "approval_required"
	ReceiptStatusDenied           = "denied"

	CompletionGateStatusPassed  = "passed"
	CompletionGateStatusBlocked = "blocked"
	CompletionGateStatusFailed  = "failed"

	TaskEnqueueDecisionApproved = "approved"
	TaskEnqueueDecisionDenied   = "denied"
	TaskEnqueueDecisionCanceled = "canceled"

	TaskEnqueueStatusPending  = "pending"
	TaskEnqueueStatusEnqueued = "enqueued"
	TaskEnqueueStatusFailed   = "failed"
	TaskEnqueueStatusCanceled = "canceled"

	VerificationVerdictPassed       = "passed"
	VerificationVerdictFailed       = "failed"
	VerificationVerdictInconclusive = "inconclusive"
	VerificationVerdictPass         = VerificationVerdictPassed

	MemoryUpdateStatusPending = "pending"
	MemoryUpdateStatusApplied = "applied"
	MemoryUpdateStatusBlocked = "blocked"
	MemoryUpdateStatusFailed  = "failed"
	MemoryUpdateStatusSkipped = "skipped"

	FollowupStatusPending    = "pending"
	FollowupStatusProcessing = "processing"
	FollowupStatusCreated    = "created"
	FollowupStatusSkipped    = "skipped"
	FollowupStatusFailed     = "failed"
	FollowupStatusCanceled   = "canceled"

	ActivationRunStatusSucceeded = "succeeded"
	ActivationRunStatusFailed    = "failed"

	ActorStatusCreated   = "created"
	ActorStatusRunning   = "running"
	ActorStatusSucceeded = "succeeded"
	ActorStatusFailed    = "failed"
	ActorStatusCanceled  = "canceled"

	ActorRolePlanner     = "planner"
	ActorRoleExecutor    = "executor"
	ActorRoleSynthesizer = "synthesizer"
)

type StandingGoal struct {
	ID            int64
	Title         string
	Description   string
	Status        string
	Priority      int
	AutonomyLevel string
	RiskBudget    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type GoalSignal struct {
	ID          int64
	GoalID      int64
	WatcherID   int64
	Source      string
	Type        string
	PayloadJSON string
	Fingerprint string
	Severity    int
	Status      string
	DedupeKey   string
	ObservedAt  time.Time
}

type SignalWatcher struct {
	ID         int64
	GoalID     int64
	Source     string
	ConfigJSON string
	Enabled    bool
	IntervalS  int
	LastCursor string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type AgenticTask struct {
	ID                 int64
	GoalID             int64
	SignalID           int64
	ParentID           int64
	QueueTaskID        int64
	Title              string
	Prompt             string
	Status             string
	Priority           int
	RiskLevel          string
	AutonomyDecision   string
	ApprovalRequestID  string
	VerificationStatus string
	DueAt              sql.NullTime
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type SignalTriageResult struct {
	Task    *AgenticTask
	Created bool
	Linked  bool
	Failed  bool
}

type TaskEdge struct {
	ParentID  int64
	ChildID   int64
	EdgeType  string
	CreatedAt time.Time
}

type AgentActor struct {
	ID                int64
	TaskID            int64
	Role              string
	StateJSON         string
	InboxJSON         string
	OutboxJSON        string
	ToolAllowlistJSON string
	BudgetJSON        string
	Status            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ActorHandoff struct {
	ID          int64
	TaskID      int64
	FromActorID int64
	ToActorID   int64
	HandoffType string
	PayloadJSON string
	Status      string
	CreatedAt   time.Time
}

type PolicyDecisionRecord struct {
	ID            int64
	TaskID        int64
	ActorID       int64
	ActionKind    string
	ToolName      string
	RiskLevel     string
	Decision      string
	Reason        string
	PolicyVersion string
	CreatedAt     time.Time
}

type ToolActionReceipt struct {
	ID                 int64
	TaskID             int64
	ActorID            int64
	PolicyDecisionID   int64
	ApprovalRequestID  string
	ToolName           string
	ToolCallID         string
	InputHash          string
	OutputHash         string
	RawOutputHash      string
	VisibleOutputHash  string
	OutputSummary      string
	Status             string
	FailureReason      string
	HookProvenanceJSON string
	Reversible         bool
	StartedAt          time.Time
	CompletedAt        sql.NullTime
}

type ActivationRun struct {
	ID                int64
	ExecutionPolicy   string
	Limit             int
	FollowupProcessed int
	FollowupCreated   int
	FollowupSkipped   int
	FollowupFailed    int
	SignalProcessed   int
	SignalCreated     int
	SignalLinked      int
	SignalFailed      int
	EnqueuePerformed  bool
	ProposedTaskIDs   []int64
	Status            string
	Reason            string
	CreatedAt         time.Time
}

type VerificationRun struct {
	ID               int64
	TaskID           int64
	VerifierActorID  int64
	CriteriaJSON     string
	EvidenceRefsJSON string
	Verdict          string
	Reason           string
	CreatedAt        time.Time
}

type CompletionGate struct {
	ID                 int64
	TaskID             int64
	QueueTaskID        int64
	VerificationRunID  int64
	Status             string
	Reason             string
	ReceiptSummaryJSON string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type TaskEnqueueDecision struct {
	ID                      int64
	TaskID                  int64
	QueueTaskID             int64
	OperatorID              string
	Decision                string
	Reason                  string
	RequestedEnforcement    string
	RequestedCompletionGate string
	Status                  string
	FailureReason           string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type MemoryUpdate struct {
	ID                int64
	TaskID            int64
	ReceiptID         int64
	VerificationRunID int64
	Target            string
	Operation         string
	PayloadHash       string
	Status            string
	Source            string
	Reason            string
	CreatedAt         time.Time
	AppliedAt         sql.NullTime
}

type Followup struct {
	ID            int64
	TaskID        int64
	GoalID        int64
	Reason        string
	Status        string
	TriggerAt     time.Time
	CreatedTaskID int64
	DedupeKey     string
	FailureReason string
	ProcessedAt   sql.NullTime
	WakeAgent     bool
	CreatedAt     time.Time
}
