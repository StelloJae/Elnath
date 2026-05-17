package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	TaskID          int64
	Event           ProgressEvent
	Raw             string
	OriginSurface   string
	DeliveryTargets []DeliveryTarget
	DeliveredAt     time.Time
}

// ProgressSink receives notification when a running task emits progress.
type ProgressSink interface {
	NotifyProgress(ctx context.Context, progress TaskProgress) error
}

// TargetedSink can opt into delivery-target filtering. Sinks that do not
// implement this interface are treated as internal audit sinks and still see
// all events so logging/spine observability is not lost.
type TargetedSink interface {
	DeliveryTarget() DeliveryTarget
}

type DeliveryRoute struct {
	OriginSurface   string
	DeliveryTargets []DeliveryTarget
}

type DeliverySinkStatus struct {
	Name         string `json:"name"`
	Completion   bool   `json:"completion"`
	Progress     bool   `json:"progress"`
	TargetAware  bool   `json:"target_aware"`
	Internal     bool   `json:"internal"`
	Target       string `json:"target,omitempty"`
	TargetKind   string `json:"target_kind,omitempty"`
	Platform     string `json:"platform,omitempty"`
	Address      string `json:"address,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	HomeChannel  bool   `json:"home_channel,omitempty"`
	DeliveryNote string `json:"delivery_note,omitempty"`
}

type DeliveryRouterStatus struct {
	CompletionSinkCount int                  `json:"completion_sink_count"`
	ProgressSinkCount   int                  `json:"progress_sink_count"`
	TargetAwareCount    int                  `json:"target_aware_count"`
	InternalSinkCount   int                  `json:"internal_sink_count"`
	Sinks               []DeliverySinkStatus `json:"sinks"`
}

// DeliveryRouter fans out task events to registered sinks.
type DeliveryRouter struct {
	sinks         []CompletionSink
	progressSinks []ProgressSink
	db            *sql.DB
	logger        *slog.Logger
	mu            sync.Mutex
	taskRoutes    map[int64]DeliveryRoute
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
	return &DeliveryRouter{db: db, logger: logger, taskRoutes: make(map[int64]DeliveryRoute)}, nil
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

func (r *DeliveryRouter) Status() DeliveryRouterStatus {
	if r == nil {
		return DeliveryRouterStatus{}
	}
	status := DeliveryRouterStatus{
		CompletionSinkCount: len(r.sinks),
		ProgressSinkCount:   len(r.progressSinks),
	}
	byName := make(map[string]int)
	for _, sink := range r.sinks {
		status.addSinkStatus(sink, true, false, byName)
	}
	for _, sink := range r.progressSinks {
		status.addSinkStatus(sink, false, true, byName)
	}
	for _, sink := range status.Sinks {
		if sink.TargetAware {
			status.TargetAwareCount++
		}
		if sink.Internal {
			status.InternalSinkCount++
		}
	}
	return status
}

func (s *DeliveryRouterStatus) addSinkStatus(sink any, completion, progress bool, byName map[string]int) {
	view := deliverySinkStatusOf(sink)
	key := view.Name + "\x00" + view.Target
	if idx, ok := byName[key]; ok {
		s.Sinks[idx].Completion = s.Sinks[idx].Completion || completion
		s.Sinks[idx].Progress = s.Sinks[idx].Progress || progress
		return
	}
	view.Completion = completion
	view.Progress = progress
	byName[key] = len(s.Sinks)
	s.Sinks = append(s.Sinks, view)
}

func (r *DeliveryRouter) RegisterTaskRoute(taskID int64, route DeliveryRoute) {
	if r == nil || taskID == 0 {
		return
	}
	route.OriginSurface = strings.ToLower(strings.TrimSpace(route.OriginSurface))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.taskRoutes[taskID] = route
}

func (r *DeliveryRouter) ClearTaskRoute(taskID int64) {
	if r == nil || taskID == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.taskRoutes, taskID)
}

func (r *DeliveryRouter) taskRoute(taskID int64) DeliveryRoute {
	if r == nil || taskID == 0 {
		return DeliveryRoute{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.taskRoutes[taskID]
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
	eligible := 0
	route := DeliveryRoute{OriginSurface: completion.OriginSurface, DeliveryTargets: completion.DeliveryTargets}
	for _, sink := range r.sinks {
		if !shouldDeliverToSink(sink, route) {
			continue
		}
		eligible++
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

	if eligible > 0 && len(errs) == eligible {
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
	route := DeliveryRoute{OriginSurface: progress.OriginSurface, DeliveryTargets: progress.DeliveryTargets}

	var errs []error
	eligible := 0
	for _, sink := range r.progressSinks {
		if !shouldDeliverToSink(sink, route) {
			continue
		}
		eligible++
		if err := sink.NotifyProgress(ctx, progress); err != nil {
			r.logger.Error("delivery: progress sink failed",
				"task_id", progress.TaskID,
				"sink", sinkNameOf(sink),
				"error", err,
			)
			errs = append(errs, err)
		}
	}

	if eligible > 0 && len(errs) == eligible {
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
	route := r.taskRoute(taskID)
	if err := r.DeliverProgress(context.Background(), TaskProgress{
		TaskID:          taskID,
		Event:           ev,
		Raw:             progress,
		OriginSurface:   route.OriginSurface,
		DeliveryTargets: route.DeliveryTargets,
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

func shouldDeliverToSink(sink any, route DeliveryRoute) bool {
	targets := route.DeliveryTargets
	if len(targets) == 0 {
		return true
	}
	targeted, ok := sink.(TargetedSink)
	if !ok {
		return true
	}
	sinkTarget := targeted.DeliveryTarget()
	for _, target := range targets {
		if deliveryTargetMatchesSink(target, route.OriginSurface, sinkTarget) {
			return true
		}
	}
	return false
}

func deliveryTargetMatchesSink(target DeliveryTarget, originSurface string, sink DeliveryTarget) bool {
	switch target.Kind {
	case DeliveryTargetLocal:
		return sink.Kind == DeliveryTargetLocal
	case DeliveryTargetOrigin:
		originSurface = strings.ToLower(strings.TrimSpace(originSurface))
		return originSurface != "" && sink.Kind == DeliveryTargetPlatform && sink.Platform == originSurface
	case DeliveryTargetPlatform:
		if sink.Kind != DeliveryTargetPlatform || sink.Platform != target.Platform {
			return false
		}
		if !target.Explicit {
			return true
		}
		if target.Address != "" && sink.Address != target.Address {
			return false
		}
		if target.ThreadID != "" && sink.ThreadID != target.ThreadID {
			return false
		}
		return true
	default:
		return false
	}
}

func sinkNameOf(sink any) string {
	if named, ok := sink.(interface{ String() string }); ok {
		return named.String()
	}
	return fmt.Sprintf("%T", sink)
}

func deliverySinkStatusOf(sink any) DeliverySinkStatus {
	view := DeliverySinkStatus{Name: sinkNameOf(sink), Internal: true}
	targeted, ok := sink.(TargetedSink)
	if !ok {
		view.DeliveryNote = "internal_audit_sink_receives_all_events"
		return view
	}
	target := targeted.DeliveryTarget()
	view.TargetAware = true
	view.Internal = false
	view.Target = strings.TrimSpace(target.String())
	view.TargetKind = string(target.Kind)
	view.Platform = target.Platform
	view.Address = target.Address
	view.ThreadID = target.ThreadID
	view.HomeChannel = target.IsHomeChannel()
	if view.Target == "" {
		view.DeliveryNote = "target_aware_sink_without_advertised_target"
	}
	return view
}
