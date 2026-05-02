package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agentic"
	agenticenqueue "github.com/stello/elnath/internal/agentic/enqueue"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	_ "modernc.org/sqlite"
)

type agenticCLI struct {
	db    *sql.DB
	store *agentic.Store
	now   time.Time
}

type agenticStatusView struct {
	AutonomyEnabled         bool                      `json:"autonomy_enabled"`
	Counts                  map[string]map[string]int `json:"counts"`
	ProposedAwaitingEnqueue int                       `json:"proposed_awaiting_enqueue"`
	DueFollowups            int                       `json:"due_followups"`
	Attention               []agenticAttentionItem    `json:"attention"`
}

type agenticAttentionItem struct {
	Kind   string `json:"kind"`
	ID     int64  `json:"id"`
	TaskID int64  `json:"task_id,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type agenticTaskView struct {
	AutonomyEnabled    bool                     `json:"autonomy_enabled"`
	Task               agenticTaskInfo          `json:"task"`
	Goal               *agenticGoalInfo         `json:"goal,omitempty"`
	Signal             *agenticSignalInfo       `json:"signal,omitempty"`
	Queue              *agenticQueueInfo        `json:"queue,omitempty"`
	Approval           *agenticApprovalInfo     `json:"approval,omitempty"`
	Policy             *agenticPolicyInfo       `json:"policy,omitempty"`
	EnqueueEligible    bool                     `json:"enqueue_eligible"`
	EnqueueDecision    *agenticEnqueueInfo      `json:"enqueue_decision,omitempty"`
	LatestVerification *agenticVerificationInfo `json:"latest_verification,omitempty"`
	CompletionCounts   map[string]int           `json:"completion_gate_counts"`
	MemoryCounts       map[string]int           `json:"memory_counts"`
	FollowupCounts     map[string]int           `json:"followup_counts"`
	DueFollowups       int                      `json:"due_followups"`
	ActorRoleCounts    map[string]int           `json:"actor_role_counts"`
}

type agenticLineageView struct {
	AutonomyEnabled  bool                      `json:"autonomy_enabled"`
	Goal             *agenticGoalInfo          `json:"goal,omitempty"`
	Signal           *agenticSignalInfo        `json:"signal,omitempty"`
	Task             agenticTaskInfo           `json:"task"`
	Queue            *agenticQueueInfo         `json:"queue,omitempty"`
	Actors           []agenticActorInfo        `json:"actors"`
	Handoffs         []agenticHandoffInfo      `json:"handoffs"`
	PolicyDecisions  []agenticPolicyInfo       `json:"policy_decisions"`
	EnqueueDecisions []agenticEnqueueInfo      `json:"enqueue_decisions"`
	Approvals        []agenticApprovalInfo     `json:"approvals"`
	Receipts         []agenticReceiptInfo      `json:"receipts"`
	CompletionGates  []agenticCompletionInfo   `json:"completion_gates"`
	VerificationRuns []agenticVerificationInfo `json:"verification_runs"`
	MemoryUpdates    []agenticMemoryInfo       `json:"memory_updates"`
	Followups        []agenticFollowupInfo     `json:"followups"`
}

type agenticGoalInfo struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
}

type agenticSignalInfo struct {
	ID        int64  `json:"id"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Severity  int    `json:"severity"`
	DedupeKey string `json:"dedupe_key,omitempty"`
}

type agenticTaskInfo struct {
	ID                 int64  `json:"id"`
	Title              string `json:"title"`
	Status             string `json:"status"`
	GoalID             int64  `json:"goal_id,omitempty"`
	SignalID           int64  `json:"signal_id,omitempty"`
	ParentID           int64  `json:"parent_id,omitempty"`
	QueueTaskID        int64  `json:"queue_task_id,omitempty"`
	RiskLevel          string `json:"risk_level"`
	AutonomyDecision   string `json:"autonomy_decision"`
	ApprovalRequestID  string `json:"approval_request_id,omitempty"`
	VerificationStatus string `json:"verification_status"`
	DueAt              string `json:"due_at,omitempty"`
}

type agenticQueueInfo struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	SessionID string `json:"session_id,omitempty"`
}

type agenticEnqueueInfo struct {
	ID                      int64  `json:"id"`
	QueueTaskID             int64  `json:"queue_task_id,omitempty"`
	OperatorID              string `json:"operator_id,omitempty"`
	Decision                string `json:"decision"`
	Reason                  string `json:"reason,omitempty"`
	RequestedEnforcement    string `json:"requested_enforcement,omitempty"`
	RequestedCompletionGate string `json:"requested_completion_gate,omitempty"`
	Status                  string `json:"status"`
	FailureReason           string `json:"failure_reason,omitempty"`
}

type agenticActorInfo struct {
	ID     int64  `json:"id"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type agenticHandoffInfo struct {
	ID          int64  `json:"id"`
	FromActorID int64  `json:"from_actor_id"`
	FromRole    string `json:"from_role,omitempty"`
	ToActorID   int64  `json:"to_actor_id"`
	ToRole      string `json:"to_role,omitempty"`
	Type        string `json:"type"`
	Status      string `json:"status"`
}

type agenticPolicyInfo struct {
	ID         int64  `json:"id"`
	ActorID    int64  `json:"actor_id,omitempty"`
	ActionKind string `json:"action_kind"`
	ToolName   string `json:"tool_name"`
	RiskLevel  string `json:"risk_level"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason,omitempty"`
	Version    string `json:"policy_version,omitempty"`
}

