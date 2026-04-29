package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"
)

type Bridge struct {
	db            *sql.DB
	store         *agentic.Store
	approvalStore *daemon.ApprovalStore
}

type Request struct {
	TaskID           int64
	ActorID          int64
	PolicyDecisionID int64
	ToolName         string
	Input            json.RawMessage
}

func NewBridge(db *sql.DB, store *agentic.Store, approvalStore *daemon.ApprovalStore) *Bridge {
	return &Bridge{db: db, store: store, approvalStore: approvalStore}
}

func (b *Bridge) CreateApproval(ctx context.Context, req Request) (*daemon.ApprovalRequest, error) {
	return b.createApproval(ctx, req, true)
}

func (b *Bridge) createApproval(ctx context.Context, req Request, retryUnique bool) (*daemon.ApprovalRequest, error) {
	if b == nil || b.db == nil || b.store == nil || b.approvalStore == nil {
		return nil, errors.New("approvals: bridge is not configured")
	}
	if req.TaskID == 0 {
		return nil, errors.New("approvals: task_id is required")
	}
	if req.PolicyDecisionID == 0 {
		return nil, errors.New("approvals: policy_decision_id is required")
	}

	decision, err := b.store.GetPolicyDecision(ctx, req.PolicyDecisionID)
	if err != nil {
		return nil, fmt.Errorf("approvals: load policy decision: %w", err)
	}
	if decision.TaskID != req.TaskID {
		return nil, fmt.Errorf("approvals: policy decision task_id %d does not match request task_id %d", decision.TaskID, req.TaskID)
	}
	if decision.Decision != agentic.PolicyDecisionApprovalRequired {
		return nil, fmt.Errorf("approvals: policy decision %d is %q, want %q", decision.ID, decision.Decision, agentic.PolicyDecisionApprovalRequired)
	}

	actorID := req.ActorID
	if actorID == 0 {
		actorID = decision.ActorID
	}
	toolName := req.ToolName
	if toolName == "" {
		toolName = decision.ToolName
	}

	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("approvals: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if existing, err := b.approvalStore.GetPendingByPolicyDecisionTx(ctx, tx, decision.ID); err == nil {
		if _, err := b.store.SetAgenticTaskApprovalRequestIDTx(ctx, tx, req.TaskID, existing.IDString()); err != nil {
			return nil, fmt.Errorf("approvals: link existing approval to task: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("approvals: commit existing approval link: %w", err)
		}
		return existing, nil
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("approvals: find pending policy approval: %w", err)
	}

	approval, err := b.approvalStore.CreateWithContextTx(ctx, tx, daemon.ApprovalCreateRequest{
		ToolName:         toolName,
		Input:            req.Input,
		TaskID:           req.TaskID,
		PolicyDecisionID: decision.ID,
		ActorID:          actorID,
		ActionKind:       decision.ActionKind,
		RiskLevel:        decision.RiskLevel,
		Reason:           decision.Reason,
		PolicyVersion:    decision.PolicyVersion,
	})
	if err != nil {
		if retryUnique && isPendingApprovalUniqueConflict(err) {
			_ = tx.Rollback()
			return b.createApproval(ctx, req, false)
		}
		return nil, err
	}
	if _, err := b.store.SetAgenticTaskApprovalRequestIDTx(ctx, tx, req.TaskID, approval.IDString()); err != nil {
		return nil, fmt.Errorf("approvals: link approval to task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("approvals: commit: %w", err)
	}
	return approval, nil
}

func isPendingApprovalUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "approval_requests_policy_decision_pending") ||
		strings.Contains(msg, "UNIQUE constraint failed: approval_requests.policy_decision_id")
}
