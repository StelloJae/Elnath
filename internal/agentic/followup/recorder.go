package followup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/secret"
)

var (
	ErrMissingTaskID         = errors.New("followup: missing task_id")
	ErrVerificationNotPassed = errors.New("followup: verification not passed")
)

type Recorder struct {
	store *agentic.Store
}

type CreateRequest struct {
	TaskID    int64
	GoalID    int64
	Reason    string
	TriggerAt time.Time
	WakeAgent bool
	Cooldown  time.Duration
}

func NewRecorder(store *agentic.Store) *Recorder {
	return &Recorder{store: store}
}

func (r *Recorder) CreateFromVerifiedOutcome(ctx context.Context, req CreateRequest) (*agentic.Followup, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("followup: nil store")
	}
	if req.TaskID == 0 {
		return nil, ErrMissingTaskID
	}
	if req.Reason == "" {
		return nil, errors.New("followup: reason is required")
	}
	req.Reason = strings.TrimSpace(secret.NewDetector().RedactString(req.Reason))
	task, err := r.store.GetAgenticTask(ctx, req.TaskID)
	if err != nil {
		return nil, fmt.Errorf("followup: get task: %w", err)
	}
	if req.GoalID == 0 {
		req.GoalID = task.GoalID
	}
	run, err := latestVerification(ctx, r.store, req.TaskID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.Verdict != agentic.VerificationVerdictPassed {
		return nil, ErrVerificationNotPassed
	}
	record := agentic.Followup{
		TaskID:    req.TaskID,
		GoalID:    req.GoalID,
		Reason:    req.Reason,
		Status:    agentic.FollowupStatusPending,
		TriggerAt: req.TriggerAt,
		DedupeKey: createDedupeKey(req),
		WakeAgent: req.WakeAgent,
	}
	if req.Cooldown > 0 {
		existing, err := r.store.FindFollowupInCooldown(ctx, record, req.Cooldown)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("followup: cooldown lookup: %w", err)
		}
	}
	followup, _, err := r.store.CreateOrGetFollowup(ctx, record)
	return followup, err
}

func latestVerification(ctx context.Context, store *agentic.Store, taskID int64) (*agentic.VerificationRun, error) {
	runs, err := store.ListVerificationRunsByTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("followup: list verification runs: %w", err)
	}
	var latest *agentic.VerificationRun
	for i := range runs {
		run := runs[i]
		if latest == nil || run.ID > latest.ID {
			latest = &run
		}
	}
	return latest, nil
}

func createDedupeKey(req CreateRequest) string {
	triggerAt := req.TriggerAt.UTC().UnixMilli()
	return fmt.Sprintf("followup:task:%d:goal:%d:wake:%t:trigger:%d:reason:%s", req.TaskID, req.GoalID, req.WakeAgent, triggerAt, hashString(req.Reason))
}
