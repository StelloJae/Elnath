package daemon

import "context"

type agenticTaskIDContextKey struct{}

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

func WithAgenticTaskID(ctx context.Context, id int64) context.Context {
	if id <= 0 {
		return ctx
	}
	return context.WithValue(ctx, agenticTaskIDContextKey{}, id)
}

func AgenticTaskIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(agenticTaskIDContextKey{}).(int64)
	if !ok || id <= 0 {
		return 0, false
	}
	return id, true
}
