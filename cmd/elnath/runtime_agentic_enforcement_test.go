package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
)

type runtimeToolUseProvider struct {
	toolName    string
	toolInput   string
	finalText   string
	streamCalls int
}

func (p *runtimeToolUseProvider) Name() string { return "mock" }

func (p *runtimeToolUseProvider) Models() []llm.ModelInfo { return nil }

func (p *runtimeToolUseProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if strings.Contains(req.System, "intent classifier") {
		return &llm.ChatResponse{Content: `{"intent":"question","confidence":0.95}`}, nil
	}
	return &llm.ChatResponse{Content: "ok"}, nil
}

func (p *runtimeToolUseProvider) Stream(_ context.Context, _ llm.ChatRequest, cb func(llm.StreamEvent)) error {
	p.streamCalls++
	if p.streamCalls == 1 {
		cb(llm.StreamEvent{Type: llm.EventToolUseStart, ToolCall: &llm.ToolUseEvent{ID: "runtime-tool-1", Name: p.toolName}})
		cb(llm.StreamEvent{Type: llm.EventToolUseDone, ToolCall: &llm.ToolUseEvent{ID: "runtime-tool-1", Name: p.toolName, Input: p.toolInput}})
		cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 10, OutputTokens: 5}})
		return nil
	}
	text := p.finalText
	if text == "" {
		text = "done"
	}
	cb(llm.StreamEvent{Type: llm.EventTextDelta, Content: text})
	cb(llm.StreamEvent{Type: llm.EventDone, Usage: &llm.UsageStats{InputTokens: 5, OutputTokens: 3}})
	return nil
}

func TestNormalRun_WithoutOptInUsesPlainRegistry(t *testing.T) {
	provider := &runtimeToolUseProvider{toolName: "glob", toolInput: `{"pattern":"*.definitely-no-match"}`}
	rt := newGatewayOptInRuntime(t, "gateway", provider)
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)

	sess, err := rt.mgr.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, _, err := rt.runTask(ctx, sess, nil, "read without opt-in", orchestrationOutput{}); err != nil {
		t.Fatalf("runTask: %v", err)
	}

	if got := runtimeCountRows(t, rt.db.Main, "policy_decisions"); got != 0 {
		t.Fatalf("policy_decisions = %d, want 0 for non-opt-in run", got)
	}
	if got := runtimeCountRows(t, rt.db.Main, "tool_action_receipts"); got != 0 {
		t.Fatalf("tool_action_receipts = %d, want 0 for non-opt-in run", got)
	}
}

func TestDaemonTask_WithoutOptInUsesPlainRegistry(t *testing.T) {
	provider := &runtimeToolUseProvider{toolName: "glob", toolInput: `{"pattern":"*.definitely-no-match"}`}
	rt := newGatewayOptInRuntime(t, "gateway", provider)
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{Prompt: "read without daemon opt-in"})

	if _, err := rt.newDaemonTaskRunner()(ctx, payload, nil); err != nil {
		t.Fatalf("daemon runner: %v", err)
	}

	if got := runtimeCountRows(t, rt.db.Main, "policy_decisions"); got != 0 {
		t.Fatalf("policy_decisions = %d, want 0 for non-opt-in daemon task", got)
	}
	if got := runtimeCountRows(t, rt.db.Main, "tool_action_receipts"); got != 0 {
		t.Fatalf("tool_action_receipts = %d, want 0 for non-opt-in daemon task", got)
	}
}

func TestDaemonTask_WithGatewayOptInUsesToolGateway(t *testing.T) {
	provider := &runtimeToolUseProvider{toolName: "glob", toolInput: `{"pattern":"*.definitely-no-match"}`}
	rt := newGatewayOptInRuntime(t, "gateway", provider)
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:             "read with daemon gateway opt-in",
		AgenticEnforcement: config.AgenticEnforcementModeGateway,
	})

	if _, err := rt.newDaemonTaskRunner()(ctx, payload, nil); err != nil {
		t.Fatalf("daemon runner: %v", err)
	}

	if got := runtimeCountRows(t, rt.db.Main, "policy_decisions"); got != 1 {
		t.Fatalf("policy_decisions = %d, want 1", got)
	}
	if got := runtimeCountRows(t, rt.db.Main, "tool_action_receipts"); got != 1 {
		t.Fatalf("tool_action_receipts = %d, want 1", got)
	}
	receipt := runtimeLatestReceipt(t, rt.db.Main)
	if receipt.Status != agentic.ReceiptStatusSucceeded || receipt.ToolCallID != "runtime-tool-1" {
		t.Fatalf("receipt = %+v, want succeeded receipt for runtime-tool-1", receipt)
	}
}

