package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/agentic"
	agenticactivation "github.com/stello/elnath/internal/agentic/activation"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/onboarding"
)

type agenticCommandFixture struct {
	cfgPath    string
	db         *core.DB
	store      *agentic.Store
	queue      *daemon.Queue
	approvals  *daemon.ApprovalStore
	goal       *agentic.StandingGoal
	signal     *agentic.GoalSignal
	task       *agentic.AgenticTask
	queueTask  int64
	policy     *agentic.PolicyDecisionRecord
	approval   *daemon.ApprovalRequest
	receipt    *agentic.ToolActionReceipt
	verifier   *agentic.VerificationRun
	gate       *agentic.CompletionGate
	memory     *agentic.MemoryUpdate
	followup   *agentic.Followup
	planner    *agentic.AgentActor
	executor   *agentic.AgentActor
	handoff    *agentic.ActorHandoff
	rawSecrets []string
}

func newAgenticCommandFixture(t *testing.T) *agenticCommandFixture {
	t.Helper()
	ctx := context.Background()
	cfgPath := writeTestConfig(t, onboarding.En)
	withArgs(t, []string{"elnath", "--config", cfgPath})
	resetLoadLocaleCache()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.MkdirAll(cfg.WikiDir, 0o755); err != nil {
		t.Fatalf("mkdir wiki dir: %v", err)
	}
	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := agentic.InitSchema(db.Main); err != nil {
		t.Fatalf("agentic schema: %v", err)
	}
	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	approvals, err := daemon.NewApprovalStore(db.Main)
	if err != nil {
		t.Fatalf("approvals: %v", err)
	}
	store := agentic.NewStore(db.Main)
	now := time.Unix(1714478400, 0)
	goal, err := store.CreateStandingGoal(ctx, agentic.StandingGoal{
		Title:         "Ship operator lineage",
		Description:   "Expose read-only lineage",
		Status:        agentic.GoalStatusActive,
		Priority:      5,
		AutonomyLevel: agentic.AutonomyLevelObserve,
		RiskBudget:    "low",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	if err != nil {
		t.Fatalf("goal: %v", err)
	}
	watcher, err := store.CreateSignalWatcher(ctx, agentic.SignalWatcher{
		GoalID:     goal.ID,
		Source:     "scheduler",
		ConfigJSON: `{"surface":"test"}`,
		Enabled:    true,
		IntervalS:  60,
		LastCursor: "cursor-1",
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("watcher: %v", err)
	}
	queueTaskID, _, err := queue.Enqueue(ctx, "queue raw prompt RAW_QUEUE_PROMPT_DO_NOT_LEAK", "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := db.Main.Exec(`UPDATE task_queue SET summary = ? WHERE id = ?`, "RAW_QUEUE_SUMMARY_DO_NOT_LEAK", queueTaskID); err != nil {
		t.Fatalf("seed queue summary: %v", err)
	}
	signal, err := store.CreateGoalSignal(ctx, agentic.GoalSignal{
		GoalID:      goal.ID,
		WatcherID:   watcher.ID,
		Source:      "scheduler",
		Type:        "scheduled_task",
		PayloadJSON: `{"queue_task_id":` + fmt.Sprint(queueTaskID) + `,"raw":"RAW_SIGNAL_PAYLOAD_DO_NOT_LEAK"}`,
		Fingerprint: "signal-fingerprint",
		Severity:    2,
		Status:      agentic.SignalStatusTriaged,
		DedupeKey:   "dedupe-operator-lineage",
		ObservedAt:  now,
	})
	if err != nil {
		t.Fatalf("signal: %v", err)
	}
	task, err := store.CreateAgenticTask(ctx, agentic.AgenticTask{
		GoalID:             goal.ID,
		SignalID:           signal.ID,
		QueueTaskID:        queueTaskID,
		Title:              "Investigate blocked receipt",
		Prompt:             "RAW_TASK_PROMPT_DO_NOT_LEAK",
		Status:             agentic.TaskStatusProposed,
		Priority:           3,
		RiskLevel:          agentic.RiskLevelHigh,
		AutonomyDecision:   agentic.PolicyDecisionApprovalRequired,
		VerificationStatus: agentic.VerificationStatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
		DueAt:              sql.NullTime{Time: now.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	planner, err := store.CreateAgentActor(ctx, agentic.AgentActor{
		TaskID:     task.ID,
		Role:       agentic.ActorRolePlanner,
		StateJSON:  `{"summary":"planned"}`,
		InboxJSON:  `[]`,
		OutboxJSON: `["subtask"]`,
		BudgetJSON: `{"depth":1}`,
		Status:     agentic.ActorStatusSucceeded,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("planner: %v", err)
	}
	executor, err := store.CreateAgentActor(ctx, agentic.AgentActor{
		TaskID:     task.ID,
		Role:       agentic.ActorRoleExecutor,
		StateJSON:  `{"summary":"executed"}`,
		InboxJSON:  `["subtask"]`,
		OutboxJSON: `["result"]`,
		BudgetJSON: `{"depth":1}`,
		Status:     agentic.ActorStatusFailed,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	handoff, err := store.CreateActorHandoff(ctx, agentic.ActorHandoff{
		TaskID:      task.ID,
		FromActorID: planner.ID,
		ToActorID:   executor.ID,
		HandoffType: "planned_subtask",
		PayloadJSON: `{"summary":"bounded"}`,
		Status:      "succeeded",
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	policy, err := store.CreatePolicyDecision(ctx, agentic.PolicyDecisionRecord{
		TaskID:        task.ID,
		ActorID:       executor.ID,
		ActionKind:    "mutating",
		ToolName:      "bash",
		RiskLevel:     agentic.RiskLevelHigh,
		Decision:      agentic.PolicyDecisionApprovalRequired,
		Reason:        "shell command requires approval",
		PolicyVersion: "agentic-policy-v1",
		CreatedAt:     now,
	})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	approval, err := approvals.CreateWithContext(ctx, daemon.ApprovalCreateRequest{
		ToolName:         "bash",
		Input:            json.RawMessage(`{"cmd":"RAW_APPROVAL_INPUT_DO_NOT_LEAK"}`),
		TaskID:           task.ID,
		ActorID:          executor.ID,
		PolicyDecisionID: policy.ID,
		ActionKind:       "mutating",
		RiskLevel:        agentic.RiskLevelHigh,
		Reason:           "shell command requires approval",
		PolicyVersion:    "agentic-policy-v1",
	})
	if err != nil {
		t.Fatalf("approval: %v", err)
	}
	if _, err := store.SetAgenticTaskApprovalRequestID(ctx, task.ID, approval.IDString()); err != nil {
		t.Fatalf("link approval: %v", err)
	}
	task, err = store.GetAgenticTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	receipt, err := store.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:            task.ID,
		ActorID:           executor.ID,
		PolicyDecisionID:  policy.ID,
		ApprovalRequestID: approval.IDString(),
		ToolName:          "bash",
		ToolCallID:        "tool-call-1",
		InputHash:         "input-hash",
		OutputHash:        "output-hash",
		RawOutputHash:     "raw-output-hash",
		VisibleOutputHash: "visible-output-hash",
		OutputSummary:     "RAW_RECEIPT_SUMMARY_DO_NOT_LEAK",
		Status:            agentic.ReceiptStatusFailed,
		FailureReason:     "RAW_RECEIPT_FAILURE_DO_NOT_LEAK",
		StartedAt:         now,
	})
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	verifier, err := store.CreateVerificationRun(ctx, agentic.VerificationRun{
		TaskID:           task.ID,
		VerifierActorID:  planner.ID,
		CriteriaJSON:     `{"criteria":"bounded"}`,
		EvidenceRefsJSON: `["receipt:` + fmt.Sprint(receipt.ID) + `"]`,
		Verdict:          agentic.VerificationVerdictFailed,
		Reason:           "verification failed bounded reason",
		CreatedAt:        now,
	})
	if err != nil {
		t.Fatalf("verification: %v", err)
	}
	gate, err := store.CreateCompletionGate(ctx, agentic.CompletionGate{
		TaskID:             task.ID,
		QueueTaskID:        queueTaskID,
		VerificationRunID:  verifier.ID,
		Status:             agentic.CompletionGateStatusBlocked,
		Reason:             "verification failed bounded reason",
		ReceiptSummaryJSON: `{"started":0,"failed":1}`,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("completion gate: %v", err)
	}
	memory, err := store.CreateMemoryUpdate(ctx, agentic.MemoryUpdate{
		TaskID:            task.ID,
		ReceiptID:         receipt.ID,
		VerificationRunID: verifier.ID,
		Target:            "wiki",
		Operation:         "write",
		PayloadHash:       "memory-payload-hash",
		Status:            agentic.MemoryUpdateStatusBlocked,
		Source:            "agentic",
		Reason:            "latest verification failed",
		CreatedAt:         now,
	})
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	followup, err := store.CreateFollowup(ctx, agentic.Followup{
		TaskID:    task.ID,
		GoalID:    goal.ID,
		Reason:    "check again later",
		Status:    agentic.FollowupStatusPending,
		TriggerAt: time.Now().Add(-time.Hour),
		WakeAgent: true,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("followup: %v", err)
	}
	return &agenticCommandFixture{
		cfgPath:   cfgPath,
		db:        db,
		store:     store,
		queue:     queue,
		approvals: approvals,
		goal:      goal,
		signal:    signal,
		task:      task,
		queueTask: queueTaskID,
		policy:    policy,
		approval:  approval,
		receipt:   receipt,
		verifier:  verifier,
		gate:      gate,
		memory:    memory,
		followup:  followup,
		planner:   planner,
		executor:  executor,
		handoff:   handoff,
		rawSecrets: []string{
			"RAW_QUEUE_PROMPT_DO_NOT_LEAK",
			"RAW_SIGNAL_PAYLOAD_DO_NOT_LEAK",
			"RAW_TASK_PROMPT_DO_NOT_LEAK",
			"RAW_APPROVAL_INPUT_DO_NOT_LEAK",
			"RAW_QUEUE_SUMMARY_DO_NOT_LEAK",
			"RAW_RECEIPT_SUMMARY_DO_NOT_LEAK",
			"RAW_RECEIPT_FAILURE_DO_NOT_LEAK",
		},
	}
}

func TestAgenticCommand_ReadOnlyDBRejectsWrites(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	cfg, err := config.Load(fx.cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	db, err := openAgenticReadOnlyDB(cfg.DataDir)
	if err != nil {
		t.Fatalf("open read-only db: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO standing_goals(title, description, status, priority, autonomy_level, risk_budget, created_at, updated_at) VALUES ('bad', '', 'active', 0, 'observe', '', 1, 1)`)
	if err == nil {
		t.Fatal("read-only agentic DB accepted INSERT")
	}
}

func TestProposedTaskEnqueue_DefaultProposedTaskDoesNotEnqueue(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	task := createStandaloneProposedTask(t, fx)
	before := countQueueRows(t, fx.db.Main)

	if _, err := fx.store.GetAgenticTask(context.Background(), task.ID); err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}

	afterTask, err := fx.store.GetAgenticTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask after no action: %v", err)
	}
	if afterTask.QueueTaskID != 0 || afterTask.Status != agentic.TaskStatusProposed {
		t.Fatalf("task changed without explicit enqueue: %+v", afterTask)
	}
	if after := countQueueRows(t, fx.db.Main); after != before {
		t.Fatalf("queue rows changed without explicit enqueue: before=%d after=%d", before, after)
	}
}

func TestProposedTaskEnqueue_CreatesOneDaemonQueueTask(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	task := createStandaloneProposedTask(t, fx)
	before := countQueueRows(t, fx.db.Main)

	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID), "--reason", "operator approved"}); err != nil {
			t.Fatalf("cmdAgentic enqueue: %v", err)
		}
	})
	if !strings.Contains(stdout, "enqueued agentic task") {
		t.Fatalf("enqueue output = %q, want success summary", stdout)
	}

	updated, err := fx.store.GetAgenticTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	if updated.QueueTaskID == 0 || updated.Status != agentic.TaskStatusPending {
		t.Fatalf("updated task = %+v, want queue link and pending status", updated)
	}
	if after := countQueueRows(t, fx.db.Main); after != before+1 {
		t.Fatalf("queue rows = %d, want %d", after, before+1)
	}
	queued, err := fx.queue.Get(context.Background(), updated.QueueTaskID)
	if err != nil {
		t.Fatalf("queue get: %v", err)
	}
	payload := daemon.ParseTaskPayload(queued.Payload)
	if payload.Prompt != task.Prompt {
		t.Fatalf("queue payload prompt = %q, want %q", payload.Prompt, task.Prompt)
	}
	if payload.AgenticEnforcement != "" || payload.AgenticCompletionGate != "" {
		t.Fatalf("default runtime modes = enforcement %q completion %q, want observe defaults", payload.AgenticEnforcement, payload.AgenticCompletionGate)
	}
}

func TestProposedTaskEnqueue_JSONOutputStable(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	task := createStandaloneProposedTask(t, fx)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID), "--reason", "json operator approval", "--json"}); err != nil {
			t.Fatalf("cmdAgentic enqueue json: %v", err)
		}
	})
	var view struct {
		AutonomyEnabled bool  `json:"autonomy_enabled"`
		TaskID          int64 `json:"task_id"`
		QueueTaskID     int64 `json:"queue_task_id"`
		Existed         bool  `json:"existed"`
		Decision        struct {
			Decision string `json:"decision"`
			Status   string `json:"status"`
			Reason   string `json:"reason"`
		} `json:"decision"`
	}
	if err := json.Unmarshal([]byte(stdout), &view); err != nil {
		t.Fatalf("enqueue JSON = %q, unmarshal: %v", stdout, err)
	}
	if view.AutonomyEnabled || view.TaskID != task.ID || view.QueueTaskID == 0 || view.Existed {
		t.Fatalf("enqueue JSON view = %+v", view)
	}
	if view.Decision.Decision != agentic.TaskEnqueueDecisionApproved || view.Decision.Status != agentic.TaskEnqueueStatusEnqueued || view.Decision.Reason != "json operator approval" {
		t.Fatalf("enqueue JSON decision = %+v", view.Decision)
	}
}

func TestProposedTaskEnqueue_RerunIsIdempotent(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	task := createStandaloneProposedTask(t, fx)
	if err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID), "--reason", "first"}); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	first, err := fx.store.GetAgenticTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask first: %v", err)
	}
	before := countQueueRows(t, fx.db.Main)

	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID), "--reason", "second"}); err != nil {
			t.Fatalf("second enqueue: %v", err)
		}
	})
	if !strings.Contains(stdout, "already enqueued") {
		t.Fatalf("second enqueue output = %q, want already enqueued", stdout)
	}
	second, err := fx.store.GetAgenticTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask second: %v", err)
	}
	if second.QueueTaskID != first.QueueTaskID {
		t.Fatalf("queue task changed on rerun: first=%d second=%d", first.QueueTaskID, second.QueueTaskID)
	}
	if after := countQueueRows(t, fx.db.Main); after != before {
		t.Fatalf("queue rows changed on rerun: before=%d after=%d", before, after)
	}
}

