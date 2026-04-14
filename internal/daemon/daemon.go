package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stello/elnath/internal/identity"
)

// IPCRequest is a JSON-line command sent over the Unix socket.
type IPCRequest struct {
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// IPCResponse is a JSON-line reply sent back over the Unix socket.
type IPCResponse struct {
	OK   bool        `json:"ok"`
	Data interface{} `json:"data,omitempty"`
	Err  string      `json:"error,omitempty"`
}

// TaskResult is the outcome of executing one queued daemon task.
type TaskResult struct {
	Result    string `json:"result"`
	Summary   string `json:"summary,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// AgentTaskRunner executes one queued task and returns structured task output.
// Callers may forward streamed text through onText during execution.
type AgentTaskRunner func(ctx context.Context, payload string, onText func(string)) (TaskResult, error)

// ProgressObserver receives real-time progress updates for running tasks.
type ProgressObserver interface {
	OnProgress(taskID int64, progress string)
}

// Daemon runs background task processing with Unix domain socket IPC.
type Daemon struct {
	queue             *Queue
	listener          net.Listener
	socketPath        string
	maxWorkers        int
	taskRunner        AgentTaskRunner
	fallbackPrincipal identity.Principal
	logger            *slog.Logger
	deliveryRouter    *DeliveryRouter
	inactivityTimeout time.Duration
	wallClockTimeout  time.Duration
	watchdogInterval  time.Duration
	progressObserver  ProgressObserver
	cancel            context.CancelFunc
	wg                sync.WaitGroup
}

// New creates a Daemon. Call Start to begin listening and processing.
func New(queue *Queue, socketPath string, maxWorkers int, runner AgentTaskRunner, logger *slog.Logger) *Daemon {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Daemon{
		queue:      queue,
		socketPath: socketPath,
		maxWorkers: maxWorkers,
		taskRunner: runner,
		logger:     logger,
	}
}

// WithDeliveryRouter attaches a DeliveryRouter to the daemon. Completions are
// delivered after each task finishes. Must be called before Start.
func (d *Daemon) WithDeliveryRouter(router *DeliveryRouter) {
	d.deliveryRouter = router
}

// WithTimeouts configures per-task timeout enforcement. inactivity is the
// maximum duration without a progress update before cancelling a task.
// wallClock is the absolute maximum duration for any single task.
// Zero values disable the respective timeout.
func (d *Daemon) WithTimeouts(inactivity, wallClock time.Duration) {
	d.inactivityTimeout = inactivity
	d.wallClockTimeout = wallClock
}

// WithProgressObserver registers an observer that receives real-time progress
// updates for running tasks. Used by the Telegram sink to stream progress
// via message editing.
func (d *Daemon) WithProgressObserver(obs ProgressObserver) {
	d.progressObserver = obs
}

func (d *Daemon) WithFallbackPrincipal(principal identity.Principal) {
	d.fallbackPrincipal = principal
}

func (d *Daemon) watchdogTick() time.Duration {
	if d.watchdogInterval > 0 {
		return d.watchdogInterval
	}
	return 10 * time.Second
}

// Start begins listening on the Unix socket and launches worker goroutines.
// It blocks until ctx is cancelled or Stop is called.
func (d *Daemon) Start(ctx context.Context) error {
	ctx, d.cancel = context.WithCancel(ctx)

	if err := os.Remove(d.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("daemon: remove stale socket: %w", err)
	}

	ln, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("daemon: listen: %w", err)
	}
	d.listener = ln
	d.logger.Info("daemon started", "socket", d.socketPath, "workers", d.maxWorkers)

	for i := 0; i < d.maxWorkers; i++ {
		d.wg.Add(1)
		go d.worker(ctx, i)
	}

	d.wg.Add(1)
	go d.acceptLoop(ctx)

	<-ctx.Done()
	return d.cleanup()
}

// Stop gracefully shuts down the daemon.
func (d *Daemon) Stop() error {
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
	return nil
}

func (d *Daemon) cleanup() error {
	if d.listener != nil {
		d.listener.Close()
	}
	d.wg.Wait()
	os.Remove(d.socketPath)
	d.logger.Info("daemon stopped")
	return nil
}

func (d *Daemon) acceptLoop(ctx context.Context) {
	defer d.wg.Done()

	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				if isClosedErr(err) {
					return
				}
				d.logger.Error("daemon: accept", "error", err)
				continue
			}
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.handleConn(ctx, conn)
		}()
	}
}

func (d *Daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req IPCRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("invalid json: %v", err)})
		return
	}

	d.logger.Debug("ipc request", "command", req.Command)

	switch req.Command {
	case "submit":
		d.handleSubmit(ctx, conn, req)
	case "status":
		d.handleStatus(ctx, conn)
	case "stop":
		d.handleStop(conn)
	default:
		d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("unknown command: %q", req.Command)})
	}
}

func (d *Daemon) handleSubmit(ctx context.Context, conn net.Conn, req IPCRequest) {
	var payload string
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("invalid payload: %v", err)})
			return
		}
	}
	if payload == "" {
		d.writeResponse(conn, IPCResponse{Err: "payload is required"})
		return
	}

	parsed := ParseTaskPayload(payload)
	principal := parsed.Principal
	if principal.IsZero() {
		principal = d.fallbackPrincipal
	}
	idemKey := identity.KeyFor(principal, parsed.Prompt)

	id, existed, err := d.queue.Enqueue(ctx, payload, idemKey)
	if err != nil {
		d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("enqueue: %v", err)})
		return
	}

	d.writeResponse(conn, IPCResponse{
		OK: true,
		Data: map[string]any{
			"task_id": id,
			"existed": existed,
		},
	})
}

func (d *Daemon) handleStatus(ctx context.Context, conn net.Conn) {
	tasks, err := d.queue.List(ctx)
	if err != nil {
		d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("list: %v", err)})
		return
	}

	type taskView struct {
		ID          int64  `json:"id"`
		Payload     string `json:"payload"`
		SessionID   string `json:"session_id"`
		Status      string `json:"status"`
		Progress    string `json:"progress"`
		Summary     string `json:"summary"`
		Result      string `json:"result,omitempty"`
		Completion  any    `json:"completion"`
		CreatedAt   int64  `json:"created_at"`
		UpdatedAt   int64  `json:"updated_at"`
		CompletedAt int64  `json:"completed_at"`
	}

	views := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, taskView{
			ID:          t.ID,
			Payload:     t.Payload,
			SessionID:   t.SessionID,
			Status:      string(t.Status),
			Progress:    t.Progress,
			Summary:     t.Summary,
			Result:      t.Result,
			Completion:  t.Completion.View(),
			CreatedAt:   t.CreatedAt.UnixMilli(),
			UpdatedAt:   t.UpdatedAt.UnixMilli(),
			CompletedAt: t.CompletedAt.UnixMilli(),
		})
	}

	d.writeResponse(conn, IPCResponse{
		OK:   true,
		Data: map[string]interface{}{"tasks": views},
	})
}

func (d *Daemon) handleStop(conn net.Conn) {
	d.writeResponse(conn, IPCResponse{
		OK:   true,
		Data: map[string]string{"message": "shutting down"},
	})
	if d.cancel != nil {
		d.cancel()
	}
}

func (d *Daemon) writeResponse(conn net.Conn, resp IPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		d.logger.Error("daemon: marshal response", "error", err)
		return
	}
	data = append(data, '\n')
	conn.Write(data)
}

func (d *Daemon) worker(ctx context.Context, id int) {
	defer d.wg.Done()

	d.logger.Debug("worker started", "worker_id", id)
	defer d.logger.Debug("worker stopped", "worker_id", id)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		task, err := d.queue.Next(ctx)
		if err != nil {
			d.logger.Error("worker: dequeue", "worker_id", id, "error", err)
			sleepCtx(ctx, time.Second)
			continue
		}

		if task == nil {
			sleepCtx(ctx, time.Second)
			continue
		}
		principal := resolveTaskLogPrincipal(ParseTaskPayload(task.Payload), d.fallbackPrincipal)

		d.logger.Info("worker: processing task",
			"worker_id", id,
			"task_id", task.ID,
			"payload", task.Payload,
			"principal_user_id", principal.UserID,
			"principal_project_id", principal.ProjectID,
			"principal_surface", principal.Surface,
		)

		result, err := d.runTask(ctx, task)
		if err != nil {
			d.logger.Error("worker: task failed",
				"worker_id", id,
				"task_id", task.ID,
				"principal_user_id", principal.UserID,
				"principal_project_id", principal.ProjectID,
				"principal_surface", principal.Surface,
				"error", err,
			)
			if markErr := d.queue.MarkFailed(ctx, task.ID, err.Error()); markErr != nil {
				d.logger.Error("worker: mark failed", "task_id", task.ID, "error", markErr)
			}
			d.deliver(ctx, task.ID)
			continue
		}

		d.logger.Info("worker: task completed",
			"worker_id", id,
			"task_id", task.ID,
			"principal_user_id", principal.UserID,
			"principal_project_id", principal.ProjectID,
			"principal_surface", principal.Surface,
		)
		if markErr := d.queue.MarkDone(ctx, task.ID, result.Result, result.Summary); markErr != nil {
			d.logger.Error("worker: mark done", "task_id", task.ID, "error", markErr)
		}
		d.deliver(ctx, task.ID)
	}
}

// deliver reads the completed task from the queue and fans out to registered sinks.
func (d *Daemon) deliver(ctx context.Context, taskID int64) {
	if d.deliveryRouter == nil {
		return
	}
	task, err := d.queue.Get(ctx, taskID)
	if err != nil {
		d.logger.Error("worker: deliver: get task", "task_id", taskID, "error", err)
		return
	}
	if task.Completion == nil {
		return
	}
	if err := d.deliveryRouter.Deliver(ctx, *task.Completion); err != nil {
		d.logger.Error("worker: deliver: router", "task_id", taskID, "error", err)
	}
}

func (d *Daemon) runTask(ctx context.Context, task *Task) (TaskResult, error) {
	if d.taskRunner == nil {
		return TaskResult{}, fmt.Errorf("daemon: task runner is nil")
	}

	taskCtx, taskCancel := context.WithCancel(ctx)
	defer taskCancel()

	if d.wallClockTimeout > 0 {
		var wallCancel context.CancelFunc
		taskCtx, wallCancel = context.WithTimeout(taskCtx, d.wallClockTimeout)
		defer wallCancel()
	}

	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixMilli())

	if d.inactivityTimeout > 0 {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.inactivityWatchdog(taskCtx, taskCancel, &lastActivity, task.ID)
		}()
	}

	var output strings.Builder
	result, err := d.taskRunner(taskCtx, task.Payload, func(text string) {
		lastActivity.Store(time.Now().UnixMilli())

		progress := text
		if ev, ok := ParseProgressEvent(text); ok {
			progress = EncodeProgressEvent(ev)
		} else {
			output.WriteString(text)
			progress = EncodeProgressEvent(TextProgressEvent(text))
		}
		if progress == "" {
			return
		}
		go func(id int64, p string) {
			if updateErr := d.queue.UpdateProgress(ctx, id, p); updateErr != nil {
				d.logger.Debug("worker: update progress", "task_id", id, "error", updateErr)
			}
		}(task.ID, progress)
		if d.progressObserver != nil {
			d.progressObserver.OnProgress(task.ID, progress)
		}
	})
	if err != nil {
		if taskCtx.Err() != nil && ctx.Err() == nil {
			return TaskResult{}, fmt.Errorf("daemon: task timed out: %w", taskCtx.Err())
		}
		return TaskResult{}, fmt.Errorf("daemon: run task: %w", err)
	}
	if result.SessionID != "" {
		if bindErr := d.queue.BindSession(ctx, task.ID, result.SessionID); bindErr != nil {
			d.logger.Debug("worker: bind session", "task_id", task.ID, "error", bindErr)
		}
	}

	if output.Len() > 0 {
		result.Result = output.String()
	}
	if result.Result == "" {
		result.Result = result.Summary
	}
	return result, nil
}

func (d *Daemon) inactivityWatchdog(ctx context.Context, cancel context.CancelFunc, lastActivity *atomic.Int64, taskID int64) {
	ticker := time.NewTicker(d.watchdogTick())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(time.UnixMilli(lastActivity.Load()))
			if elapsed >= d.inactivityTimeout {
				d.logger.Warn("worker: task inactivity timeout",
					"task_id", taskID,
					"idle_seconds", int(elapsed.Seconds()),
				)
				cancel()
				return
			}
		}
	}
}

func summarizeProgress(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if len(line) > 120 {
			return line[:117] + "..."
		}
		return line
	}
	return ""
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func isClosedErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "use of closed network connection")
}

func resolveTaskLogPrincipal(payload TaskPayload, fallback identity.Principal) identity.Principal {
	principal := payload.Principal
	if principal.IsZero() && !fallback.IsZero() {
		principal = fallback
	}
	return principalForTaskLog(principal)
}

func principalForTaskLog(principal identity.Principal) identity.Principal {
	if strings.TrimSpace(principal.UserID) == "" {
		principal.UserID = identity.LegacyPrincipal().UserID
	}
	if strings.TrimSpace(principal.ProjectID) == "" {
		principal.ProjectID = identity.LegacyPrincipal().ProjectID
	}
	if strings.TrimSpace(principal.Surface) == "" {
		principal.Surface = identity.LegacyPrincipal().Surface
	}
	return principal
}
