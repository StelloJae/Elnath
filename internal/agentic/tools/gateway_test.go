package agentictools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/agentic/approvals"
	"github.com/stello/elnath/internal/agentic/policy"
	"github.com/stello/elnath/internal/daemon"
	basetools "github.com/stello/elnath/internal/tools"

	_ "modernc.org/sqlite"
)

type recordingExecutor struct {
	mu     sync.Mutex
	calls  int
	result *basetools.Result
	err    error
	before func()
}

func (e *recordingExecutor) Execute(ctx context.Context, name string, params json.RawMessage) (*basetools.Result, error) {
	if e.before != nil {
		e.before()
	}
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if e.result != nil || e.err != nil {
		return e.result, e.err
	}
	return basetools.SuccessResult(name + " ok"), nil
}

func (e *recordingExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type fakeTool struct {
	name       string
	safe       bool
	reversible bool
	scope      basetools.ToolScope
}

func (t fakeTool) Name() string            { return t.name }
func (t fakeTool) Description() string     { return t.name }
func (t fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t fakeTool) Execute(context.Context, json.RawMessage) (*basetools.Result, error) {
	return basetools.SuccessResult(t.name + " registry ok"), nil
}
func (t fakeTool) IsConcurrencySafe(json.RawMessage) bool    { return t.safe }
func (t fakeTool) Reversible() bool                          { return t.reversible }
func (t fakeTool) Scope(json.RawMessage) basetools.ToolScope { return t.scope }
func (t fakeTool) ShouldCancelSiblingsOnError() bool         { return false }

type failingStore struct {
	policyErr  error
	receiptErr error
}

func (s failingStore) CreatePolicyDecision(context.Context, agentic.PolicyDecisionRecord) (*agentic.PolicyDecisionRecord, error) {
	if s.policyErr != nil {
		return nil, s.policyErr
	}
	return &agentic.PolicyDecisionRecord{ID: 1, TaskID: 1, Decision: agentic.PolicyDecisionAuto, RiskLevel: agentic.RiskLevelLow}, nil
}

func (s failingStore) CreateToolActionReceipt(context.Context, agentic.ToolActionReceipt) (*agentic.ToolActionReceipt, error) {
	if s.receiptErr != nil {
		return nil, s.receiptErr
	}
	return &agentic.ToolActionReceipt{ID: 1, Status: agentic.ReceiptStatusStarted}, nil
}

func (s failingStore) CompleteToolActionReceipt(context.Context, int64, agentic.ToolActionReceiptCompletion) (*agentic.ToolActionReceipt, error) {
	return &agentic.ToolActionReceipt{ID: 1, Status: agentic.ReceiptStatusSucceeded}, nil
}

func TestToolGateway_ReadOnlyActionAutoAllowedExecutesAndReceipts(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	result, err := gateway.Execute(WithContext(ctx, Context{
		TaskID:     task.ID,
		ToolCallID: "call-read",
		ActionKind: "observe",
	}), "read_file", json.RawMessage(`{"path":"README.md"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError || !strings.Contains(result.Output, "read_file ok") {
		t.Fatalf("result = %+v, want successful read", result)
	}
	if exec.Calls() != 1 {
		t.Fatalf("executor calls = %d, want 1", exec.Calls())
	}
	assertLatestDecision(t, db, agentic.PolicyDecisionAuto)
	receipt := latestReceipt(t, db)
	if receipt.Status != agentic.ReceiptStatusSucceeded || receipt.ToolCallID != "call-read" || receipt.InputHash == "" || receipt.RawOutputHash == "" || receipt.VisibleOutputHash == "" || receipt.OutputHash == "" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestToolGateway_ObserveAllowsAgenticActorGraph(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	result, err := gateway.Execute(WithContext(ctx, Context{
		TaskID:     task.ID,
		ToolCallID: "call-graph",
		ActionKind: "observe",
	}), ActorGraphToolName, json.RawMessage(`{"task_id":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || result.IsError || !strings.Contains(result.Output, ActorGraphToolName+" ok") {
		t.Fatalf("result = %+v, want successful graph read", result)
	}
	if exec.Calls() != 1 {
		t.Fatalf("executor calls = %d, want 1", exec.Calls())
	}
	assertLatestDecision(t, db, agentic.PolicyDecisionAuto)
}

func TestToolGateway_AgenticActorGraphIsReadOnly(t *testing.T) {
	if !isReadOnlyTool(ActorGraphToolName) {
		t.Fatal("agentic_actor_graph should be classified as read-only")
	}
}

func TestToolGateway_AgenticTaskEvidenceIsReadOnly(t *testing.T) {
	if !isReadOnlyTool(TaskEvidenceToolName) {
		t.Fatal("agentic_task_evidence should be classified as read-only")
	}
}

func TestToolGateway_AgenticDelegateListIsReadOnly(t *testing.T) {
	if !isReadOnlyTool(DelegateListToolName) {
		t.Fatal("agentic_delegate_list should be classified as read-only")
	}
}

func TestToolGateway_AgenticMessageListIsReadOnly(t *testing.T) {
	if !isReadOnlyTool(ActorMessageListToolName) {
		t.Fatal("agentic_message_list should be classified as read-only")
	}
}

func TestToolGateway_DelegateCreateIsMutating(t *testing.T) {
	if isReadOnlyTool(DelegateCreateToolName) {
		t.Fatal("agentic_delegate_create should not be classified as read-only")
	}
}

func TestToolGateway_DelegateEnqueueIsMutating(t *testing.T) {
	if isReadOnlyTool(DelegateEnqueueToolName) {
		t.Fatal("agentic_delegate_enqueue should not be classified as read-only")
	}
}

func TestToolGateway_AgenticMessageSendIsMutating(t *testing.T) {
	if isReadOnlyTool(ActorMessageSendToolName) {
		t.Fatal("agentic_message_send should not be classified as read-only")
	}
}

func TestToolGateway_MutatingActionRequiresApprovalAndDoesNotExecute(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	result, err := gateway.Execute(WithContext(ctx, Context{TaskID: task.ID, ToolCallID: "call-write", ActionKind: "mutate"}), "write_file", json.RawMessage(`{"path":"a.txt","content":"x"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "approval required") {
		t.Fatalf("result = %+v, want approval-required error result", result)
	}
	if exec.Calls() != 0 {
		t.Fatalf("executor calls = %d, want 0", exec.Calls())
	}
	assertLatestDecision(t, db, agentic.PolicyDecisionApprovalRequired)
	receipt := latestReceipt(t, db)
	if receipt.Status != agentic.ReceiptStatusApprovalRequired || receipt.ApprovalRequestID == "" {
		t.Fatalf("unexpected blocked receipt: %+v", receipt)
	}
	if countRows(t, db, "approval_requests") != 1 {
		t.Fatalf("approval_requests count = %d, want 1", countRows(t, db, "approval_requests"))
	}
}

func TestToolGateway_MutatingActionReusesPendingApprovalForSameAction(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)
	toolCtx := Context{TaskID: task.ID, ToolCallID: "call-write-1", ActionKind: "mutate"}
	input := json.RawMessage(`{"path":"a.txt","content":"x"}`)

	first, err := gateway.Execute(WithContext(ctx, toolCtx), "write_file", input)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	toolCtx.ToolCallID = "call-write-2"
	second, err := gateway.Execute(WithContext(ctx, toolCtx), "write_file", input)
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if first == nil || second == nil || !first.IsError || !second.IsError {
		t.Fatalf("results = %+v / %+v, want blocked error results", first, second)
	}
	if exec.Calls() != 0 {
		t.Fatalf("executor calls = %d, want 0", exec.Calls())
	}
	if got := countRows(t, db, "approval_requests"); got != 1 {
		t.Fatalf("approval_requests count = %d, want 1", got)
	}
	var distinctApprovals int
	if err := db.QueryRow(`SELECT COUNT(DISTINCT approval_request_id) FROM tool_action_receipts WHERE status = ?`, agentic.ReceiptStatusApprovalRequired).Scan(&distinctApprovals); err != nil {
		t.Fatalf("count distinct approval ids: %v", err)
	}
	if distinctApprovals != 1 {
		t.Fatalf("distinct blocked approval ids = %d, want 1", distinctApprovals)
	}
}

func TestToolGateway_MutatingActionDoesNotReuseResolvedApproval(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	gateway := NewGateway(&recordingExecutor{}, store, policy.NewEvaluator(), approvalBridge)
	toolCtx := Context{TaskID: task.ID, ToolCallID: "call-write-1", ActionKind: "mutate"}
	input := json.RawMessage(`{"path":"a.txt","content":"x"}`)

	first, err := gateway.Execute(WithContext(ctx, toolCtx), "write_file", input)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if first == nil || !first.IsError {
		t.Fatalf("first result = %+v, want approval error", first)
	}
	var firstApprovalID int64
	if err := db.QueryRow(`SELECT id FROM approval_requests ORDER BY id LIMIT 1`).Scan(&firstApprovalID); err != nil {
		t.Fatalf("first approval id: %v", err)
	}
	if _, err := db.Exec(`UPDATE approval_requests SET decision = 'approved' WHERE id = ?`, firstApprovalID); err != nil {
		t.Fatalf("approve first request: %v", err)
	}

	toolCtx.ToolCallID = "call-write-2"
	second, err := gateway.Execute(WithContext(ctx, toolCtx), "write_file", input)
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}
	if second == nil || !second.IsError {
		t.Fatalf("second result = %+v, want approval error", second)
	}
	if got := countRows(t, db, "approval_requests"); got != 2 {
		t.Fatalf("approval_requests count = %d, want 2", got)
	}
	receipt := latestReceipt(t, db)
	firstApprovalIDString := strconv.FormatInt(firstApprovalID, 10)
	if receipt.ApprovalRequestID == "" || receipt.ApprovalRequestID == firstApprovalIDString {
		t.Fatalf("latest receipt approval id = %q, want new id different from %s", receipt.ApprovalRequestID, firstApprovalIDString)
	}
}

func TestToolGateway_HardlineDeniedDoesNotExecute(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	result, err := gateway.Execute(WithContext(ctx, Context{TaskID: task.ID, ToolCallID: "call-deny", ActionKind: "mutate"}), "bash", json.RawMessage(`{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "denied") {
		t.Fatalf("result = %+v, want denied error result", result)
	}
	if exec.Calls() != 0 {
		t.Fatalf("executor calls = %d, want 0", exec.Calls())
	}
	assertLatestDecision(t, db, agentic.PolicyDecisionDenied)
	receipt := latestReceipt(t, db)
	if receipt.Status != agentic.ReceiptStatusDenied || receipt.FailureReason == "" {
		t.Fatalf("unexpected denied receipt: %+v", receipt)
	}
}

func TestToolGateway_ReceiptCreatedBeforeExecution(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{
		before: func() {
			if got := countRows(t, db, "tool_action_receipts"); got != 1 {
				t.Fatalf("receipt count before execution = %d, want 1", got)
			}
		},
	}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	if _, err := gateway.Execute(WithContext(ctx, Context{TaskID: task.ID, ToolCallID: "call-before", ActionKind: "observe"}), "read_file", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestToolGateway_CompletesReceiptOnToolSuccess(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{result: basetools.SuccessResult("custom success")}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	if _, err := gateway.Execute(WithContext(ctx, Context{TaskID: task.ID, ToolCallID: "call-success", ActionKind: "observe"}), "read_file", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	receipt := latestReceipt(t, db)
	if receipt.Status != agentic.ReceiptStatusSucceeded || receipt.OutputSummary != "custom success" || receipt.OutputHash == "" || receipt.RawOutputHash != receipt.VisibleOutputHash {
		t.Fatalf("unexpected success receipt: %+v", receipt)
	}
}

func TestToolGateway_CompletesReceiptOnToolErrorResult(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{result: basetools.ErrorResult("tool failed")}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	result, err := gateway.Execute(WithContext(ctx, Context{TaskID: task.ID, ToolCallID: "call-error-result", ActionKind: "observe"}), "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("result = %+v, want error result", result)
	}
	receipt := latestReceipt(t, db)
	if receipt.Status != agentic.ReceiptStatusFailed || receipt.FailureReason == "" {
		t.Fatalf("unexpected failed receipt: %+v", receipt)
	}
}

func TestToolGateway_CompletesReceiptOnExecutorError(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	exec := &recordingExecutor{err: errors.New("boom")}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	result, err := gateway.Execute(WithContext(ctx, Context{TaskID: task.ID, ToolCallID: "call-error", ActionKind: "observe"}), "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "boom") {
		t.Fatalf("result = %+v, want executor error result", result)
	}
	receipt := latestReceipt(t, db)
	if receipt.Status != agentic.ReceiptStatusFailed || !strings.Contains(receipt.FailureReason, "boom") {
		t.Fatalf("unexpected failed receipt: %+v", receipt)
	}
}

func TestToolGateway_FailsClosedWhenPolicyCannotBeRecorded(t *testing.T) {
	exec := &recordingExecutor{}
	gateway := NewGateway(exec, failingStore{policyErr: errors.New("policy store down")}, policy.NewEvaluator(), nil)

	result, err := gateway.Execute(WithContext(context.Background(), Context{TaskID: 1, ToolCallID: "call-policy", ActionKind: "observe"}), "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "policy") {
		t.Fatalf("result = %+v, want policy error result", result)
	}
	if exec.Calls() != 0 {
		t.Fatalf("executor calls = %d, want 0", exec.Calls())
	}
}

func TestToolGateway_FailsClosedWhenReceiptCannotBeCreated(t *testing.T) {
	exec := &recordingExecutor{}
	gateway := NewGateway(exec, failingStore{receiptErr: errors.New("receipt store down")}, policy.NewEvaluator(), nil)

	result, err := gateway.Execute(WithContext(context.Background(), Context{TaskID: 1, ToolCallID: "call-receipt", ActionKind: "observe"}), "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "receipt") {
		t.Fatalf("result = %+v, want receipt error result", result)
	}
	if exec.Calls() != 0 {
		t.Fatalf("executor calls = %d, want 0", exec.Calls())
	}
}

func TestToolGateway_DoesNotChangePlainRegistryExecution(t *testing.T) {
	reg := basetools.NewRegistry()
	reg.Register(fakeTool{name: "write_file", scope: basetools.ToolScope{Persistent: true}})

	result, err := reg.Execute(context.Background(), "write_file", json.RawMessage(`{"path":"a.txt","content":"x"}`))
	if err != nil {
		t.Fatalf("plain registry Execute: %v", err)
	}
	if result == nil || result.IsError || result.Output != "write_file registry ok" {
		t.Fatalf("plain registry result = %+v", result)
	}
}

func TestAgenticToolContext_MissingTaskIDFailsClosed(t *testing.T) {
	_, store, approvalBridge, _ := newGatewayTestStore(t)
	exec := &recordingExecutor{}
	gateway := NewGateway(exec, store, policy.NewEvaluator(), approvalBridge)

	result, err := gateway.Execute(context.Background(), "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Output, "task_id") {
		t.Fatalf("result = %+v, want missing task_id error", result)
	}
	if exec.Calls() != 0 {
		t.Fatalf("executor calls = %d, want 0", exec.Calls())
	}
}

func TestAgenticToolGateway_DoesNotCreateVerifierMemoryFollowupSideEffects(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	gateway := NewGateway(&recordingExecutor{}, store, policy.NewEvaluator(), approvalBridge)

	if _, err := gateway.Execute(WithContext(ctx, Context{TaskID: task.ID, ToolCallID: "call-side-effects", ActionKind: "observe"}), "read_file", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, table := range []string{"verification_runs", "memory_updates", "followups"} {
		if got := countRows(t, db, table); got != 0 {
			t.Fatalf("%s count = %d, want 0", table, got)
		}
	}
}

func TestToolGateway_ParallelToolCallsCreateDistinctReceipts(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	gateway := NewGateway(&recordingExecutor{}, store, policy.NewEvaluator(), approvalBridge)

	const calls = 8
	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for i := 0; i < calls; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := gateway.Execute(WithContext(ctx, Context{TaskID: task.ID, ToolCallID: "parallel-" + string(rune('a'+i)), ActionKind: "observe"}), "read_file", json.RawMessage(`{}`))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("parallel Execute: %v", err)
		}
	}
	if got := countRows(t, db, "tool_action_receipts"); got != calls {
		t.Fatalf("receipt count = %d, want %d", got, calls)
	}
	if got := countDistinct(t, db, "tool_action_receipts", "tool_call_id"); got != calls {
		t.Fatalf("distinct tool_call_id count = %d, want %d", got, calls)
	}
}

func TestToolGateway_FinalizerKeyIncludesActorID(t *testing.T) {
	ctx := context.Background()
	db, store, approvalBridge, task := newGatewayTestStore(t)
	actorOne := createGatewayTestActor(t, ctx, store, task.ID, "executor-one")
	actorTwo := createGatewayTestActor(t, ctx, store, task.ID, "executor-two")
	gateway := NewGateway(&recordingExecutor{}, store, policy.NewEvaluator(), approvalBridge)
	params := json.RawMessage(`{}`)

	for _, actor := range []*agentic.AgentActor{actorOne, actorTwo} {
		toolCtx := Context{TaskID: task.ID, ActorID: actor.ID, ToolCallID: "same-call-id", ActionKind: "observe", FinalizeResult: true}
		if _, err := gateway.Execute(WithContext(ctx, toolCtx), "read_file", params); err != nil {
			t.Fatalf("Execute actor %d: %v", actor.ID, err)
		}
	}

	if err := gateway.FinalizeToolResult(WithContext(ctx, Context{TaskID: task.ID, ActorID: actorOne.ID, ToolCallID: "same-call-id"}), "read_file", params, basetools.SuccessResult("visible one")); err != nil {
		t.Fatalf("Finalize actor one: %v", err)
	}
	if err := gateway.FinalizeToolResult(WithContext(ctx, Context{TaskID: task.ID, ActorID: actorTwo.ID, ToolCallID: "same-call-id"}), "read_file", params, basetools.SuccessResult("visible two")); err != nil {
		t.Fatalf("Finalize actor two: %v", err)
	}

	if got := receiptSummaryForActor(t, db, actorOne.ID); got != "visible one" {
		t.Fatalf("actor one summary = %q, want visible one", got)
	}
	if got := receiptSummaryForActor(t, db, actorTwo.ID); got != "visible two" {
		t.Fatalf("actor two summary = %q, want visible two", got)
	}
}

func newGatewayTestStore(t *testing.T) (*sql.DB, *agentic.Store, *approvals.Bridge, *agentic.AgenticTask) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := agentic.InitSchema(db); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	approvalStore, err := daemon.NewApprovalStore(db)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	store := agentic.NewStore(db)
	task, err := store.CreateAgenticTask(context.Background(), agentic.AgenticTask{
		Title:              "Review signal",
		Prompt:             "Inspect the signal.",
		Status:             agentic.TaskStatusProposed,
		Priority:           1,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return db, store, approvals.NewBridge(db, store, approvalStore), task
}

func createGatewayTestActor(t *testing.T, ctx context.Context, store *agentic.Store, taskID int64, role string) *agentic.AgentActor {
	t.Helper()
	actor, err := store.CreateAgentActor(ctx, agentic.AgentActor{
		TaskID:            taskID,
		Role:              role,
		StateJSON:         `{}`,
		InboxJSON:         `[]`,
		OutboxJSON:        `[]`,
		ToolAllowlistJSON: `[]`,
		BudgetJSON:        `{}`,
		Status:            "active",
	})
	if err != nil {
		t.Fatalf("CreateAgentActor: %v", err)
	}
	return actor
}

func latestReceipt(t *testing.T, db *sql.DB) agentic.ToolActionReceipt {
	t.Helper()
	var r agentic.ToolActionReceipt
	err := db.QueryRow(`
		SELECT id, task_id, policy_decision_id, approval_request_id, tool_name, input_hash, output_hash, output_summary,
			status, tool_call_id, raw_output_hash, visible_output_hash, failure_reason, hook_provenance_json
		FROM tool_action_receipts ORDER BY id DESC LIMIT 1
	`).Scan(&r.ID, &r.TaskID, &r.PolicyDecisionID, &r.ApprovalRequestID, &r.ToolName, &r.InputHash, &r.OutputHash, &r.OutputSummary,
		&r.Status, &r.ToolCallID, &r.RawOutputHash, &r.VisibleOutputHash, &r.FailureReason, &r.HookProvenanceJSON)
	if err != nil {
		t.Fatalf("latest receipt: %v", err)
	}
	return r
}

func assertLatestDecision(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(`SELECT decision FROM policy_decisions ORDER BY id DESC LIMIT 1`).Scan(&got); err != nil {
		t.Fatalf("latest decision: %v", err)
	}
	if got != want {
		t.Fatalf("latest decision = %q, want %q", got, want)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func countDistinct(t *testing.T, db *sql.DB, table, column string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(DISTINCT ` + column + `) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count distinct %s.%s: %v", table, column, err)
	}
	return n
}

func receiptSummaryForActor(t *testing.T, db *sql.DB, actorID int64) string {
	t.Helper()
	var summary string
	if err := db.QueryRow(`SELECT output_summary FROM tool_action_receipts WHERE actor_id = ?`, actorID).Scan(&summary); err != nil {
		t.Fatalf("receipt summary for actor %d: %v", actorID, err)
	}
	return summary
}