func TestGatewayOptIn_ConfigObserveBlocksGatewayRequest(t *testing.T) {
	provider := &runtimeToolUseProvider{toolName: "glob", toolInput: `{"pattern":"*.definitely-no-match"}`}
	rt := newGatewayOptInRuntime(t, "observe", provider)
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:             "blocked by config maximum",
		AgenticEnforcement: config.AgenticEnforcementModeGateway,
	})

	_, err := rt.newDaemonTaskRunner()(ctx, payload, nil)
	if err == nil {
		t.Fatal("daemon runner error = nil, want config maximum failure")
	}
	if !strings.Contains(err.Error(), "agentic gateway enforcement requested") {
		t.Fatalf("daemon runner error = %q, want gateway enforcement config failure", err.Error())
	}
	if got := runtimeCountRows(t, rt.db.Main, "tool_action_receipts"); got != 0 {
		t.Fatalf("tool_action_receipts = %d, want 0", got)
	}
}

func TestGatewayOptIn_MissingTaskIDFailsClosed(t *testing.T) {
	provider := &runtimeToolUseProvider{toolName: "glob", toolInput: `{"pattern":"*.definitely-no-match"}`}
	rt := newGatewayOptInRuntime(t, "gateway", provider)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:             "missing agentic task id",
		AgenticEnforcement: config.AgenticEnforcementModeGateway,
	})

	_, err := rt.newDaemonTaskRunner()(context.Background(), payload, nil)
	if err == nil {
		t.Fatal("daemon runner error = nil, want missing task id failure")
	}
	if !strings.Contains(err.Error(), "agentic task id is required") {
		t.Fatalf("daemon runner error = %q, want missing task id failure", err.Error())
	}
	if got := runtimeCountRows(t, rt.db.Main, "tool_action_receipts"); got != 0 {
		t.Fatalf("tool_action_receipts = %d, want 0", got)
	}
}

func TestGatewayOptIn_ReadOnlyToolRecordsDecisionReceiptAndExecutes(t *testing.T) {
	provider := &runtimeToolUseProvider{toolName: "glob", toolInput: `{"pattern":"*.definitely-no-match"}`}
	rt := newGatewayOptInRuntime(t, "gateway", provider)
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:             "read with gateway",
		AgenticEnforcement: config.AgenticEnforcementModeGateway,
	})

	result, err := rt.newDaemonTaskRunner()(ctx, payload, nil)
	if err != nil {
		t.Fatalf("daemon runner: %v", err)
	}
	if !strings.Contains(result.Result, "done") {
		t.Fatalf("result = %q, want final model response", result.Result)
	}

	var decision string
	if err := rt.db.Main.QueryRow(`SELECT decision FROM policy_decisions ORDER BY id DESC LIMIT 1`).Scan(&decision); err != nil {
		t.Fatalf("latest policy decision: %v", err)
	}
	if decision != agentic.PolicyDecisionAuto {
		t.Fatalf("decision = %q, want %q", decision, agentic.PolicyDecisionAuto)
	}
	receipt := runtimeLatestReceipt(t, rt.db.Main)
	if receipt.Status != agentic.ReceiptStatusSucceeded || receipt.RawOutputHash == "" || receipt.VisibleOutputHash == "" {
		t.Fatalf("receipt = %+v, want completed succeeded receipt", receipt)
	}
}

func TestGatewayOptIn_MutatingToolRequiresApprovalAndDoesNotExecute(t *testing.T) {
	writePath := filepath.Join(t.TempDir(), "should-not-exist.txt")
	rt := newGatewayOptInRuntime(t, "gateway", &runtimeToolUseProvider{
		toolName:  "bash",
		toolInput: `{"command":"touch ` + writePath + `"}`,
	})
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:             "write with gateway",
		AgenticEnforcement: config.AgenticEnforcementModeGateway,
	})

	if _, err := rt.newDaemonTaskRunner()(ctx, payload, nil); err != nil {
		t.Fatalf("daemon runner: %v", err)
	}

	if got := runtimeCountRows(t, rt.db.Main, "approval_requests"); got != 1 {
		t.Fatalf("approval_requests = %d, want 1", got)
	}
	receipt := runtimeLatestReceipt(t, rt.db.Main)
	if receipt.Status != agentic.ReceiptStatusApprovalRequired || receipt.ApprovalRequestID == "" {
		t.Fatalf("receipt = %+v, want approval-required receipt", receipt)
	}
	if fileExists(writePath) {
		t.Fatalf("write_file executed at %s; want ToolGateway to block before execution", writePath)
	}
}

