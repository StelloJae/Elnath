package agentictools

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/agentic/approvals"
	"github.com/stello/elnath/internal/agentic/policy"
	"github.com/stello/elnath/internal/daemon"
	basetools "github.com/stello/elnath/internal/tools"
)

type receiptStore interface {
	CreatePolicyDecision(context.Context, agentic.PolicyDecisionRecord) (*agentic.PolicyDecisionRecord, error)
	CreateToolActionReceipt(context.Context, agentic.ToolActionReceipt) (*agentic.ToolActionReceipt, error)
	CompleteToolActionReceipt(context.Context, int64, agentic.ToolActionReceiptCompletion) (*agentic.ToolActionReceipt, error)
}

type reusableApprovalStore interface {
	FindReusableApprovalRequestID(context.Context, int64, int64, string, string) (string, error)
}

type approvalCreator interface {
	CreateApproval(context.Context, approvals.Request) (*daemon.ApprovalRequest, error)
}

type toolLookup interface {
	Get(name string) (basetools.Tool, bool)
}

type Gateway struct {
	executor  basetools.Executor
	store     receiptStore
	evaluator *policy.Evaluator
	approvals approvalCreator

	pending sync.Map
}

type pendingReceipt struct {
	id         int64
	rawHash    string
	reversible bool
}

func NewGateway(exec basetools.Executor, store receiptStore, evaluator *policy.Evaluator, approvals approvalCreator) *Gateway {
	if evaluator == nil {
		evaluator = policy.NewEvaluator()
	}
	return &Gateway{
		executor:  exec,
		store:     store,
		evaluator: evaluator,
		approvals: approvals,
	}
}

func (g *Gateway) Execute(ctx context.Context, name string, params json.RawMessage) (*basetools.Result, error) {
	toolCtx, ok := ContextFrom(ctx)
	if !ok || toolCtx.TaskID == 0 {
		return basetools.ErrorResult("agentic tool context task_id is required"), nil
	}
	if g == nil || g.executor == nil || g.store == nil || g.evaluator == nil {
		return basetools.ErrorResult("agentic tool gateway is not configured"), nil
	}

	actionKind := toolCtx.ActionKind
	if actionKind == "" {
		actionKind = defaultActionKind(name)
	}
	policyResult, err := g.evaluator.Evaluate(policy.Request{
		TaskID:     toolCtx.TaskID,
		ActorID:    toolCtx.ActorID,
		ActionKind: actionKind,
		ToolName:   name,
		Input:      params,
	})
	if err != nil {
		return basetools.ErrorResult("agentic policy evaluation failed: " + err.Error()), nil
	}
	decision, err := g.store.CreatePolicyDecision(ctx, agentic.PolicyDecisionRecord{
		TaskID:        policyResult.TaskID,
		ActorID:       policyResult.ActorID,
		ActionKind:    policyResult.ActionKind,
		ToolName:      policyResult.ToolName,
		RiskLevel:     policyResult.RiskLevel,
		Decision:      policyResult.Decision,
		Reason:        policyResult.Reason,
		PolicyVersion: policyResult.PolicyVersion,
	})
	if err != nil {
		return basetools.ErrorResult("agentic policy decision record failed: " + err.Error()), nil
	}

	switch decision.Decision {
	case agentic.PolicyDecisionAuto:
		return g.executeAllowed(ctx, toolCtx, decision, name, params)
	case agentic.PolicyDecisionObserveOnly:
		if !isReadOnlyTool(name) {
			return g.block(ctx, toolCtx, decision, name, params, agentic.ReceiptStatusDenied, "observe_only policy cannot execute mutating tool")
		}
		return g.executeAllowed(ctx, toolCtx, decision, name, params)
	case agentic.PolicyDecisionApprovalRequired:
		return g.requireApproval(ctx, toolCtx, decision, name, params)
	case agentic.PolicyDecisionDenied:
		return g.block(ctx, toolCtx, decision, name, params, agentic.ReceiptStatusDenied, decision.Reason)
	default:
		return g.block(ctx, toolCtx, decision, name, params, agentic.ReceiptStatusDenied, "unknown policy decision: "+decision.Decision)
	}
}