type agenticApprovalInfo struct {
	ID               int64  `json:"id"`
	TaskID           int64  `json:"task_id,omitempty"`
	PolicyDecisionID int64  `json:"policy_decision_id,omitempty"`
	ToolName         string `json:"tool_name"`
	Decision         string `json:"decision"`
	RiskLevel        string `json:"risk_level,omitempty"`
	Reason           string `json:"reason,omitempty"`
	DecidedBy        string `json:"decided_by,omitempty"`
}

type agenticReceiptInfo struct {
	ID                int64  `json:"id"`
	ActorID           int64  `json:"actor_id,omitempty"`
	PolicyDecisionID  int64  `json:"policy_decision_id,omitempty"`
	ApprovalRequestID string `json:"approval_request_id,omitempty"`
	ToolName          string `json:"tool_name"`
	Status            string `json:"status"`
	ToolCallID        string `json:"tool_call_id,omitempty"`
}

type agenticCompletionInfo struct {
	ID                 int64  `json:"id"`
	QueueTaskID        int64  `json:"queue_task_id,omitempty"`
	VerificationRunID  int64  `json:"verification_run_id,omitempty"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	ReceiptSummaryJSON string `json:"receipt_summary_json,omitempty"`
}

type agenticVerificationInfo struct {
	ID              int64  `json:"id"`
	VerifierActorID int64  `json:"verifier_actor_id,omitempty"`
	Verdict         string `json:"verdict"`
	Reason          string `json:"reason,omitempty"`
}

type agenticMemoryInfo struct {
	ID                int64  `json:"id"`
	ReceiptID         int64  `json:"receipt_id,omitempty"`
	VerificationRunID int64  `json:"verification_run_id,omitempty"`
	Target            string `json:"target"`
	Operation         string `json:"operation"`
	PayloadHash       string `json:"payload_hash"`
	Status            string `json:"status"`
	Source            string `json:"source,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

type agenticFollowupInfo struct {
	ID            int64  `json:"id"`
	GoalID        int64  `json:"goal_id,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	CreatedTaskID int64  `json:"created_task_id,omitempty"`
	TriggerAt     string `json:"trigger_at"`
	WakeAgent     bool   `json:"wake_agent"`
	Due           bool   `json:"due"`
}

func cmdAgentic(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(`Usage: elnath agentic <subcommand> [flags]

Subcommands:
  status [--json]                         Read-only control-plane summary
  task <id> [--json]                      Read-only task status
  task --queue-task-id <id> [--json]      Resolve agentic task from daemon queue task
  lineage <task-id> [--json]              Read-only task lineage
  enqueue <task-id> [flags]               Explicitly enqueue an approved proposed task

Enqueue flags:
  --agentic-enforcement gateway           Request ToolGateway runtime opt-in
  --completion-gate verification          Request verifier-gated completion
  --reason <text>                         Record operator reason
  --json                                  Print JSON result`)
		return nil
	}
	if args[0] == "enqueue" {
		return cmdAgenticEnqueue(ctx, args[1:])
	}
	cli, closeFn, err := openAgenticCLI()
	if err != nil {
		return err
	}
	defer closeFn()
	switch args[0] {
	case "status":
		jsonOut := hasFlag(args[1:], "--json")
		view, err := cli.status(ctx)
		if err != nil {
			return err
		}
		if jsonOut {
			return printJSON(view)
		}
		fmt.Print(renderAgenticStatus(view))
		return nil
	case "task":
		id, jsonOut, err := cli.resolveTaskID(ctx, args[1:])
		if err != nil {
			return err
		}
		view, err := cli.task(ctx, id)
		if err != nil {
			return err
		}
		if jsonOut {
			return printJSON(view)
		}
		fmt.Print(renderAgenticTask(view))
		return nil
	case "lineage":
		id, jsonOut, err := parseAgenticIDArgs(args[1:], "elnath agentic lineage <task-id>")
		if err != nil {
			return err
		}
		view, err := cli.lineage(ctx, id)
		if err != nil {
			return err
		}
		if jsonOut {
			return printJSON(view)
		}
		fmt.Print(renderAgenticLineage(view))
		return nil
	default:
		return fmt.Errorf("unknown agentic subcommand: %s", args[0])
	}
}

type agenticEnqueueCommandResult struct {
	AutonomyEnabled bool                `json:"autonomy_enabled"`
	TaskID          int64               `json:"task_id"`
	QueueTaskID     int64               `json:"queue_task_id"`
	Status          string              `json:"status"`
	Existed         bool                `json:"existed"`
	Decision        *agenticEnqueueInfo `json:"decision,omitempty"`
}

type agenticEnqueueArgs struct {
	TaskID                  int64
	Reason                  string
	RequestedEnforcement    string
	RequestedCompletionGate string
	JSON                    bool
}

func cmdAgenticEnqueue(ctx context.Context, args []string) error {
	parsed, err := parseAgenticEnqueueArgs(args)
	if err != nil {
		return err
	}
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("agentic enqueue: load config: %w", err)
	}
	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("agentic enqueue: open db: %w", err)
	}
	defer db.Close()
	if err := agentic.InitSchema(db.Main); err != nil {
		return fmt.Errorf("agentic enqueue: init schema: %w", err)
	}
	queue, err := daemon.NewQueueNoRecover(db.Main)
	if err != nil {
		return fmt.Errorf("agentic enqueue: open queue: %w", err)
	}
	store := agentic.NewStore(db.Main)
	operatorID := strings.TrimSpace(cfg.Principal.UserID)
	if operatorID == "" {
		operatorID = "cli"
	}
	service := agenticenqueue.NewService(store, queue, agenticenqueue.Options{
		EnforcementMode:    cfg.Agentic.Enforcement.Mode,
		CompletionGateMode: cfg.Agentic.CompletionGate.Mode,
	})
	result, err := service.Enqueue(ctx, agenticenqueue.Request{
		TaskID:                  parsed.TaskID,
		OperatorID:              operatorID,
		Reason:                  parsed.Reason,
		RequestedEnforcement:    parsed.RequestedEnforcement,
		RequestedCompletionGate: parsed.RequestedCompletionGate,
	})
	if err != nil {
		return err
	}
	view := agenticEnqueueCommandResult{
		AutonomyEnabled: false,
		TaskID:          result.Task.ID,
		QueueTaskID:     result.QueueTaskID,
		Status:          result.Task.Status,
		Existed:         result.Existed,
	}
	if result.Decision != nil {
		view.Decision = enqueueInfo(*result.Decision)
	}
	if parsed.JSON {
		return printJSON(view)
	}
	if result.Existed {
		fmt.Printf("already enqueued agentic task #%d as daemon queue task #%d\n", result.Task.ID, result.QueueTaskID)
		return nil
	}
	fmt.Printf("enqueued agentic task #%d as daemon queue task #%d\n", result.Task.ID, result.QueueTaskID)
	return nil
}

func parseAgenticEnqueueArgs(args []string) (agenticEnqueueArgs, error) {
	var parsed agenticEnqueueArgs
	var ids []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			parsed.JSON = true
		case "--reason":
			i++
			if i >= len(args) {
				return parsed, fmt.Errorf("usage: elnath agentic enqueue <task-id> [--reason <text>]")
			}
			parsed.Reason = args[i]
		case "--agentic-enforcement":
			i++
			if i >= len(args) {
				return parsed, fmt.Errorf("usage: elnath agentic enqueue <task-id> [--agentic-enforcement gateway]")
			}
			parsed.RequestedEnforcement = args[i]
		case "--completion-gate":
			i++
			if i >= len(args) {
				return parsed, fmt.Errorf("usage: elnath agentic enqueue <task-id> [--completion-gate verification]")
			}
			parsed.RequestedCompletionGate = args[i]
		default:
			if strings.HasPrefix(arg, "-") {
				return parsed, fmt.Errorf("unknown enqueue flag: %s", arg)
			}
			ids = append(ids, arg)
		}
	}
	if len(ids) != 1 {
		return parsed, fmt.Errorf("usage: elnath agentic enqueue <task-id>")
	}
	id, err := strconv.ParseInt(ids[0], 10, 64)
	if err != nil {
		return parsed, fmt.Errorf("invalid agentic task ID %q: %w", ids[0], err)
	}
	parsed.TaskID = id
	return parsed, nil
}

func openAgenticCLI() (*agenticCLI, func(), error) {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("agentic: load config: %w", err)
	}
	db, err := openAgenticReadOnlyDB(cfg.DataDir)
	if err != nil {
		return nil, func() {}, fmt.Errorf("agentic: open db: %w", err)
	}
	return &agenticCLI{db: db, store: agentic.NewStore(db), now: time.Now()}, func() { _ = db.Close() }, nil
}

func openAgenticReadOnlyDB(dataDir string) (*sql.DB, error) {
	mainPath := filepath.Join(dataDir, "elnath.db")
	dsn := (&url.URL{Scheme: "file", Path: mainPath}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA query_only=ON",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=30000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("exec %q: %w", pragma, err)
		}
	}
	return db, nil
}

func (c *agenticCLI) resolveTaskID(ctx context.Context, args []string) (int64, bool, error) {
	jsonOut := hasFlag(args, "--json")
	var filtered []string
	for _, arg := range args {
		if arg != "--json" {
			filtered = append(filtered, arg)
		}
	}
	if len(filtered) == 2 && filtered[0] == "--queue-task-id" {
		queueID, err := strconv.ParseInt(filtered[1], 10, 64)
		if err != nil {
			return 0, jsonOut, fmt.Errorf("invalid queue task ID %q: %w", filtered[1], err)
		}
		task, err := c.store.GetAgenticTaskByQueueTaskID(ctx, queueID)
		if err != nil {
			return 0, jsonOut, fmt.Errorf("agentic task for queue task %d: %w", queueID, err)
		}
		return task.ID, jsonOut, nil
	}
	id, _, err := parseAgenticIDArgs(args, "elnath agentic task <id>")
	return id, jsonOut, err
}

func parseAgenticIDArgs(args []string, usage string) (int64, bool, error) {
	jsonOut := hasFlag(args, "--json")
	var ids []string
	for _, arg := range args {
		if arg == "--json" {
			continue
		}
		ids = append(ids, arg)
	}
	if len(ids) != 1 {
		return 0, jsonOut, fmt.Errorf("usage: %s", usage)
	}
	id, err := strconv.ParseInt(ids[0], 10, 64)
	if err != nil {
		return 0, jsonOut, fmt.Errorf("invalid agentic task ID %q: %w", ids[0], err)
	}
	return id, jsonOut, nil
}

func (c *agenticCLI) status(ctx context.Context) (*agenticStatusView, error) {
	counts := map[string]map[string]int{}
	specs := map[string]struct {
		table    string
		column   string
		optional bool
	}{
		"goals":            {"standing_goals", "status", false},
		"signals":          {"goal_signals", "status", false},
		"tasks":            {"agentic_tasks", "status", false},
		"approvals":        {"approval_requests", "decision", false},
		"receipts":         {"tool_action_receipts", "status", false},
		"completion_gates": {"completion_gates", "status", true},
		"enqueue":          {"task_enqueue_decisions", "status", true},
		"verification":     {"verification_runs", "verdict", false},
		"memory":           {"memory_updates", "status", false},
		"followups":        {"followups", "status", false},
		"actors":           {"agent_actors", "status", false},
	}
	for key, spec := range specs {
		if spec.optional {
			exists, err := c.tableExists(ctx, spec.table)
			if err != nil {
				return nil, err
			}
			if !exists {
				counts[key] = map[string]int{}
				continue
			}
		}
		values, err := c.countBy(ctx, spec.table, spec.column, "")
		if err != nil {
			return nil, err
		}
		counts[key] = values
	}
	due, err := c.countDueFollowups(ctx)
	if err != nil {
		return nil, err
	}
	awaitingEnqueue, err := c.countProposedAwaitingEnqueue(ctx)
	if err != nil {
		return nil, err
	}
	attention, err := c.attention(ctx)
	if err != nil {
		return nil, err
	}
	return &agenticStatusView{
		AutonomyEnabled:         false,
		Counts:                  counts,
		ProposedAwaitingEnqueue: awaitingEnqueue,
		DueFollowups:            due,
		Attention:               attention,
	}, nil
}

func (c *agenticCLI) task(ctx context.Context, id int64) (*agenticTaskView, error) {
	task, err := c.store.GetAgenticTask(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("agentic task %d: %w", id, err)
	}
	lineage, err := c.lineage(ctx, id)
	if err != nil {
		return nil, err
	}
	memoryCounts := countMemory(lineage.MemoryUpdates)
	completionCounts := countCompletionGates(lineage.CompletionGates)
	followupCounts, due := countFollowups(lineage.Followups)
	actorCounts := countActors(lineage.Actors)
	var approval *agenticApprovalInfo
	if len(lineage.Approvals) > 0 {
		approval = &lineage.Approvals[0]
	}
	var policy *agenticPolicyInfo
	if len(lineage.PolicyDecisions) > 0 {
		policy = &lineage.PolicyDecisions[0]
	}
	var latest *agenticVerificationInfo
	if len(lineage.VerificationRuns) > 0 {
		latest = &lineage.VerificationRuns[len(lineage.VerificationRuns)-1]
	}
	var enqueueDecision *agenticEnqueueInfo
	if len(lineage.EnqueueDecisions) > 0 {
		enqueueDecision = &lineage.EnqueueDecisions[len(lineage.EnqueueDecisions)-1]
	}
	return &agenticTaskView{
		AutonomyEnabled:    false,
		Task:               taskInfo(*task),
		Goal:               lineage.Goal,
		Signal:             lineage.Signal,
		Queue:              lineage.Queue,
		Approval:           approval,
		Policy:             policy,
		EnqueueEligible:    task.Status == agentic.TaskStatusProposed && task.QueueTaskID == 0,
		EnqueueDecision:    enqueueDecision,
		LatestVerification: latest,
		CompletionCounts:   completionCounts,
		MemoryCounts:       memoryCounts,
		FollowupCounts:     followupCounts,
		DueFollowups:       due,
		ActorRoleCounts:    actorCounts,
	}, nil
}

func (c *agenticCLI) lineage(ctx context.Context, id int64) (*agenticLineageView, error) {
	task, err := c.store.GetAgenticTask(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("agentic task %d: %w", id, err)
	}
	view := &agenticLineageView{
		AutonomyEnabled: false,
		Task:            taskInfo(*task),
	}
	if task.GoalID != 0 {
		goal, err := c.store.GetStandingGoal(ctx, task.GoalID)
		if err != nil {
			return nil, fmt.Errorf("agentic goal %d: %w", task.GoalID, err)
		}
		view.Goal = &agenticGoalInfo{ID: goal.ID, Title: goal.Title, Status: goal.Status, Priority: goal.Priority}
	}
	if task.SignalID != 0 {
		signal, err := c.store.GetGoalSignal(ctx, task.SignalID)
		if err != nil {
			return nil, fmt.Errorf("agentic signal %d: %w", task.SignalID, err)
		}
		view.Signal = &agenticSignalInfo{ID: signal.ID, Source: signal.Source, Type: signal.Type, Status: signal.Status, Severity: signal.Severity, DedupeKey: signal.DedupeKey}
	}
	if task.QueueTaskID != 0 {
		queue, err := c.queueInfo(ctx, task.QueueTaskID)
		if err != nil {
			return nil, err
		}
		view.Queue = queue
	}
	actors, err := c.store.ListAgentActorsByTask(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	roleByActor := map[int64]string{}
	for _, actor := range actors {
		view.Actors = append(view.Actors, agenticActorInfo{ID: actor.ID, Role: actor.Role, Status: actor.Status})
		roleByActor[actor.ID] = actor.Role
	}
	sort.Slice(view.Actors, func(i, j int) bool { return view.Actors[i].ID < view.Actors[j].ID })
	handoffs, err := c.store.ListActorHandoffsByTask(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	for _, handoff := range handoffs {
		view.Handoffs = append(view.Handoffs, agenticHandoffInfo{
			ID:          handoff.ID,
			FromActorID: handoff.FromActorID,
			FromRole:    roleByActor[handoff.FromActorID],
			ToActorID:   handoff.ToActorID,
			ToRole:      roleByActor[handoff.ToActorID],
			Type:        handoff.HandoffType,
			Status:      handoff.Status,
		})
	}
	policies, err := c.policyDecisions(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	view.PolicyDecisions = policies
	enqueues, err := c.taskEnqueueDecisions(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	view.EnqueueDecisions = enqueues
	approvals, err := c.approvals(ctx, task.ID, task.ApprovalRequestID)
	if err != nil {
		return nil, err
	}
	view.Approvals = approvals
	receipts, err := c.receipts(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	view.Receipts = receipts
	gates, err := c.completionGates(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	for _, gate := range gates {
		view.CompletionGates = append(view.CompletionGates, agenticCompletionInfo{
			ID:                 gate.ID,
			QueueTaskID:        gate.QueueTaskID,
			VerificationRunID:  gate.VerificationRunID,
			Status:             gate.Status,
			Reason:             bounded(gate.Reason, 120),
			ReceiptSummaryJSON: bounded(gate.ReceiptSummaryJSON, 120),
		})
	}
	runs, err := c.store.ListVerificationRunsByTask(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		view.VerificationRuns = append(view.VerificationRuns, agenticVerificationInfo{ID: run.ID, VerifierActorID: run.VerifierActorID, Verdict: run.Verdict, Reason: bounded(run.Reason, 120)})
	}
	updates, err := c.store.ListMemoryUpdatesByTask(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	for _, update := range updates {
		view.MemoryUpdates = append(view.MemoryUpdates, agenticMemoryInfo{
			ID:                update.ID,
			ReceiptID:         update.ReceiptID,
			VerificationRunID: update.VerificationRunID,
			Target:            update.Target,
			Operation:         update.Operation,
			PayloadHash:       update.PayloadHash,
			Status:            update.Status,
			Source:            update.Source,
			Reason:            bounded(update.Reason, 120),
		})
	}
	followups, err := c.followups(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	view.Followups = followups
	return view, nil
}

func (c *agenticCLI) tableExists(ctx context.Context, table string) (bool, error) {
	var name string
	err := c.db.QueryRowContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("agentic: inspect table %s: %w", table, err)
	}
	return true, nil
}

func (c *agenticCLI) completionGates(ctx context.Context, taskID int64) ([]agentic.CompletionGate, error) {
	exists, err := c.tableExists(ctx, "completion_gates")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	return c.store.ListCompletionGatesByTask(ctx, taskID)
}

func (c *agenticCLI) taskEnqueueDecisions(ctx context.Context, taskID int64) ([]agenticEnqueueInfo, error) {
	exists, err := c.tableExists(ctx, "task_enqueue_decisions")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	decisions, err := c.store.ListTaskEnqueueDecisionsByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]agenticEnqueueInfo, 0, len(decisions))
	for _, decision := range decisions {
		info := enqueueInfo(decision)
		out = append(out, *info)
	}
	return out, nil
}

func enqueueInfo(decision agentic.TaskEnqueueDecision) *agenticEnqueueInfo {
	return &agenticEnqueueInfo{
		ID:                      decision.ID,
		QueueTaskID:             decision.QueueTaskID,
		OperatorID:              bounded(decision.OperatorID, 80),
		Decision:                decision.Decision,
		Reason:                  bounded(decision.Reason, 120),
		RequestedEnforcement:    decision.RequestedEnforcement,
		RequestedCompletionGate: decision.RequestedCompletionGate,
		Status:                  decision.Status,
		FailureReason:           bounded(decision.FailureReason, 120),
	}
}

func taskInfo(task agentic.AgenticTask) agenticTaskInfo {
	info := agenticTaskInfo{
		ID:                 task.ID,
		Title:              task.Title,
		Status:             task.Status,
		GoalID:             task.GoalID,
		SignalID:           task.SignalID,
		ParentID:           task.ParentID,
		QueueTaskID:        task.QueueTaskID,
		RiskLevel:          task.RiskLevel,
		AutonomyDecision:   task.AutonomyDecision,
		ApprovalRequestID:  task.ApprovalRequestID,
		VerificationStatus: task.VerificationStatus,
	}
	if task.DueAt.Valid {
		info.DueAt = task.DueAt.Time.Format(time.RFC3339)
	}
	return info
}

func (c *agenticCLI) countBy(ctx context.Context, table, column, where string, args ...any) (map[string]int, error) {
	query := fmt.Sprintf("SELECT %s, COUNT(*) FROM %s", column, table)
	if where != "" {
		query += " WHERE " + where
	}
	query += fmt.Sprintf(" GROUP BY %s ORDER BY %s", column, column)
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("agentic: count %s.%s: %w", table, column, err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[status] = count
	}
	return out, rows.Err()
}

func (c *agenticCLI) countDueFollowups(ctx context.Context) (int, error) {
	var count int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM followups WHERE status IN (?, ?) AND trigger_at <= ?`, agentic.FollowupStatusPending, agentic.FollowupStatusProcessing, c.now.UnixMilli()).Scan(&count)
	return count, err
}