func TestProposedTaskEnqueue_RejectsNonProposedTask(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	task := createStandaloneProposedTask(t, fx)
	if _, err := fx.store.UpdateAgenticTaskStatus(context.Background(), task.ID, agentic.TaskStatusFailed); err != nil {
		t.Fatalf("UpdateAgenticTaskStatus: %v", err)
	}
	err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID)})
	if err == nil || !strings.Contains(err.Error(), "not eligible") {
		t.Fatalf("enqueue failed task err = %v, want not eligible", err)
	}
}

func TestProposedTaskEnqueue_RejectsAlreadyQueueBackedTask(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(fx.task.ID)})
	if err == nil || !strings.Contains(err.Error(), "already linked to queue task") {
		t.Fatalf("enqueue queue-backed task err = %v, want already linked rejection", err)
	}
}

func TestProposedTaskEnqueue_CarriesRequestedGatewayAndCompletionGateOnlyWhenExplicit(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	enableAgenticModes(t, fx.cfgPath)
	task := createStandaloneProposedTask(t, fx)
	if err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID), "--agentic-enforcement", "gateway", "--completion-gate", "verification"}); err != nil {
		t.Fatalf("enqueue with explicit modes: %v", err)
	}
	updated, err := fx.store.GetAgenticTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetAgenticTask: %v", err)
	}
	queued, err := fx.queue.Get(context.Background(), updated.QueueTaskID)
	if err != nil {
		t.Fatalf("queue get: %v", err)
	}
	payload := daemon.ParseTaskPayload(queued.Payload)
	if payload.AgenticEnforcement != config.AgenticEnforcementModeGateway || payload.AgenticCompletionGate != config.AgenticCompletionGateModeVerification {
		t.Fatalf("payload modes = enforcement %q completion %q", payload.AgenticEnforcement, payload.AgenticCompletionGate)
	}
}

