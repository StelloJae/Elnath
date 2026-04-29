package runtime

import (
	"context"
	"database/sql"
	"strings"

	"github.com/stello/elnath/internal/agentic"
	"github.com/stello/elnath/internal/daemon"
)

type DaemonEnvelope struct {
	store *agentic.Store
}

func NewDaemonEnvelope(store *agentic.Store) *DaemonEnvelope {
	return &DaemonEnvelope{store: store}
}

func (e *DaemonEnvelope) Reconcile(ctx context.Context) error {
	return e.store.ReconcileDaemonTaskStatuses(ctx)
}

func (e *DaemonEnvelope) Start(ctx context.Context, task daemon.Task) (daemon.TaskEnvelopeRun, error) {
	existing, err := e.store.GetAgenticTaskByQueueTaskID(ctx, task.ID)
	if err == nil {
		updated, err := e.store.UpdateAgenticTaskStatus(ctx, existing.ID, agentic.TaskStatusRunning)
		if err != nil {
			return nil, err
		}
		return &daemonEnvelopeRun{store: e.store, agenticTaskID: updated.ID}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	payload := daemon.ParseTaskPayload(task.Payload)
	prompt := payload.Prompt
	if prompt == "" {
		prompt = strings.TrimSpace(task.Payload)
	}
	created, err := e.store.CreateAgenticTask(ctx, agentic.AgenticTask{
		QueueTaskID:        task.ID,
		Title:              daemonTaskTitle(payload, prompt),
		Prompt:             prompt,
		Status:             agentic.TaskStatusRunning,
		Priority:           0,
		RiskLevel:          agentic.RiskLevelLow,
		AutonomyDecision:   agentic.PolicyDecisionObserve,
		VerificationStatus: agentic.VerificationStatusPending,
	})
	if err != nil {
		if existing, getErr := e.store.GetAgenticTaskByQueueTaskID(ctx, task.ID); getErr == nil {
			return &daemonEnvelopeRun{store: e.store, agenticTaskID: existing.ID}, nil
		}
		return nil, err
	}
	return &daemonEnvelopeRun{store: e.store, agenticTaskID: created.ID}, nil
}

type daemonEnvelopeRun struct {
	store         *agentic.Store
	agenticTaskID int64
}

func (r *daemonEnvelopeRun) AgenticTaskID() int64 {
	return r.agenticTaskID
}

func (r *daemonEnvelopeRun) Succeed(ctx context.Context) error {
	_, err := r.store.UpdateAgenticTaskStatus(ctx, r.agenticTaskID, agentic.TaskStatusSucceeded)
	return err
}

func (r *daemonEnvelopeRun) Fail(ctx context.Context) error {
	_, err := r.store.UpdateAgenticTaskStatus(ctx, r.agenticTaskID, agentic.TaskStatusFailed)
	return err
}

func daemonTaskTitle(payload daemon.TaskPayload, prompt string) string {
	switch payload.Type {
	case daemon.TaskTypeResearch:
		return "Research task"
	case daemon.TaskTypeSkillPromote:
		return "Skill promotion task"
	default:
		if prompt == "" {
			return "Daemon task"
		}
		if len(prompt) <= 80 {
			return prompt
		}
		return prompt[:77] + "..."
	}
}