func TestGatewayOptIn_DoesNotGateQueueMarkDone(t *testing.T) {
	writePath := filepath.Join(t.TempDir(), "should-not-exist.txt")
	rt := newGatewayOptInRuntime(t, "gateway", &runtimeToolUseProvider{
		toolName:  "bash",
		toolInput: `{"command":"touch ` + writePath + `"}`,
		finalText: "workflow still completes after blocked tool result",
	})
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:             "approval-required tool should not gate daemon completion in this lane",
		AgenticEnforcement: config.AgenticEnforcementModeGateway,
	})

	result, err := rt.newDaemonTaskRunner()(ctx, payload, nil)
	if err != nil {
		t.Fatalf("daemon runner: %v", err)
	}
	if !strings.Contains(result.Result, "workflow still completes") {
		t.Fatalf("result = %q, want workflow completion unaffected by receipt state", result.Result)
	}
	if fileExists(writePath) {
		t.Fatalf("bash command executed at %s; want approval-required path blocked", writePath)
	}
}

func TestGatewayOptIn_DoesNotEnqueueProposedTasksOrWakeFollowups(t *testing.T) {
	provider := &runtimeToolUseProvider{toolName: "glob", toolInput: `{"pattern":"*.definitely-no-match"}`}
	rt := newGatewayOptInRuntime(t, "gateway", provider)
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:             "gateway should only receipt the tool call",
		AgenticEnforcement: config.AgenticEnforcementModeGateway,
	})

	if _, err := rt.newDaemonTaskRunner()(ctx, payload, nil); err != nil {
		t.Fatalf("daemon runner: %v", err)
	}
	if got := runtimeCountRows(t, rt.db.Main, "agentic_tasks"); got != 1 {
		t.Fatalf("agentic_tasks = %d, want existing task only", got)
	}
	if got := runtimeCountRows(t, rt.db.Main, "followups"); got != 0 {
		t.Fatalf("followups = %d, want 0", got)
	}
	for _, table := range []string{"verification_runs", "memory_updates"} {
		if got := runtimeCountRows(t, rt.db.Main, table); got != 0 {
			t.Fatalf("%s = %d, want 0", table, got)
		}
	}
}

func TestAgenticOperatorLineage_ShowsOptInReceipts(t *testing.T) {
	provider := &runtimeToolUseProvider{toolName: "glob", toolInput: `{"pattern":"*.definitely-no-match"}`}
	rt := newGatewayOptInRuntime(t, "gateway", provider)
	task := createRuntimeAgenticTask(t, rt)
	ctx := daemon.WithAgenticTaskID(context.Background(), task.ID)
	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:             "gateway receipt should appear in lineage",
		AgenticEnforcement: config.AgenticEnforcementModeGateway,
	})

	if _, err := rt.newDaemonTaskRunner()(ctx, payload, nil); err != nil {
		t.Fatalf("daemon runner: %v", err)
	}
	view, err := (&agenticCLI{db: rt.db.Main, store: rt.agenticStore}).lineage(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("lineage: %v", err)
	}
	if len(view.Receipts) != 1 {
		t.Fatalf("lineage receipts = %+v, want 1 opt-in receipt", view.Receipts)
	}
	if view.Receipts[0].ToolCallID != "runtime-tool-1" || view.Receipts[0].Status != agentic.ReceiptStatusSucceeded {
		t.Fatalf("lineage receipt = %+v, want succeeded runtime-tool-1 receipt", view.Receipts[0])
	}
}

func newGatewayOptInRuntime(t *testing.T, mode string, provider *runtimeToolUseProvider) *executionRuntime {
	t.Helper()
	t.Setenv("ELNATH_BENCHMARK_MODE", "1")
	return newTestExecutionRuntimeWithConfig(t, provider, true, func(cfg *config.Config) {
		cfg.Agentic.Enforcement.Mode = mode
	})
}

func createRuntimeAgenticTask(t *testing.T, rt *executionRuntime) *agentic.AgenticTask {
	t.Helper()
	task, err := rt.agenticStore.CreateAgenticTask(context.Background(), agentic.AgenticTask{
		Title:            "Runtime opt-in task",
		Prompt:           "runtime opt-in task",
		Status:           agentic.TaskStatusRunning,
		RiskLevel:        agentic.RiskLevelLow,
		AutonomyDecision: agentic.PolicyDecisionObserveOnly,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return task
}

func quoteJSON(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runtimeCountRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func runtimeLatestReceipt(t *testing.T, db *sql.DB) agentic.ToolActionReceipt {
	t.Helper()
	var r agentic.ToolActionReceipt
	if err := db.QueryRow(`
		SELECT id, status, tool_call_id, approval_request_id, raw_output_hash, visible_output_hash
		FROM tool_action_receipts ORDER BY id DESC LIMIT 1
	`).Scan(&r.ID, &r.Status, &r.ToolCallID, &r.ApprovalRequestID, &r.RawOutputHash, &r.VisibleOutputHash); err != nil {
		t.Fatalf("latest receipt: %v", err)
	}
	return r
}