func (c *agenticCLI) countProposedAwaitingEnqueue(ctx context.Context) (int, error) {
	var count int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agentic_tasks WHERE status = ? AND queue_task_id IS NULL`, agentic.TaskStatusProposed).Scan(&count)
	return count, err
}

func (c *agenticCLI) attention(ctx context.Context) ([]agenticAttentionItem, error) {
	var out []agenticAttentionItem
	addRows := func(kind, query string, args ...any) error {
		rows, err := c.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item agenticAttentionItem
			var reason sql.NullString
			if err := rows.Scan(&item.ID, &item.TaskID, &item.Status, &reason); err != nil {
				return err
			}
			item.Kind = kind
			if kind == "followup" {
				item.Status = "due"
			}
			item.Reason = bounded(reason.String, 120)
			out = append(out, item)
		}
		return rows.Err()
	}
	if err := addRows("approval", `SELECT id, COALESCE(task_id, 0), decision, reason FROM approval_requests WHERE decision = 'pending' ORDER BY id LIMIT 10`); err != nil {
		return nil, err
	}
	if err := addRows("receipt", `SELECT id, task_id, status, '' FROM tool_action_receipts WHERE status IN (?, ?) ORDER BY id LIMIT 10`, agentic.ReceiptStatusDenied, agentic.ReceiptStatusFailed); err != nil {
		return nil, err
	}
	if err := addRows("verification", `SELECT id, task_id, verdict, reason FROM verification_runs WHERE verdict IN (?, ?) ORDER BY id LIMIT 10`, agentic.VerificationVerdictFailed, agentic.VerificationVerdictInconclusive); err != nil {
		return nil, err
	}
	if err := addRows("memory", `SELECT id, task_id, status, reason FROM memory_updates WHERE status IN (?, ?) ORDER BY id LIMIT 10`, agentic.MemoryUpdateStatusBlocked, agentic.MemoryUpdateStatusFailed); err != nil {
		return nil, err
	}
	if err := addRows("followup", `SELECT id, COALESCE(task_id, 0), status, reason FROM followups WHERE status IN (?, ?) AND trigger_at <= ? ORDER BY trigger_at, id LIMIT 10`, agentic.FollowupStatusPending, agentic.FollowupStatusProcessing, c.now.UnixMilli()); err != nil {
		return nil, err
	}
	exists, err := c.tableExists(ctx, "task_enqueue_decisions")
	if err != nil {
		return nil, err
	}
	if exists {
		if err := addRows("enqueue", `SELECT id, task_id, status, failure_reason FROM task_enqueue_decisions WHERE status = ? ORDER BY id LIMIT 10`, agentic.TaskEnqueueStatusFailed); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *agenticCLI) queueInfo(ctx context.Context, id int64) (*agenticQueueInfo, error) {
	var q agenticQueueInfo
	var session string
	err := c.db.QueryRowContext(ctx, `SELECT id, status, session_id FROM task_queue WHERE id = ?`, id).Scan(&q.ID, &q.Status, &session)
	if err != nil {
		return nil, fmt.Errorf("queue task %d: %w", id, err)
	}
	q.SessionID = bounded(session, 80)
	return &q, nil
}

func (c *agenticCLI) policyDecisions(ctx context.Context, taskID int64) ([]agenticPolicyInfo, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, COALESCE(actor_id, 0), action_kind, tool_name, risk_level, decision, reason, policy_version
		FROM policy_decisions
		WHERE task_id = ?
		ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agenticPolicyInfo
	for rows.Next() {
		var p agenticPolicyInfo
		if err := rows.Scan(&p.ID, &p.ActorID, &p.ActionKind, &p.ToolName, &p.RiskLevel, &p.Decision, &p.Reason, &p.Version); err != nil {
			return nil, err
		}
		p.Reason = bounded(p.Reason, 120)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (c *agenticCLI) approvals(ctx context.Context, taskID int64, approvalRequestID string) ([]agenticApprovalInfo, error) {
	query := `
		SELECT id, COALESCE(task_id, 0), COALESCE(policy_decision_id, 0), tool_name, decision, risk_level, reason, decided_by
		FROM approval_requests
		WHERE task_id = ?`
	args := []any{taskID}
	if approvalRequestID != "" {
		query += ` OR id = ?`
		args = append(args, approvalRequestID)
	}
	query += ` ORDER BY id`
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agenticApprovalInfo
	for rows.Next() {
		var a agenticApprovalInfo
		if err := rows.Scan(&a.ID, &a.TaskID, &a.PolicyDecisionID, &a.ToolName, &a.Decision, &a.RiskLevel, &a.Reason, &a.DecidedBy); err != nil {
			return nil, err
		}
		a.Reason = bounded(a.Reason, 120)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (c *agenticCLI) receipts(ctx context.Context, taskID int64) ([]agenticReceiptInfo, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, COALESCE(actor_id, 0), COALESCE(policy_decision_id, 0), approval_request_id, tool_name, tool_call_id, status
		FROM tool_action_receipts
		WHERE task_id = ?
		ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agenticReceiptInfo
	for rows.Next() {
		var r agenticReceiptInfo
		if err := rows.Scan(&r.ID, &r.ActorID, &r.PolicyDecisionID, &r.ApprovalRequestID, &r.ToolName, &r.ToolCallID, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (c *agenticCLI) followups(ctx context.Context, taskID int64) ([]agenticFollowupInfo, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, COALESCE(goal_id, 0), reason, status, trigger_at, COALESCE(created_task_id, 0), wake_agent
		FROM followups
		WHERE task_id = ? OR created_task_id = ?
		ORDER BY id`, taskID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agenticFollowupInfo
	for rows.Next() {
		var f agenticFollowupInfo
		var triggerAt int64
		var wake int
		if err := rows.Scan(&f.ID, &f.GoalID, &f.Reason, &f.Status, &triggerAt, &f.CreatedTaskID, &wake); err != nil {
			return nil, err
		}
		f.Reason = bounded(f.Reason, 120)
		f.TriggerAt = time.UnixMilli(triggerAt).Format(time.RFC3339)
		f.WakeAgent = wake != 0
		f.Due = (f.Status == agentic.FollowupStatusPending || f.Status == agentic.FollowupStatusProcessing) && triggerAt <= c.now.UnixMilli()
		out = append(out, f)
	}
	return out, rows.Err()
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func renderAgenticStatus(view *agenticStatusView) string {
	var b strings.Builder
	fmt.Fprintln(&b, "Agentic Control Plane")
	fmt.Fprintf(&b, "  autonomy_enabled: %t\n", view.AutonomyEnabled)
	fmt.Fprintf(&b, "  proposed_awaiting_enqueue: %d\n", view.ProposedAwaitingEnqueue)
	for _, key := range []string{"goals", "signals", "tasks", "approvals", "receipts", "completion_gates", "enqueue", "verification", "memory", "followups", "actors"} {
		line := formatCounts(view.Counts[key])
		if key == "followups" {
			line = strings.TrimSpace(line + fmt.Sprintf(" due=%d", view.DueFollowups))
		}
		fmt.Fprintf(&b, "  %s: %s\n", key, noneIfEmpty(line))
	}
	if len(view.Attention) == 0 {
		fmt.Fprintln(&b, "\nAttention:\n  none")
		return b.String()
	}
	fmt.Fprintln(&b, "\nAttention:")
	for _, item := range view.Attention {
		reason := ""
		if item.Reason != "" {
			reason = ": " + item.Reason
		}
		if item.TaskID != 0 {
			fmt.Fprintf(&b, "  - %s #%d %s task #%d%s\n", item.Kind, item.ID, item.Status, item.TaskID, reason)
		} else {
			fmt.Fprintf(&b, "  - %s #%d %s%s\n", item.Kind, item.ID, item.Status, reason)
		}
	}
	return b.String()
}

func renderAgenticTask(view *agenticTaskView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agentic Task #%d\n", view.Task.ID)
	fmt.Fprintf(&b, "  title: %s\n", view.Task.Title)
	fmt.Fprintf(&b, "  status: %s\n", view.Task.Status)
	if view.Goal != nil {
		fmt.Fprintf(&b, "  goal: #%d %s\n", view.Goal.ID, view.Goal.Title)
	} else {
		fmt.Fprintln(&b, "  goal: none")
	}
	if view.Signal != nil {
		fmt.Fprintf(&b, "  signal: #%d %s/%s %s\n", view.Signal.ID, view.Signal.Source, view.Signal.Type, view.Signal.Status)
	} else {
		fmt.Fprintln(&b, "  signal: none")
	}
	if view.Queue != nil {
		fmt.Fprintf(&b, "  queue_task_id: %d\n", view.Queue.ID)
	} else {
		fmt.Fprintln(&b, "  queue_task_id: none")
	}
	fmt.Fprintf(&b, "  enqueue_eligible: %t\n", view.EnqueueEligible)
	if view.EnqueueDecision != nil {
		fmt.Fprintf(&b, "  enqueue_decision: #%d %s %s queue_task_id=%s reason=%s\n", view.EnqueueDecision.ID, view.EnqueueDecision.Decision, view.EnqueueDecision.Status, intOrNone(view.EnqueueDecision.QueueTaskID), noneIfEmpty(view.EnqueueDecision.Reason))
	} else {
		fmt.Fprintln(&b, "  enqueue_decision: none")
	}
	fmt.Fprintf(&b, "  parent_id: %s\n", intOrNone(view.Task.ParentID))
	fmt.Fprintf(&b, "  due_at: %s\n", noneIfEmpty(view.Task.DueAt))
	if view.Approval != nil {
		policyText := ""
		if view.Policy != nil {
			policyText = fmt.Sprintf(" (policy #%d %s risk=%s)", view.Policy.ID, view.Policy.Decision, view.Policy.RiskLevel)
		}
		fmt.Fprintf(&b, "  approval: #%d %s%s\n", view.Approval.ID, view.Approval.Decision, policyText)
	} else {
		fmt.Fprintln(&b, "  approval: none")
	}
	if view.LatestVerification != nil {
		reason := ""
		if view.LatestVerification.Reason != "" {
			reason = " - " + view.LatestVerification.Reason
		}
		fmt.Fprintf(&b, "  latest verification: #%d %s%s\n", view.LatestVerification.ID, view.LatestVerification.Verdict, reason)
	} else {
		fmt.Fprintln(&b, "  latest verification: none")
	}
	fmt.Fprintf(&b, "  completion_gates: %s\n", noneIfEmpty(formatCounts(view.CompletionCounts)))
	fmt.Fprintf(&b, "  memory: %s\n", noneIfEmpty(formatCounts(view.MemoryCounts)))
	followups := formatCounts(view.FollowupCounts)
	if view.DueFollowups > 0 {
		followups = strings.TrimSpace(followups + fmt.Sprintf(" due=%d", view.DueFollowups))
	}
	fmt.Fprintf(&b, "  followups: %s\n", noneIfEmpty(followups))
	fmt.Fprintf(&b, "  actors: %s\n", noneIfEmpty(formatCounts(view.ActorRoleCounts)))
	return b.String()
}

func renderAgenticLineage(view *agenticLineageView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Lineage for Agentic Task #%d\n\n", view.Task.ID)
	fmt.Fprintln(&b, "Goal")
	if view.Goal == nil {
		fmt.Fprintln(&b, "  none")
	} else {
		fmt.Fprintf(&b, "  #%d %s priority=%d %s\n", view.Goal.ID, view.Goal.Status, view.Goal.Priority, view.Goal.Title)
	}
	fmt.Fprintln(&b, "\nSignal")
	if view.Signal == nil {
		fmt.Fprintln(&b, "  none")
	} else {
		fmt.Fprintf(&b, "  #%d %s/%s status=%s severity=%d dedupe=%s\n", view.Signal.ID, view.Signal.Source, view.Signal.Type, view.Signal.Status, view.Signal.Severity, noneIfEmpty(view.Signal.DedupeKey))
	}
	fmt.Fprintln(&b, "\nTask")
	fmt.Fprintf(&b, "  #%d %s risk=%s autonomy=%s\n", view.Task.ID, view.Task.Status, view.Task.RiskLevel, view.Task.AutonomyDecision)
	fmt.Fprintln(&b, "\nQueue")
	if view.Queue == nil {
		fmt.Fprintln(&b, "  none")
	} else {
		fmt.Fprintf(&b, "  queue_task_id: %d status=%s session=%s\n", view.Queue.ID, view.Queue.Status, noneIfEmpty(view.Queue.SessionID))
	}
	fmt.Fprintln(&b, "\nEnqueue")
	if len(view.EnqueueDecisions) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, decision := range view.EnqueueDecisions {
			fmt.Fprintf(&b, "  #%d %s %s queue_task_id=%s enforcement=%s completion_gate=%s reason=%s\n", decision.ID, decision.Decision, decision.Status, intOrNone(decision.QueueTaskID), noneIfEmpty(decision.RequestedEnforcement), noneIfEmpty(decision.RequestedCompletionGate), noneIfEmpty(decision.Reason))
			if decision.FailureReason != "" {
				fmt.Fprintf(&b, "    failure: %s\n", decision.FailureReason)
			}
		}
	}
	fmt.Fprintln(&b, "\nActors")
	if len(view.Actors) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, actor := range view.Actors {
			fmt.Fprintf(&b, "  #%d %s %s\n", actor.ID, actor.Role, actor.Status)
		}
	}
	fmt.Fprintln(&b, "\nHandoffs")
	if len(view.Handoffs) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, handoff := range view.Handoffs {
			fmt.Fprintf(&b, "  #%d %s -> %s %s %s\n", handoff.ID, noneIfEmpty(handoff.FromRole), noneIfEmpty(handoff.ToRole), handoff.Type, handoff.Status)
		}
	}
	fmt.Fprintln(&b, "\nPolicy decisions")
	if len(view.PolicyDecisions) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, policy := range view.PolicyDecisions {
			fmt.Fprintf(&b, "  #%d %s risk=%s tool=%s reason=%s\n", policy.ID, policy.Decision, policy.RiskLevel, policy.ToolName, noneIfEmpty(policy.Reason))
		}
	}
	fmt.Fprintln(&b, "\nApprovals")
	if len(view.Approvals) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, approval := range view.Approvals {
			fmt.Fprintf(&b, "  #%d %s tool=%s risk=%s reason=%s\n", approval.ID, approval.Decision, approval.ToolName, noneIfEmpty(approval.RiskLevel), noneIfEmpty(approval.Reason))
		}
	}
	fmt.Fprintln(&b, "\nReceipts")
	if len(view.Receipts) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, receipt := range view.Receipts {
			fmt.Fprintf(&b, "  #%d %s tool=%s approval=%s\n", receipt.ID, receipt.Status, receipt.ToolName, noneIfEmpty(receipt.ApprovalRequestID))
		}
	}
	fmt.Fprintln(&b, "\nCompletion gates")
	if len(view.CompletionGates) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, gate := range view.CompletionGates {
			fmt.Fprintf(&b, "  #%d %s verifier=%s reason=%s\n", gate.ID, gate.Status, hashIDOrNone(gate.VerificationRunID), noneIfEmpty(gate.Reason))
		}
	}
	fmt.Fprintln(&b, "\nVerification")
	if len(view.VerificationRuns) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, run := range view.VerificationRuns {
			fmt.Fprintf(&b, "  #%d %s reason=%s\n", run.ID, run.Verdict, noneIfEmpty(run.Reason))
		}
	}
	fmt.Fprintln(&b, "\nMemory")
	if len(view.MemoryUpdates) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, update := range view.MemoryUpdates {
			fmt.Fprintf(&b, "  #%d %s target=%s operation=%s reason=%s\n", update.ID, update.Status, update.Target, update.Operation, noneIfEmpty(update.Reason))
		}
	}
	fmt.Fprintln(&b, "\nFollowups")
	if len(view.Followups) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, followup := range view.Followups {
			fmt.Fprintf(&b, "  #%d %s trigger_at=%s due=%t reason=%s\n", followup.ID, followup.Status, followup.TriggerAt, followup.Due, noneIfEmpty(followup.Reason))
		}
	}
	return b.String()
}

func formatCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}

func countMemory(updates []agenticMemoryInfo) map[string]int {
	out := map[string]int{}
	for _, update := range updates {
		out[update.Status]++
	}
	return out
}

func countCompletionGates(gates []agenticCompletionInfo) map[string]int {
	out := map[string]int{}
	for _, gate := range gates {
		out[gate.Status]++
	}
	return out
}

func countFollowups(followups []agenticFollowupInfo) (map[string]int, int) {
	out := map[string]int{}
	var due int
	for _, followup := range followups {
		out[followup.Status]++
		if followup.Due {
			due++
		}
	}
	return out, due
}

func countActors(actors []agenticActorInfo) map[string]int {
	out := map[string]int{}
	for _, actor := range actors {
		out[actor.Role]++
	}
	return out
}

func noneIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "none"
	}
	return value
}

func intOrNone(value int64) string {
	if value == 0 {
		return "none"
	}
	return strconv.FormatInt(value, 10)
}

func hashIDOrNone(value int64) string {
	if value == 0 {
		return "none"
	}
	return "#" + strconv.FormatInt(value, 10)
}

func bounded(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}