func TestProposedTaskEnqueue_ConfigObserveRejectsGatewayOrCompletionRequest(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	task := createStandaloneProposedTask(t, fx)
	err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID), "--agentic-enforcement", "gateway"})
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("gateway request with observe config err = %v, want config rejection", err)
	}
	err = cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID), "--completion-gate", "verification"})
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("completion request with observe config err = %v, want config rejection", err)
	}
}

func TestProposedTaskEnqueue_DoesNotCreateToolReceiptsVerificationMemoryOrFollowups(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	task := createStandaloneProposedTask(t, fx)
	before := map[string]int{
		"policy_decisions":     countRows(t, fx.db.Main, "policy_decisions"),
		"approval_requests":    countRows(t, fx.db.Main, "approval_requests"),
		"tool_action_receipts": countRows(t, fx.db.Main, "tool_action_receipts"),
		"verification_runs":    countRows(t, fx.db.Main, "verification_runs"),
		"memory_updates":       countRows(t, fx.db.Main, "memory_updates"),
		"followups":            countRows(t, fx.db.Main, "followups"),
	}
	if err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID)}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	for table, want := range before {
		if got := countRows(t, fx.db.Main, table); got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
}

func TestAgenticActivate_RunOnceJSONProcessesDueFollowupWithoutEnqueue(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	beforeQueue := countQueueRows(t, fx.db.Main)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"activate", "--once", "--limit", "10", "--json"}); err != nil {
			t.Fatalf("cmdAgentic activate: %v", err)
		}
	})
	var view agenticActivationView
	if err := json.Unmarshal([]byte(stdout), &view); err != nil {
		t.Fatalf("activate JSON = %q, unmarshal: %v", stdout, err)
	}
	if view.RunID == 0 || view.AutonomyEnabled || view.ExecutionPolicy != "propose_only" || view.Limit != 10 || view.EnqueuePerformed || view.Status != agentic.ActivationRunStatusSucceeded {
		t.Fatalf("activate view = %+v", view)
	}
	if view.Followups.Processed != 1 || view.Followups.Created != 1 {
		t.Fatalf("followup counts = %+v", view.Followups)
	}
	if view.Signals.Processed != 1 || view.Signals.Failed != 0 {
		t.Fatalf("signal counts = %+v", view.Signals)
	}
	if after := countQueueRows(t, fx.db.Main); after != beforeQueue {
		t.Fatalf("queue rows changed: before=%d after=%d", beforeQueue, after)
	}
	updated, err := fx.store.GetFollowup(context.Background(), fx.followup.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	if updated.Status != agentic.FollowupStatusCreated || updated.CreatedTaskID == 0 {
		t.Fatalf("followup after activate = %+v", updated)
	}
	child, err := fx.store.GetAgenticTask(context.Background(), updated.CreatedTaskID)
	if err != nil {
		t.Fatalf("GetAgenticTask child: %v", err)
	}
	if child.Status != agentic.TaskStatusProposed || child.QueueTaskID != 0 || child.ParentID != fx.task.ID {
		t.Fatalf("child task = %+v", child)
	}
	if len(view.ProposedTaskIDs) != 1 || view.ProposedTaskIDs[0] != child.ID {
		t.Fatalf("proposed task ids = %+v, want child %d", view.ProposedTaskIDs, child.ID)
	}
	signal, err := fx.store.GetGoalSignal(context.Background(), child.SignalID)
	if err != nil {
		t.Fatalf("GetGoalSignal: %v", err)
	}
	if signal.Status != agentic.SignalStatusTriaged {
		t.Fatalf("signal status = %q, want triaged", signal.Status)
	}
	run, err := fx.store.GetActivationRun(context.Background(), view.RunID)
	if err != nil {
		t.Fatalf("GetActivationRun: %v", err)
	}
	if run.FollowupCreated != 1 || run.SignalProcessed != 1 || run.EnqueuePerformed {
		t.Fatalf("activation run = %+v", run)
	}
}