func (g *Gateway) executeAllowed(ctx context.Context, toolCtx Context, decision *agentic.PolicyDecisionRecord, name string, params json.RawMessage) (*basetools.Result, error) {
	receipt, err := g.store.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:           toolCtx.TaskID,
		ActorID:          toolCtx.ActorID,
		PolicyDecisionID: decision.ID,
		ToolName:         name,
		ToolCallID:       toolCtx.ToolCallID,
		InputHash:        hashBytes(params),
		Status:           agentic.ReceiptStatusStarted,
		Reversible:       g.reversible(name, params),
	})
	if err != nil {
		return basetools.ErrorResult("agentic receipt creation failed: " + err.Error()), nil
	}

	result, execErr := g.executor.Execute(ctx, name, params)
	if execErr != nil {
		result = basetools.ErrorResult(execErr.Error())
	}
	if result == nil {
		result = basetools.ErrorResult("tool returned nil result")
	}
	status := agentic.ReceiptStatusSucceeded
	failureReason := ""
	if execErr != nil || result.IsError {
		status = agentic.ReceiptStatusFailed
		failureReason = strings.TrimSpace(result.Output)
	}
	rawHash := hashString(result.Output)
	if toolCtx.FinalizeResult && toolCtx.ToolCallID != "" {
		g.pending.Store(receiptKey(toolCtx), pendingReceipt{id: receipt.ID, rawHash: rawHash, reversible: g.reversible(name, params)})
		return result, nil
	}
	_, err = g.store.CompleteToolActionReceipt(ctx, receipt.ID, agentic.ToolActionReceiptCompletion{
		OutputHash:        rawHash,
		RawOutputHash:     rawHash,
		VisibleOutputHash: rawHash,
		OutputSummary:     summarizeOutput(result.Output),
		Status:            status,
		FailureReason:     failureReason,
		Reversible:        g.reversible(name, params),
	})
	if err != nil {
		return basetools.ErrorResult("agentic receipt completion failed: " + err.Error()), nil
	}
	return result, nil
}

func (g *Gateway) requireApproval(ctx context.Context, toolCtx Context, decision *agentic.PolicyDecisionRecord, name string, params json.RawMessage) (*basetools.Result, error) {
	inputHash := hashBytes(params)
	receipt, err := g.createStartedReceipt(ctx, toolCtx, decision, name, params)
	if err != nil {
		return basetools.ErrorResult("agentic receipt creation failed: " + err.Error()), nil
	}
	if approvalID, ok := g.reusableApprovalID(ctx, toolCtx, name, inputHash); ok {
		msg := fmt.Sprintf("approval required: approval_request_id=%s", approvalID)
		return g.completeBlockedReceipt(ctx, receipt.ID, approvalID, agentic.ReceiptStatusApprovalRequired, msg)
	}
	if g.approvals == nil {
		return g.completeBlockedReceipt(ctx, receipt.ID, "", agentic.ReceiptStatusApprovalRequired, "approval bridge is not configured")
	}
	approval, err := g.approvals.CreateApproval(ctx, approvals.Request{
		TaskID:           toolCtx.TaskID,
		ActorID:          toolCtx.ActorID,
		PolicyDecisionID: decision.ID,
		ToolName:         name,
		Input:            params,
	})
	if err != nil {
		return g.completeBlockedReceipt(ctx, receipt.ID, "", agentic.ReceiptStatusApprovalRequired, "approval request failed: "+err.Error())
	}
	msg := fmt.Sprintf("approval required: approval_request_id=%s", approval.IDString())
	return g.completeBlockedReceipt(ctx, receipt.ID, approval.IDString(), agentic.ReceiptStatusApprovalRequired, msg)
}

func (g *Gateway) reusableApprovalID(ctx context.Context, toolCtx Context, name, inputHash string) (string, bool) {
	store, ok := g.store.(reusableApprovalStore)
	if !ok {
		return "", false
	}
	id, err := store.FindReusableApprovalRequestID(ctx, toolCtx.TaskID, toolCtx.ActorID, name, inputHash)
	return id, err == nil && id != ""
}

func (g *Gateway) completeBlockedReceipt(ctx context.Context, receiptID int64, approvalID, status, reason string) (*basetools.Result, error) {
	hash := hashString(reason)
	_, err := g.store.CompleteToolActionReceipt(ctx, receiptID, agentic.ToolActionReceiptCompletion{
		ApprovalRequestID:  approvalID,
		OutputHash:         hash,
		RawOutputHash:      hash,
		VisibleOutputHash:  hash,
		OutputSummary:      summarizeOutput(reason),
		Status:             status,
		FailureReason:      reason,
		HookProvenanceJSON: "",
		Reversible:         false,
	})
	if err != nil {
		return basetools.ErrorResult("agentic receipt completion failed: " + err.Error()), nil
	}
	return basetools.ErrorResult(reason), nil
}

