package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// CompletionSink receives notification when a task finishes.
type CompletionSink interface {
	NotifyCompletion(ctx context.Context, completion TaskCompletion) error
}

// DeliveryRouter fans out completion events to registered sinks.
type DeliveryRouter struct {
	sinks  []CompletionSink
	logger *slog.Logger
}

// NewDeliveryRouter returns a DeliveryRouter with no sinks registered.
func NewDeliveryRouter(logger *slog.Logger) *DeliveryRouter {
	if logger == nil {
		logger = slog.Default()
	}
	return &DeliveryRouter{logger: logger}
}

// Register adds a sink to the router.
func (r *DeliveryRouter) Register(sink CompletionSink) {
	r.sinks = append(r.sinks, sink)
}

// Deliver calls all registered sinks. Individual sink failures are logged but
// do not prevent other sinks from running. A non-nil error is returned only
// when every sink fails; partial failures are silently absorbed.
func (r *DeliveryRouter) Deliver(ctx context.Context, completion TaskCompletion) error {
	if len(r.sinks) == 0 {
		return nil
	}

	var errs []error
	for _, sink := range r.sinks {
		if err := sink.NotifyCompletion(ctx, completion); err != nil {
			r.logger.Error("delivery: sink failed",
				"task_id", completion.TaskID,
				"sink", fmt.Sprintf("%T", sink),
				"error", err,
			)
			errs = append(errs, err)
		}
	}

	if len(errs) == len(r.sinks) {
		return fmt.Errorf("delivery: all sinks failed: %w", errors.Join(errs...))
	}
	return nil
}