func TestAgenticActivate_AutoEnqueuesLowRiskDueFollowupWhenConfigured(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	enableAgenticActivationAutoEnqueue(t, fx.cfgPath)
	beforeQueue := countQueueRows(t, fx.db.Main)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"activate", "--once", "--limit", "10", "--json"}); err != nil {
			t.Fatalf("cmdAgentic activate: %v", err)
		}
	})
	var view agenticActivationView
	if err := json.Unmarshal([]byte(stdout), &view); err != nil {
		t.Fatalf("activate JSON = %q, unmarshal: %v", stdout, err)
	}
	if view.ExecutionPolicy != agenticactivation.ExecutionPolicyAutoEnqueueLowRisk || !view.EnqueuePerformed || view.Status != agentic.ActivationRunStatusSucceeded {
		t.Fatalf("activate view = %+v", view)
	}
	if view.AutoEnqueue == nil || view.AutoEnqueue.Considered != 1 || view.AutoEnqueue.Enqueued != 1 || len(view.AutoEnqueue.QueueTaskIDs) != 1 {
		t.Fatalf("auto enqueue view = %+v", view.AutoEnqueue)
	}
	if after := countQueueRows(t, fx.db.Main); after != beforeQueue+1 {
		t.Fatalf("queue rows = %d, want %d", after, beforeQueue+1)
	}
	updated, err := fx.store.GetFollowup(context.Background(), fx.followup.ID)
	if err != nil {
		t.Fatalf("GetFollowup: %v", err)
	}
	child, err := fx.store.GetAgenticTask(context.Background(), updated.CreatedTaskID)
	if err != nil {
		t.Fatalf("GetAgenticTask child: %v", err)
	}
	if child.Status != agentic.TaskStatusPending || child.QueueTaskID != view.AutoEnqueue.QueueTaskIDs[0] {
		t.Fatalf("child task = %+v", child)
	}
	decisions, err := fx.store.ListTaskEnqueueDecisionsByTask(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("ListTaskEnqueueDecisionsByTask: %v", err)
	}
	if len(decisions) != 1 || decisions[0].QueueTaskID != child.QueueTaskID || decisions[0].OperatorID != "agentic-activation" || decisions[0].RequestedEnforcement != config.AgenticEnforcementModeGateway || decisions[0].RequestedCompletionGate != config.AgenticCompletionGateModeVerification {
		t.Fatalf("enqueue decisions = %+v", decisions)
	}
}

