package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/stello/elnath/internal/agentic"
)

const (
	SourceAgentic      = "agentic"
	SourceUserExplicit = "user_explicit"

	TargetLearningLesson = "learning.lesson"
	TargetWikiPage       = "wiki.page"
	OperationAppend      = "append"
	OperationUpsert      = "upsert"
)

var (
	ErrBlocked       = errors.New("memory gate: blocked")
	ErrMissingTaskID = errors.New("memory gate: missing task_id")
	ErrNilStore      = errors.New("memory gate: nil store")
)

type Store interface {
	CreateMemoryUpdate(context.Context, agentic.MemoryUpdate) (*agentic.MemoryUpdate, error)
	FindMemoryUpdate(context.Context, agentic.MemoryUpdate) (*agentic.MemoryUpdate, error)
	ListVerificationRunsByTask(context.Context, int64) ([]agentic.VerificationRun, error)
	UpdateMemoryUpdateStatus(context.Context, int64, string, string, sql.NullTime) (*agentic.MemoryUpdate, error)
}

type Gate struct {
	store Store
	now   func() time.Time
}

type Request struct {
	TaskID            int64
	ReceiptID         int64
	VerificationRunID int64
	Target            string
	Operation         string
	PayloadHash       string
	Source            string
	Reason            string
}

type Decision struct {
	Allowed         bool
	Reason          string
	VerificationRun *agentic.VerificationRun
	Update          *agentic.MemoryUpdate
}

func NewGate(store Store) *Gate {
	return &Gate{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (g *Gate) Decide(ctx context.Context, req Request) (*Decision, error) {
	if g == nil || g.store == nil {
		return nil, ErrNilStore
	}
	if req.TaskID == 0 {
		return nil, ErrMissingTaskID
	}
	if req.Source == "" {
		req.Source = SourceAgentic
	}
	if req.Target == "" || req.Operation == "" || req.PayloadHash == "" {
		return nil, errors.New("memory gate: target, operation, and payload_hash are required")
	}

	var run *agentic.VerificationRun
	if req.Source == SourceUserExplicit {
		if existing, err := g.findUpdate(ctx, req, agentic.MemoryUpdateStatusApplied); err == nil {
			return &Decision{Allowed: true, Reason: "already applied", Update: existing}, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if req.Reason == "" {
			req.Reason = "explicit user memory"
		}
		return g.createPending(ctx, req, nil)
	}

	run, blockedReason, err := g.latestVerification(ctx, req)
	if err != nil {
		return nil, err
	}
	if blockedReason != "" {
		blockedReq := req
		blockedReq.VerificationRunID = verificationID(run)
		if existing, err := g.findUpdate(ctx, blockedReq, agentic.MemoryUpdateStatusBlocked); err == nil {
			return &Decision{Allowed: false, Reason: existing.Reason, VerificationRun: run, Update: existing}, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		update, err := g.store.CreateMemoryUpdate(ctx, agentic.MemoryUpdate{
			TaskID:            req.TaskID,
			ReceiptID:         req.ReceiptID,
			VerificationRunID: blockedReq.VerificationRunID,
			Target:            req.Target,
			Operation:         req.Operation,
			PayloadHash:       req.PayloadHash,
			Status:            agentic.MemoryUpdateStatusBlocked,
			Source:            req.Source,
			Reason:            blockedReason,
		})
		if err != nil {
			return nil, fmt.Errorf("memory gate: record blocked update: %w", err)
		}
		return &Decision{Allowed: false, Reason: blockedReason, VerificationRun: run, Update: update}, nil
	}

	req.VerificationRunID = run.ID
	if req.Reason == "" {
		req.Reason = "verification passed"
	}
	if existing, err := g.findUpdate(ctx, req, agentic.MemoryUpdateStatusApplied); err == nil {
		return &Decision{Allowed: true, Reason: "already applied", VerificationRun: run, Update: existing}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return g.createPending(ctx, req, run)
}

func (g *Gate) findUpdate(ctx context.Context, req Request, status string) (*agentic.MemoryUpdate, error) {
	existing, err := g.store.FindMemoryUpdate(ctx, agentic.MemoryUpdate{
		TaskID:            req.TaskID,
		VerificationRunID: req.VerificationRunID,
		Target:            req.Target,
		Operation:         req.Operation,
		PayloadHash:       req.PayloadHash,
		Status:            status,
		Source:            req.Source,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("memory gate: find existing update: %w", err)
	}
	return existing, nil
}

func (g *Gate) MarkApplied(ctx context.Context, updateID int64, reason string) (*agentic.MemoryUpdate, error) {
	if g == nil || g.store == nil {
		return nil, ErrNilStore
	}
	if reason == "" {
		reason = "memory write applied"
	}
	return g.store.UpdateMemoryUpdateStatus(ctx, updateID, agentic.MemoryUpdateStatusApplied, reason, sql.NullTime{Time: g.now(), Valid: true})
}

func (g *Gate) MarkFailed(ctx context.Context, updateID int64, reason string) (*agentic.MemoryUpdate, error) {
	if g == nil || g.store == nil {
		return nil, ErrNilStore
	}
	return g.store.UpdateMemoryUpdateStatus(ctx, updateID, agentic.MemoryUpdateStatusFailed, reason, sql.NullTime{})
}

func (g *Gate) createPending(ctx context.Context, req Request, run *agentic.VerificationRun) (*Decision, error) {
	if existing, err := g.findUpdate(ctx, req, agentic.MemoryUpdateStatusPending); err == nil {
		return &Decision{Allowed: true, Reason: existing.Reason, VerificationRun: run, Update: existing}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	update, err := g.store.CreateMemoryUpdate(ctx, agentic.MemoryUpdate{
		TaskID:            req.TaskID,
		ReceiptID:         req.ReceiptID,
		VerificationRunID: req.VerificationRunID,
		Target:            req.Target,
		Operation:         req.Operation,
		PayloadHash:       req.PayloadHash,
		Status:            agentic.MemoryUpdateStatusPending,
		Source:            req.Source,
		Reason:            req.Reason,
	})
	if err != nil {
		return nil, fmt.Errorf("memory gate: create pending update: %w", err)
	}
	return &Decision{Allowed: true, Reason: "allowed", VerificationRun: run, Update: update}, nil
}

func (g *Gate) latestVerification(ctx context.Context, req Request) (*agentic.VerificationRun, string, error) {
	runs, err := g.store.ListVerificationRunsByTask(ctx, req.TaskID)
	if err != nil {
		return nil, "", fmt.Errorf("memory gate: list verification runs: %w", err)
	}
	var latest *agentic.VerificationRun
	for i := range runs {
		run := runs[i]
		if req.VerificationRunID != 0 && run.ID != req.VerificationRunID {
			continue
		}
		if latest == nil || run.ID > latest.ID {
			latest = &run
		}
	}
	if latest == nil {
		return nil, "missing verification run", nil
	}
	switch latest.Verdict {
	case agentic.VerificationVerdictPassed:
		return latest, "", nil
	case agentic.VerificationVerdictFailed, agentic.VerificationVerdictInconclusive:
		return latest, "verification " + latest.Verdict, nil
	default:
		return latest, "verification invalid", nil
	}
}

func verificationID(run *agentic.VerificationRun) int64 {
	if run == nil {
		return 0
	}
	return run.ID
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
