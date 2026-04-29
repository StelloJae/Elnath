package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/agentic"
	agenticapprovals "github.com/stello/elnath/internal/agentic/approvals"
	agenticpolicy "github.com/stello/elnath/internal/agentic/policy"
	agentictools "github.com/stello/elnath/internal/agentic/tools"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
	basetools "github.com/stello/elnath/internal/tools"

	_ "modernc.org/sqlite"
)

type contextSpyExecutor struct {
	seen agentictools.Context
	ok   bool
}

func (e *contextSpyExecutor) Execute(ctx context.Context, name string, params json.RawMessage) (*basetools.Result, error) {
	e.seen, e.ok = agentictools.ContextFrom(ctx)
	return basetools.SuccessResult("ok"), nil
}

func TestAgentExecutor_ToolCallContextCarriesToolUseID(t *testing.T) {
	reg := basetools.NewRegistry()
	reg.Register(&fakeTool{name: "read_file", safe: true, reversible: true, scope: basetools.ToolScope{ReadPaths: []string{filepath.Join(t.TempDir(), "input")}}})
	spy := &contextSpyExecutor{}
	a := New(&mockProvider{}, reg, WithPermission(NewPermission(WithMode(ModeBypass))), WithToolExecutor(spy))

	ctx := agentictools.WithContext(context.Background(), agentictools.Context{TaskID: 42, ActorID: 7, ActionKind: "observe"})
	if _, err := a.executeTools(ctx, nil, []llm.ToolUseBlock{{ID: "tool-use-123", Name: "read_file", Input: json.RawMessage(`{}`)}}, nil); err != nil {
		t.Fatalf("executeTools: %v", err)
	}

	if !spy.ok {
		t.Fatal("executor did not receive agentic tool context")
	}
	if spy.seen.TaskID != 42 || spy.seen.ActorID != 7 || spy.seen.ToolCallID != "tool-use-123" || spy.seen.ActionKind != "observe" {
		t.Fatalf("context = %+v, want task/actor/tool call/action", spy.seen)
	}
}

func TestToolGateway_HookTransformedOutputPreservesRawAndVisibleHashes(t *testing.T) {
	ctx := context.Background()
	db, store, bridge, task := newAgenticGatewayAgentTest(t)

	reg := basetools.NewRegistry()
	reg.Register(&fakeTool{name: "read_file", safe: true, reversible: true, scope: basetools.ToolScope{ReadPaths: []string{filepath.Join(t.TempDir(), "input")}}})

	gateway := agentictools.NewGateway(reg, store, agenticpolicy.NewEvaluator(), bridge)
	hooks := NewHookRegistry()
	hooks.Add(&mutateResultHook{})

	a := New(&mockProvider{}, reg, WithPermission(NewPermission(WithMode(ModeBypass))), WithToolExecutor(gateway), WithHooks(hooks))
	toolCtx := agentictools.WithContext(ctx, agentictools.Context{TaskID: task.ID, ToolCallID: "outer-will-be-overwritten", ActionKind: "observe"})
	messages, err := a.executeTools(toolCtx, nil, []llm.ToolUseBlock{{ID: "tool-use-redact", Name: "read_file", Input: json.RawMessage(`{}`)}}, nil)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	blocks := toolResultBlocks(t, messages)
	if len(blocks) != 1 || blocks[0].Content != "hook redacted" {
		t.Fatalf("tool result blocks = %+v, want hook redacted", blocks)
	}

	receipt := latestAgenticReceipt(t, db)
	if receipt.ToolCallID != "tool-use-redact" {
		t.Fatalf("tool_call_id = %q, want tool-use-redact", receipt.ToolCallID)
	}
	if receipt.RawOutputHash == "" || receipt.VisibleOutputHash == "" || receipt.RawOutputHash == receipt.VisibleOutputHash {
		t.Fatalf("raw/visible hashes = %q/%q, want distinct non-empty hashes", receipt.RawOutputHash, receipt.VisibleOutputHash)
	}
	if receipt.OutputHash != receipt.VisibleOutputHash {
		t.Fatalf("output_hash = %q, want visible hash %q", receipt.OutputHash, receipt.VisibleOutputHash)
	}
	if receipt.OutputSummary != "hook redacted" || strings.Contains(receipt.OutputSummary, "read_file ok") {
		t.Fatalf("output summary = %q, want visible redacted output only", receipt.OutputSummary)
	}
	if !strings.Contains(receipt.HookProvenanceJSON, "transformed") {
		t.Fatalf("hook provenance = %q, want transformation evidence", receipt.HookProvenanceJSON)
	}
}

type failingFinalizerExecutor struct{}

func (e *failingFinalizerExecutor) Execute(context.Context, string, json.RawMessage) (*basetools.Result, error) {
	return basetools.SuccessResult("raw secret"), nil
}

func (e *failingFinalizerExecutor) FinalizeToolResult(context.Context, string, json.RawMessage, *basetools.Result) error {
	return errors.New("receipt finalizer down")
}

func TestAgentExecutor_FinalizerErrorReturnsToolError(t *testing.T) {
	reg := basetools.NewRegistry()
	reg.Register(&fakeTool{name: "read_file", safe: true, reversible: true, scope: basetools.ToolScope{ReadPaths: []string{filepath.Join(t.TempDir(), "input")}}})
	a := New(&mockProvider{}, reg, WithPermission(NewPermission(WithMode(ModeBypass))), WithToolExecutor(&failingFinalizerExecutor{}))

	ctx := agentictools.WithContext(context.Background(), agentictools.Context{TaskID: 42, ActionKind: "observe"})
	messages, err := a.executeTools(ctx, nil, []llm.ToolUseBlock{{ID: "tool-use-finalizer", Name: "read_file", Input: json.RawMessage(`{}`)}}, nil)
	if err != nil {
		t.Fatalf("executeTools: %v", err)
	}
	blocks := toolResultBlocks(t, messages)
	if len(blocks) != 1 || !blocks[0].IsError || !strings.Contains(blocks[0].Content, "tool result finalizer error") {
		t.Fatalf("tool result blocks = %+v, want finalizer error tool result", blocks)
	}
}

func newAgenticGatewayAgentTest(t *testing.T) (*sql.DB, *agentic.Store, *agenticapprovals.Bridge, *agentic.AgenticTask) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
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
		Title:              "Run read",
		Prompt:             "Read state.",
		Status:             agentic.TaskStatusProposed,
		Priority:           1,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		t.Fatalf("CreateAgenticTask: %v", err)
	}
	return db, store, agenticapprovals.NewBridge(db, store, approvalStore), task
}

func latestAgenticReceipt(t *testing.T, db *sql.DB) agentic.ToolActionReceipt {
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