func TestAgenticActivate_RequiresExplicitOnce(t *testing.T) {
	newAgenticCommandFixture(t)
	err := cmdAgentic(context.Background(), []string{"activate", "--json"})
	if err == nil || !strings.Contains(err.Error(), "activate --once") {
		t.Fatalf("activate without --once err = %v, want usage", err)
	}
}

func TestAgenticActivate_HumanOutputSummarizesPolicy(t *testing.T) {
	newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"activate", "--once"}); err != nil {
			t.Fatalf("cmdAgentic activate: %v", err)
		}
	})
	for _, want := range []string{
		"Agentic Activation",
		"run_id:",
		"execution_policy: propose_only",
		"enqueue_performed: false",
		"status: succeeded",
		"proposed_task_ids:",
		"followups: processed=1 created=1",
		"signals: processed=1",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("activate output = %q, want %q", stdout, want)
		}
	}
}

func TestAgenticActivations_ReadOnlyHistory(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"activate", "--once"}); err != nil {
			t.Fatalf("activate: %v", err)
		}
	})
	before := tableCounts(t, fx.db.Main, agenticSideEffectTables()...)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"activations", "--limit", "5"}); err != nil {
			t.Fatalf("activations: %v", err)
		}
	})
	after := tableCounts(t, fx.db.Main, agenticSideEffectTables()...)
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("activations mutated side-effect tables: before=%v after=%v", before, after)
	}
	for _, want := range []string{
		"Agentic Activations",
		"policy=propose_only",
		"enqueue=false",
		"proposed_task_ids=",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("activations output = %q, want %q", stdout, want)
		}
	}
}

