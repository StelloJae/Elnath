package daemon

import "context"

// TaskEnvelope observes daemon task lifecycle without owning execution.
type TaskEnvelope interface {
	Start(ctx context.Context, task Task) (TaskEnvelopeRun, error)
}

type TaskEnvelopeReconciler interface {
	Reconcile(ctx context.Context) error
}

// TaskEnvelopeRun is the per-task lifecycle handle returned by TaskEnvelope.
type TaskEnvelopeRun interface {
	AgenticTaskID() int64
	Succeed(ctx context.Context) error
	Fail(ctx context.Context) error
}