func (g *Gateway) block(ctx context.Context, toolCtx Context, decision *agentic.PolicyDecisionRecord, name string, params json.RawMessage, status, reason string) (*basetools.Result, error) {
	if strings.TrimSpace(reason) == "" {
		reason = status
	}
	if _, err := g.createBlockedReceipt(ctx, toolCtx, decision, name, params, "", status, reason); err != nil {
		return basetools.ErrorResult("agentic receipt creation failed: " + err.Error()), nil
	}
	output := reason
	if status == agentic.ReceiptStatusDenied && !strings.Contains(strings.ToLower(output), "denied") {
		output = "denied: " + output
	}
	return basetools.ErrorResult(output), nil
}

func (g *Gateway) createStartedReceipt(ctx context.Context, toolCtx Context, decision *agentic.PolicyDecisionRecord, name string, params json.RawMessage) (*agentic.ToolActionReceipt, error) {
	return g.store.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:           toolCtx.TaskID,
		ActorID:          toolCtx.ActorID,
		PolicyDecisionID: decision.ID,
		ToolName:         name,
		ToolCallID:       toolCtx.ToolCallID,
		InputHash:        hashBytes(params),
		Status:           agentic.ReceiptStatusStarted,
		Reversible:       false,
	})
}

func (g *Gateway) createBlockedReceipt(ctx context.Context, toolCtx Context, decision *agentic.PolicyDecisionRecord, name string, params json.RawMessage, approvalID, status, reason string) (*agentic.ToolActionReceipt, error) {
	hash := hashString(reason)
	return g.store.CreateToolActionReceipt(ctx, agentic.ToolActionReceipt{
		TaskID:             toolCtx.TaskID,
		ActorID:            toolCtx.ActorID,
		PolicyDecisionID:   decision.ID,
		ApprovalRequestID:  approvalID,
		ToolName:           name,
		ToolCallID:         toolCtx.ToolCallID,
		InputHash:          hashBytes(params),
		OutputHash:         hash,
		RawOutputHash:      hash,
		VisibleOutputHash:  hash,
		OutputSummary:      summarizeOutput(reason),
		Status:             status,
		FailureReason:      reason,
		HookProvenanceJSON: "",
		Reversible:         false,
		CompletedAt:        sqlNullNow(),
	})
}

func (g *Gateway) FinalizeToolResult(ctx context.Context, name string, params json.RawMessage, result *basetools.Result) error {
	toolCtx, ok := ContextFrom(ctx)
	if !ok || toolCtx.ToolCallID == "" {
		return nil
	}
	raw, ok := g.pending.LoadAndDelete(receiptKey(toolCtx))
	if !ok {
		return nil
	}
	pending, ok := raw.(pendingReceipt)
	if !ok {
		return errors.New("agentic gateway pending receipt has unexpected type")
	}
	if result == nil {
		result = basetools.ErrorResult("tool returned nil result")
	}
	visibleHash := hashString(result.Output)
	status := agentic.ReceiptStatusSucceeded
	failureReason := ""
	if result.IsError {
		status = agentic.ReceiptStatusFailed
		failureReason = strings.TrimSpace(result.Output)
	}
	provenance := ""
	if pending.rawHash != visibleHash {
		provenance = `{"transformed":true}`
	}
	_, err := g.store.CompleteToolActionReceipt(ctx, pending.id, agentic.ToolActionReceiptCompletion{
		OutputHash:         visibleHash,
		RawOutputHash:      pending.rawHash,
		VisibleOutputHash:  visibleHash,
		OutputSummary:      summarizeOutput(result.Output),
		Status:             status,
		FailureReason:      failureReason,
		HookProvenanceJSON: provenance,
		Reversible:         pending.reversible,
	})
	return err
}

func (g *Gateway) reversible(name string, params json.RawMessage) bool {
	getter, ok := g.executor.(toolLookup)
	if !ok {
		return false
	}
	tool, ok := getter.Get(name)
	if !ok {
		return false
	}
	return tool.Reversible()
}

func defaultActionKind(name string) string {
	if isReadOnlyTool(name) {
		return "observe"
	}
	return "mutate"
}

func isReadOnlyTool(name string) bool {
	switch name {
	case "read_file", "glob", "grep", "web_fetch", "web_search",
		"wiki_search", "wiki_read",
		"conversation_search", "cross_project_search", "cross_project_conversation_search",
		ActorGraphToolName, TaskEvidenceToolName, DelegateListToolName, ActorMessageListToolName:
		return true
	}
	return false
}

func receiptKey(c Context) string {
	return fmt.Sprintf("%d:%d:%s", c.TaskID, c.ActorID, c.ToolCallID)
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashString(s string) string {
	return hashBytes([]byte(s))
}

func sqlNullNow() sql.NullTime {
	return sql.NullTime{Time: time.Now(), Valid: true}
}

func summarizeOutput(out string) string {
	out = strings.TrimSpace(out)
	if len(out) <= 200 {
		return out
	}
	return out[:200]
}