func TestAgenticActivations_JSONOutputStable(t *testing.T) {
	newAgenticCommandFixture(t)
	captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"activate", "--once"}); err != nil {
			t.Fatalf("activate: %v", err)
		}
	})
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"activations", "--json"}); err != nil {
			t.Fatalf("activations json: %v", err)
		}
	})
	var view agenticActivationsView
	if err := json.Unmarshal([]byte(stdout), &view); err != nil {
		t.Fatalf("activations JSON = %q, unmarshal: %v", stdout, err)
	}
	if view.AutonomyEnabled || view.Limit != 10 || len(view.Runs) != 1 {
		t.Fatalf("activations view = %+v", view)
	}
	if view.Runs[0].ExecutionPolicy != "propose_only" || view.Runs[0].Status != agentic.ActivationRunStatusSucceeded {
		t.Fatalf("activation run summary = %+v", view.Runs[0])
	}
	if len(view.Runs[0].ProposedTaskIDs) != 1 {
		t.Fatalf("activation run proposed task ids = %+v, want one", view.Runs[0].ProposedTaskIDs)
	}
}

func TestAgenticOperatorLineage_ShowsProposedTaskEnqueueState(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	task := createStandaloneProposedTask(t, fx)
	if err := cmdAgentic(context.Background(), []string{"enqueue", fmt.Sprint(task.ID), "--reason", "bounded operator continuation"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"lineage", fmt.Sprint(task.ID)}); err != nil {
			t.Fatalf("lineage: %v", err)
		}
	})
	for _, want := range []string{
		"Enqueue",
		"approved",
		"enqueued",
		"bounded operator continuation",
		"queue_task_id:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("lineage output = %q, want %q", stdout, want)
		}
	}
}

func createStandaloneProposedTask(t *testing.T, fx *agenticCommandFixture) *agentic.AgenticTask {
	t.Helper()
	task, err := fx.store.CreateAgenticTask(context.Background(), agentic.AgenticTask{
		GoalID:             fx.goal.ID,
		Title:              "Standalone proposed task",
		Prompt:             "Review the proposed task and execute bounded work.",
		Status:             agentic.TaskStatusProposed,
		Priority:           1,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func countQueueRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	return countRows(t, db, "task_queue")
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func enableAgenticModes(t *testing.T, cfgPath string) {
	t.Helper()
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString("\nagentic:\n  enforcement:\n    mode: gateway\n  completion_gate:\n    mode: verification\n"); err != nil {
		t.Fatalf("append config: %v", err)
	}
}

func enableAgenticActivationAutoEnqueue(t *testing.T, cfgPath string) {
	t.Helper()
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(`
agentic:
  enforcement:
    mode: gateway
  completion_gate:
    mode: verification
  activation:
    enabled: true
    auto_enqueue:
      enabled: true
      limit: 3
      max_risk_level: low
      agentic_enforcement: gateway
      completion_gate: verification
`); err != nil {
		t.Fatalf("append config: %v", err)
	}
}

func TestAgenticCommand_StatusSummarizesLedgerCounts(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"status"}); err != nil {
			t.Fatalf("cmdAgentic status: %v", err)
		}
	})
	for _, want := range []string{
		"Agentic Control Plane",
		"autonomy_enabled: false",
		"goals: active=1",
		"signals: triaged=1",
		"tasks: proposed=1",
		"approvals: pending=1",
		"receipts: failed=1",
		"completion_gates: blocked=1",
		"verification: failed=1",
		"memory: blocked=1",
		"followups: pending=1 due=1",
		"actors: failed=1 succeeded=1",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status output = %q, want %q", stdout, want)
		}
	}
	assertNoRawSecrets(t, stdout, fx.rawSecrets)
}

func TestAgenticCommand_StatusReportsAttentionItems(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"status"}); err != nil {
			t.Fatalf("cmdAgentic status: %v", err)
		}
	})
	for _, want := range []string{
		fmt.Sprintf("approval #%d pending", fx.approval.ID),
		fmt.Sprintf("receipt #%d failed", fx.receipt.ID),
		fmt.Sprintf("verification #%d failed", fx.verifier.ID),
		fmt.Sprintf("memory #%d blocked", fx.memory.ID),
		fmt.Sprintf("followup #%d due", fx.followup.ID),
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status output = %q, want attention %q", stdout, want)
		}
	}
}

func TestAgenticCommand_CompletionGateViewsHandleMissingTable(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	if _, err := fx.db.Main.Exec(`DROP TABLE completion_gates`); err != nil {
		t.Fatalf("drop completion_gates: %v", err)
	}

	statusOut, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"status"}); err != nil {
			t.Fatalf("cmdAgentic status with missing completion_gates: %v", err)
		}
	})
	if !strings.Contains(statusOut, "Agentic Control Plane") {
		t.Fatalf("status output = %q, want agentic status", statusOut)
	}

	taskOut, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"task", fmt.Sprint(fx.task.ID)}); err != nil {
			t.Fatalf("cmdAgentic task with missing completion_gates: %v", err)
		}
	})
	if !strings.Contains(taskOut, "completion_gates: none") {
		t.Fatalf("task output = %q, want missing completion gates rendered as none", taskOut)
	}

	lineageOut, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"lineage", fmt.Sprint(fx.task.ID)}); err != nil {
			t.Fatalf("cmdAgentic lineage with missing completion_gates: %v", err)
		}
	})
	if !strings.Contains(lineageOut, "Completion gates\n  none") {
		t.Fatalf("lineage output = %q, want missing completion gates section rendered as none", lineageOut)
	}
}

