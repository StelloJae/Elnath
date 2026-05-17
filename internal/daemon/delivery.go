package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const createDeliveryTable = `
CREATE TABLE IF NOT EXISTS task_completion_deliveries (
	task_id      INTEGER NOT NULL,
	sink_name    TEXT    NOT NULL,
	delivered_at INTEGER NOT NULL,
	PRIMARY KEY (task_id, sink_name)
);`

// CompletionSink receives notification when a task finishes.
type CompletionSink interface {
	NotifyCompletion(ctx context.Context, completion TaskCompletion) error
}

// TaskProgress is the UI-safe progress contract for a running task.
type TaskProgress struct {
	TaskID      int64
	Event       ProgressEvent
	Raw         string
	DeliveredAt time.Time
}

// ProgressSink receives notification when a running task emits progress.
type ProgressSink interface {
	NotifyProgress(ctx context.Context, progress TaskProgress) error
}

// DeliveryRouter fans out task events to registered sinks.
type DeliveryRouter struct {
	sinks         []CompletionSink
	progressSinks []ProgressSink
	db            *sql.DB
	logger        *slog.Logger
}

var _ ProgressObserver = (*DeliveryRouter)(nil)

// NewDeliveryRouter returns a DeliveryRouter with no sinks registered.
func NewDeliveryRouter(db *sql.DB, logger *slog.Logger) (*DeliveryRouter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if db != nil {
		if _, err := db.Exec(createDeliveryTable); err != nil {
			return nil, fmt.Errorf("delivery: create table: %w", err)
		}
	}
	return &DeliveryRouter{db: db, logger: logger}, nil
}

// Register adds a completion sink to the router. Sinks that also implement
// ProgressSink receive progress events through the same router.
func (r *DeliveryRouter) Register(sink CompletionSink) {
	r.sinks = append(r.sinks, sink)
	if progressSink, ok := sink.(ProgressSink); ok {
		r.progressSinks = append(r.progressSinks, progressSink)
	}
}

// RegisterProgress adds a progress-only sink to the router.
func (r *DeliveryRouter) RegisterProgress(sink ProgressSink) {
	r.progressSinks = append(r.progressSinks, sink)
}

// Deliver calls all registered sinks. Individual sink failures are logged but
// do not prevent other sinks from running. A non-nil error is returned only
// when every sink fails; partial failures are silently absorbed.
//
// Dedup ordering and crash window: the dedup row is claimed with INSERT OR
// IGNORE before NotifyCompletion runs, and rolled back with DELETE if the sink
// returns an error so retryable failures can be replayed. A daemon crash after
// the claim INSERT commits but before successful sink delivery can still leave a
// zombie claim row behind, which will suppress future delivery attempts for the
// same (task_id, sink_name). SF2 review-fixes explicitly leaves nightly cleanup
// of task_completion_deliveries out of scope; a future cleanup task should age
// out stale claims using delivered_at once it can verify sink-side delivery.
func (r *DeliveryRouter) Deliver(ctx context.Context, completion TaskCompletion) error {
	if len(r.sinks) == 0 {
		return nil
	}

	var errs []error
	for _, sink := range r.sinks {
		sinkName := sinkNameOf(sink)
		claimedDelivery := false
		if r.db != nil {
			res, err := r.db.ExecContext(ctx,
				`INSERT OR IGNORE INTO task_completion_deliveries(task_id, sink_name, delivered_at) VALUES (?, ?, ?)`,
				completion.TaskID, sinkName, time.Now().UnixMilli(),
			)
			if err != nil {
				r.logger.Error("delivery: dedup insert failed",
					"task_id", completion.TaskID,
					"sink", sinkName,
					"error", err,
				)
			} else if n, _ := res.RowsAffected(); n == 0 {
				r.logger.Debug("delivery: skipped duplicate", "task_id", completion.TaskID, "sink", sinkName)
				continue
			} else {
				claimedDelivery = true
			}
		}

		if err := sink.NotifyCompletion(ctx, completion); err != nil {
			if claimedDelivery {
				if _, deleteErr := r.db.ExecContext(ctx,
					`DELETE FROM task_completion_deliveries WHERE task_id = ? AND sink_name = ?`,
					completion.TaskID, sinkName,
				); deleteErr != nil {
					r.logger.Error("delivery: dedup rollback failed",
						"task_id", completion.TaskID,
						"sink", sinkName,
						"error", deleteErr,
					)
				}
			}
			r.logger.Error("delivery: sink failed",
				"task_id", completion.TaskID,
				"sink", sinkName,
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

// DeliverProgress fans out progress events. Unlike completion delivery, progress
// delivery is not deduplicated: every progress event is part of the live stream.
func (r *DeliveryRouter) DeliverProgress(ctx context.Context, progress TaskProgress) error {
	if len(r.progressSinks) == 0 {
		return nil
	}
	progress = normalizeTaskProgress(progress)
	if progress.Event.Message == "" {
		return nil
	}

	var errs []error
	for _, sink := range r.progressSinks {
		if err := sink.NotifyProgress(ctx, progress); err != nil {
			r.logger.Error("delivery: progress sink failed",
				"task_id", progress.TaskID,
				"sink", sinkNameOf(sink),
				"error", err,
			)
			errs = append(errs, err)
		}
	}

	if len(errs) == len(r.progressSinks) {
		return fmt.Errorf("delivery: all progress sinks failed: %w", errors.Join(errs...))
	}
	return nil
}

// OnProgress implements ProgressObserver so the daemon can route progress
// through the same delivery layer used for completion notifications.
func (r *DeliveryRouter) OnProgress(taskID int64, progress string) {
	ev, ok := ParseProgressEvent(progress)
	if !ok {
		return
	}
	if err := r.DeliverProgress(context.Background(), TaskProgress{
		TaskID: taskID,
		Event:  ev,
		Raw:    progress,
	}); err != nil {
		r.logger.Error("delivery: progress router failed", "task_id", taskID, "error", err)
	}
}

func normalizeTaskProgress(progress TaskProgress) TaskProgress {
	if progress.DeliveredAt.IsZero() {
		progress.DeliveredAt = time.Now()
	}
	if progress.Raw == "" {
		progress.Raw = EncodeProgressEvent(progress.Event)
	}
	if progress.Event.Message == "" && progress.Raw != "" {
		if ev, ok := ParseProgressEvent(progress.Raw); ok {
			progress.Event = ev
		}
	}
	return progress
}

func sinkNameOf(sink any) string {
	if named, ok := sink.(interface{ String() string }); ok {
		return named.String()
	}
	return fmt.Sprintf("%T", sink)
}
