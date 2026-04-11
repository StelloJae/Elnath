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

// DeliveryRouter fans out completion events to registered sinks.
type DeliveryRouter struct {
	sinks  []CompletionSink
	db     *sql.DB
	logger *slog.Logger
}

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

func sinkNameOf(sink CompletionSink) string {
	if named, ok := sink.(interface{ String() string }); ok {
		return named.String()
	}
	return fmt.Sprintf("%T", sink)
}