func TestAgenticCommand_TaskShowsCoreTaskLinks(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"task", fmt.Sprint(fx.task.ID)}); err != nil {
			t.Fatalf("cmdAgentic task: %v", err)
		}
	})
	for _, want := range []string{
		fmt.Sprintf("Agentic Task #%d", fx.task.ID),
		"Investigate blocked receipt",
		"status: proposed",
		fmt.Sprintf("goal: #%d Ship operator lineage", fx.goal.ID),
		fmt.Sprintf("signal: #%d scheduler/scheduled_task triaged", fx.signal.ID),
		fmt.Sprintf("queue_task_id: %d", fx.queueTask),
		fmt.Sprintf("approval: #%d pending", fx.approval.ID),
		fmt.Sprintf("policy #%d approval_required", fx.policy.ID),
		fmt.Sprintf("latest verification: #%d failed", fx.verifier.ID),
		"memory: blocked=1",
		"followups: pending=1 due=1",
		"actors: executor=1 planner=1",
		"completion_gates: blocked=1",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("task output = %q, want %q", stdout, want)
		}
	}
	assertNoRawSecrets(t, stdout, fx.rawSecrets)
}

func TestAgenticCommand_LineageShowsGoalSignalTaskActorPolicyApprovalReceiptVerificationMemoryFollowup(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"lineage", fmt.Sprint(fx.task.ID)}); err != nil {
			t.Fatalf("cmdAgentic lineage: %v", err)
		}
	})
	for _, want := range []string{
		fmt.Sprintf("Lineage for Agentic Task #%d", fx.task.ID),
		"Goal",
		fmt.Sprintf("#%d active priority=5 Ship operator lineage", fx.goal.ID),
		"Signal",
		fmt.Sprintf("#%d scheduler/scheduled_task status=triaged", fx.signal.ID),
		"Task",
		"Queue",
		fmt.Sprintf("queue_task_id: %d", fx.queueTask),
		"Actors",
		fmt.Sprintf("#%d planner succeeded", fx.planner.ID),
		fmt.Sprintf("#%d executor failed", fx.executor.ID),
		"Handoffs",
		fmt.Sprintf("#%d planner -> executor planned_subtask succeeded", fx.handoff.ID),
		"Policy decisions",
		fmt.Sprintf("#%d approval_required risk=high tool=bash", fx.policy.ID),
		"Approvals",
		fmt.Sprintf("#%d pending tool=bash risk=high", fx.approval.ID),
		"Receipts",
		fmt.Sprintf("#%d failed tool=bash", fx.receipt.ID),
		"Completion gates",
		fmt.Sprintf("#%d blocked verifier=#%d reason=verification failed bounded reason summary={\"started\":0,\"failed\":1}", fx.gate.ID, fx.verifier.ID),
		"Verification",
		fmt.Sprintf("#%d failed", fx.verifier.ID),
		"Memory",
		fmt.Sprintf("#%d blocked target=wiki", fx.memory.ID),
		"Followups",
		fmt.Sprintf("#%d pending", fx.followup.ID),
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("lineage output = %q, want %q", stdout, want)
		}
	}
	assertNoRawSecrets(t, stdout, fx.rawSecrets)
}

func TestAgenticCommand_LineageHandlesMissingOptionalSections(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	minimal, err := fx.store.CreateAgenticTask(context.Background(), agentic.AgenticTask{
		Title:              "Minimal task",
		Prompt:             "minimal prompt",
		Status:             agentic.TaskStatusProposed,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserveOnly,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("minimal task: %v", err)
	}
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"lineage", fmt.Sprint(minimal.ID)}); err != nil {
			t.Fatalf("cmdAgentic lineage minimal: %v", err)
		}
	})
	for _, want := range []string{
		fmt.Sprintf("Lineage for Agentic Task #%d", minimal.ID),
		"Goal\n  none",
		"Signal\n  none",
		"Queue\n  none",
		"Actors\n  none",
		"Handoffs\n  none",
		"Policy decisions\n  none",
		"Approvals\n  none",
		"Receipts\n  none",
		"Completion gates\n  none",
		"Verification\n  none",
		"Memory\n  none",
		"Followups\n  none",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("minimal lineage output = %q, want %q", stdout, want)
		}
	}
}

func TestAgenticCommand_JSONOutputStable(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"lineage", fmt.Sprint(fx.task.ID), "--json"}); err != nil {
			t.Fatalf("cmdAgentic lineage json: %v", err)
		}
	})
	var view struct {
		AutonomyEnabled bool `json:"autonomy_enabled"`
		Task            struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
		Goal         any   `json:"goal"`
		Signal       any   `json:"signal"`
		Queue        any   `json:"queue"`
		Actors       []any `json:"actors"`
		Handoffs     []any `json:"handoffs"`
		Policies     []any `json:"policy_decisions"`
		Approvals    []any `json:"approvals"`
		Receipts     []any `json:"receipts"`
		Gates        []any `json:"completion_gates"`
		Verification []any `json:"verification_runs"`
		Memory       []any `json:"memory_updates"`
		Followups    []any `json:"followups"`
	}
	if err := json.Unmarshal([]byte(stdout), &view); err != nil {
		t.Fatalf("lineage JSON = %q, unmarshal: %v", stdout, err)
	}
	if view.AutonomyEnabled {
		t.Fatal("autonomy_enabled = true, want false")
	}
	if view.Task.ID != fx.task.ID || view.Task.Status != agentic.TaskStatusProposed {
		t.Fatalf("json task = %+v, want id/status", view.Task)
	}
	if len(view.Actors) != 2 || len(view.Policies) != 1 || len(view.Receipts) != 1 || len(view.Gates) != 1 || len(view.Followups) != 1 {
		t.Fatalf("json view missing sections: %+v", view)
	}
	assertNoRawSecrets(t, stdout, fx.rawSecrets)
}

