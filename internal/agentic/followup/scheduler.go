package followup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/secret"
)

const (
	SourceFollowup  = "followup"
	TypeFollowupDue = "followup_due"
)

type Result struct {
	Processed int
	Created   int
	Skipped   int
	Failed    int
}

type Scheduler struct {
	store *agentic.Store
	now   func() time.Time
}

func NewScheduler(store *agentic.Store) *Scheduler {
	return &Scheduler{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Scheduler) RunOnce(ctx context.Context, limit int) (Result, error) {
	if s == nil || s.store == nil {
		return Result{}, errors.New("followup: nil store")
	}
	due, err := s.store.ListDueFollowups(ctx, s.now(), limit)
	if err != nil {
		return Result{}, err
	}
	var result Result
	for _, fu := range due {
		result.Processed++
		outcome, err := s.process(ctx, fu)
		switch outcome {
		case agentic.FollowupStatusCreated:
			result.Created++
		case agentic.FollowupStatusSkipped:
			result.Skipped++
		case agentic.FollowupStatusFailed:
			result.Failed++
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Scheduler) process(ctx context.Context, fu agentic.Followup) (string, error) {
	if fu.CreatedTaskID != 0 {
		if _, err := s.store.MarkFollowupCreated(ctx, fu.ID, fu.CreatedTaskID); err != nil {
			return agentic.FollowupStatusFailed, err
		}
		return agentic.FollowupStatusCreated, nil
	}
	if !fu.WakeAgent {
		if _, err := s.store.MarkFollowupSkipped(ctx, fu.ID, "wakeAgent=false"); err != nil {
			return agentic.FollowupStatusFailed, err
		}
		return agentic.FollowupStatusSkipped, nil
	}
	if _, err := s.store.MarkFollowupProcessing(ctx, fu.ID); err != nil {
		return agentic.FollowupStatusFailed, err
	}
	signal, err := s.createOrGetDueSignal(ctx, fu)
	if err != nil {
		return s.fail(ctx, fu.ID, err)
	}
	if task, err := s.store.GetAgenticTaskBySignalID(ctx, signal.ID); err == nil {
		if _, markErr := s.store.MarkFollowupCreated(ctx, fu.ID, task.ID); markErr != nil {
			return agentic.FollowupStatusFailed, markErr
		}
		return agentic.FollowupStatusCreated, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return s.fail(ctx, fu.ID, err)
	}
	task, err := s.store.CreateAgenticTask(ctx, taskFromFollowup(fu, signal.ID))
	if err != nil {
		if task, getErr := s.store.GetAgenticTaskBySignalID(ctx, signal.ID); getErr == nil {
			if _, markErr := s.store.MarkFollowupCreated(ctx, fu.ID, task.ID); markErr != nil {
				return agentic.FollowupStatusFailed, markErr
			}
			return agentic.FollowupStatusCreated, nil
		}
		return s.fail(ctx, fu.ID, err)
	}
	if _, err := s.store.MarkFollowupCreated(ctx, fu.ID, task.ID); err != nil {
		return agentic.FollowupStatusFailed, err
	}
	return agentic.FollowupStatusCreated, nil
}

func (s *Scheduler) fail(ctx context.Context, followupID int64, cause error) (string, error) {
	reason := cause.Error()
	if _, err := s.store.MarkFollowupFailed(ctx, followupID, reason); err != nil {
		return agentic.FollowupStatusFailed, fmt.Errorf("%w; mark failed: %v", cause, err)
	}
	return agentic.FollowupStatusFailed, cause
}

func (s *Scheduler) createOrGetDueSignal(ctx context.Context, fu agentic.Followup) (*agentic.GoalSignal, error) {
	payload := map[string]any{
		"followup_id":    fu.ID,
		"parent_task_id": fu.TaskID,
		"reason_hash":    hashString(fu.Reason),
		"wake_agent":     fu.WakeAgent,
		"trigger_at":     fu.TriggerAt.UTC().UnixMilli(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("followup: encode signal payload: %w", err)
	}
	dedupeKey := fu.DedupeKey
	if dedupeKey == "" {
		dedupeKey = fmt.Sprintf("followup:%d:due", fu.ID)
	}
	signal, _, err := s.store.CreateOrGetGoalSignal(ctx, agentic.GoalSignal{
		GoalID:      fu.GoalID,
		Source:      SourceFollowup,
		Type:        TypeFollowupDue,
		PayloadJSON: string(body),
		Fingerprint: fingerprint(SourceFollowup, TypeFollowupDue, dedupeKey, string(body)),
		Severity:    1,
		Status:      agentic.SignalStatusNew,
		DedupeKey:   dedupeKey,
		ObservedAt:  s.now(),
	})
	if err != nil {
		return nil, err
	}
	return signal, nil
}

func taskFromFollowup(fu agentic.Followup, signalID int64) agentic.AgenticTask {
	return agentic.AgenticTask{
		GoalID:             fu.GoalID,
		SignalID:           signalID,
		ParentID:           fu.TaskID,
		Title:              "Proposed follow-up task",
		Prompt:             fmt.Sprintf("Follow-up proposal from verified task %d: %s. Review the parent task and decide the next bounded action. No daemon queue work has been enqueued.", fu.TaskID, safeReason(fu.Reason)),
		Status:             agentic.TaskStatusProposed,
		Priority:           0,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
		DueAt:              sql.NullTime{Time: fu.TriggerAt, Valid: !fu.TriggerAt.IsZero()},
	}
}

func safeReason(reason string) string {
	return snippet(secret.NewDetector().RedactString(reason), 300)
}

func snippet(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func fingerprint(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