func TestAgenticCommand_ReadOnlyDoesNotMutateAgenticTables(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	before := tableCounts(t, fx.db.Main, agenticSideEffectTables()...)
	runAgenticCommandVariants(t, fx)
	after := tableCounts(t, fx.db.Main, agenticSideEffectTables()...)
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("agentic table counts changed: before=%v after=%v", before, after)
	}
}

func TestAgenticCommand_DoesNotEnqueueDaemonWork(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	before := tableCounts(t, fx.db.Main, "task_queue")
	runAgenticCommandVariants(t, fx)
	after := tableCounts(t, fx.db.Main, "task_queue")
	if after["task_queue"] != before["task_queue"] {
		t.Fatalf("task_queue count changed: before=%v after=%v", before, after)
	}
}

func TestAgenticCommand_DoesNotCreatePolicyApprovalReceiptVerificationMemoryOrFollowupRows(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	tables := []string{"policy_decisions", "approval_requests", "tool_action_receipts", "verification_runs", "memory_updates", "followups"}
	before := tableCounts(t, fx.db.Main, tables...)
	runAgenticCommandVariants(t, fx)
	after := tableCounts(t, fx.db.Main, tables...)
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("side-effect table counts changed: before=%v after=%v", before, after)
	}
}

func TestAgenticCommand_RedactsRawPayloadsAndToolOutputs(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"lineage", fmt.Sprint(fx.task.ID)}); err != nil {
			t.Fatalf("cmdAgentic lineage: %v", err)
		}
	})
	assertNoRawSecrets(t, stdout, fx.rawSecrets)
	if strings.Contains(stdout, `"queue_task_id":`) {
		t.Fatalf("lineage output leaked raw payload JSON: %q", stdout)
	}
}

func TestAgenticCommand_TaskCanResolveByQueueTaskIDIfFlagged(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdAgentic(context.Background(), []string{"task", "--queue-task-id", fmt.Sprint(fx.queueTask)}); err != nil {
			t.Fatalf("cmdAgentic task by queue id: %v", err)
		}
	})
	if !strings.Contains(stdout, fmt.Sprintf("Agentic Task #%d", fx.task.ID)) {
		t.Fatalf("task by queue output = %q, want agentic task id %d", stdout, fx.task.ID)
	}
}

func TestExistingTaskAndDaemonStatusCommandsUnchanged(t *testing.T) {
	fx := newAgenticCommandFixture(t)
	stdout, _ := captureOutput(t, func() {
		if err := cmdTaskShow(context.Background(), []string{fmt.Sprint(fx.queueTask)}); err != nil {
			t.Fatalf("cmdTaskShow: %v", err)
		}
	})
	for _, want := range []string{"ID:", "Status:", "Payload:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("task show output = %q, want %q", stdout, want)
		}
	}
	for _, forbidden := range []string{"Agentic Task", "policy #", "verification:", "followups:"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("task show output = %q, should not contain %q", stdout, forbidden)
		}
	}
}

func runAgenticCommandVariants(t *testing.T, fx *agenticCommandFixture) {
	t.Helper()
	for _, args := range [][]string{
		{"status"},
		{"status", "--json"},
		{"activations"},
		{"activations", "--json"},
		{"task", fmt.Sprint(fx.task.ID)},
		{"task", fmt.Sprint(fx.task.ID), "--json"},
		{"task", "--queue-task-id", fmt.Sprint(fx.queueTask)},
		{"lineage", fmt.Sprint(fx.task.ID)},
		{"lineage", fmt.Sprint(fx.task.ID), "--json"},
	} {
		captureOutput(t, func() {
			if err := cmdAgentic(context.Background(), args); err != nil {
				t.Fatalf("cmdAgentic(%v): %v", args, err)
			}
		})
	}
}

func tableCounts(t *testing.T, db *sql.DB, tables ...string) map[string]int {
	t.Helper()
	out := make(map[string]int, len(tables))
	for _, table := range tables {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = count
	}
	return out
}

func agenticSideEffectTables() []string {
	return []string{
		"standing_goals",
		"signal_watchers",
		"goal_signals",
		"agentic_tasks",
		"task_edges",
		"agent_actors",
		"actor_handoffs",
		"policy_decisions",
		"approval_requests",
		"tool_action_receipts",
		"completion_gates",
		"task_enqueue_decisions",
		"verification_runs",
		"memory_updates",
		"followups",
		"activation_runs",
		"task_queue",
	}
}

func assertNoRawSecrets(t *testing.T, output string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(output, secret) {
			t.Fatalf("output leaked raw secret %q: %q", secret, output)
		}
	}
}
