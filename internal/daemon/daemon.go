package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stello/elnath/internal/agent/errorclass"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/fault"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/userfacingerr"
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

// TaskStatusView is the JSON-safe representation returned by daemon status.
type TaskStatusView struct {
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

// StatusResponse is the payload returned by daemon status.
type StatusResponse struct {
	Tasks                    []TaskStatusView     `json:"tasks"`
	InactivityTimeoutSeconds int64                `json:"inactivity_timeout_seconds"`
	WallClockTimeoutSeconds  int64                `json:"wall_clock_timeout_seconds"`
	Delivery                 DeliveryRouterStatus `json:"delivery"`
}

// TaskResult is the outcome of executing one queued daemon task.
type TaskResult struct {
	Result    string `json:"result"`
	Summary   string `json:"summary,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// AgentTaskRunner executes one legacy agent task payload.
// Callers may forward streamed events through the sink during execution.
type AgentTaskRunner func(ctx context.Context, payload string, sink event.Sink) (TaskResult, error)

// ProgressObserver receives real-time progress updates for running tasks.
type ProgressObserver interface {
	OnProgress(taskID int64, progress string)
}

type SessionRetirementRequest struct {
	SessionID               string
	FailureClass            string
	ShouldRetireSession     bool
	SessionRetirementReason string
	NextAction              string
}

type SessionRetirer interface {
	RetireSession(ctx context.Context, req SessionRetirementRequest) error
}

type SessionRetirerFunc func(ctx context.Context, req SessionRetirementRequest) error

func (fn SessionRetirerFunc) RetireSession(ctx context.Context, req SessionRetirementRequest) error {
	return fn(ctx, req)
}

type Scheduler interface {
	Run(ctx context.Context) error
}

type SubmitSignalBridge interface {
	RecordManualSubmitSignal(ctx context.Context, payload string, queueTaskID int64, existed bool) error
}

type CompletionGateDecision struct {
	Passed            bool
	Status            string
	Reason            string
	VerificationRunID int64
	GateID            int64
}

type CompletionGate interface {
	Validate(ctx context.Context, task Task, agenticTaskID int64) error
	Evaluate(ctx context.Context, task Task, agenticTaskID int64) (CompletionGateDecision, error)
}

type runningTaskCancelFunc func(reason string)

// Daemon runs background task processing with Unix domain socket IPC.
type Daemon struct {
	queue             *Queue
	listener          net.Listener
	socketPath        string
	maxWorkers        int
	agentRunner       AgentTaskRunner
	researchRunner    TaskRunner
	fallbackPrincipal identity.Principal
	logger            *slog.Logger
	deliveryRouter    *DeliveryRouter
	inactivityTimeout time.Duration
	wallClockTimeout  time.Duration
	watchdogInterval  time.Duration
	progressObserver  ProgressObserver
	taskEnvelope      TaskEnvelope
	completionGate    CompletionGate
	submitSignal      SubmitSignalBridge
	scheduler         Scheduler
	sessionRetirer    SessionRetirer
	faultInjector     fault.Injector
	faultScenario     *fault.Scenario
	faultGuard        fault.GuardConfig
	faultGuardChecked bool
	runningMu         sync.Mutex
	running           map[int64]runningTaskCancelFunc
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
		queue:         queue,
		socketPath:    socketPath,
		maxWorkers:    maxWorkers,
		agentRunner:   runner,
		logger:        logger,
		faultInjector: fault.NoopInjector{},
		running:       map[int64]runningTaskCancelFunc{},
	}
}

func (d *Daemon) CancelRunningTask(id int64, reason string) (bool, error) {
	if id <= 0 {
		return false, fmt.Errorf("daemon: cancel running task: id must be positive")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "task_stop requested"
	}

	d.runningMu.Lock()
	cancel, ok := d.running[id]
	d.runningMu.Unlock()
	if !ok {
		return false, nil
	}

	cancel(reason)
	return true, nil
}

func (d *Daemon) registerRunningTask(id int64, cancel runningTaskCancelFunc) {
	if id <= 0 || cancel == nil {
		return
	}
	d.runningMu.Lock()
	d.running[id] = cancel
	d.runningMu.Unlock()
}

func (d *Daemon) unregisterRunningTask(id int64) {
	if id <= 0 {
		return
	}
	d.runningMu.Lock()
	delete(d.running, id)
	d.runningMu.Unlock()
}

func (d *Daemon) WithFaultInjection(inj fault.Injector, scenario *fault.Scenario) {
	if inj == nil {
		inj = fault.NoopInjector{}
	}
	d.faultInjector = inj
	d.faultScenario = scenario
}

func (d *Daemon) WithFaultGuardConfig(cfg fault.GuardConfig) {
	d.faultGuard = cfg
}

func (d *Daemon) MarkFaultGuardChecked() {
	d.faultGuardChecked = true
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

func (d *Daemon) WithTaskEnvelope(envelope TaskEnvelope) {
	d.taskEnvelope = envelope
}

func (d *Daemon) WithCompletionGate(gate CompletionGate) {
	d.completionGate = gate
}

func (d *Daemon) WithSubmitSignalBridge(bridge SubmitSignalBridge) {
	d.submitSignal = bridge
}

func (d *Daemon) WithScheduler(s Scheduler) {
	d.scheduler = s
}

func (d *Daemon) WithSessionRetirer(retirer SessionRetirer) {
	d.sessionRetirer = retirer
}

func (d *Daemon) WithFallbackPrincipal(principal identity.Principal) {
	d.fallbackPrincipal = principal
}

func (d *Daemon) SetResearchRunner(r TaskRunner) {
	d.researchRunner = r
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
	if !d.faultGuardChecked {
		if _, err := fault.CheckGuards(d.faultGuard); err != nil {
			return fmt.Errorf("daemon: %w", err)
		}
	}
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
	d.deliverExistingCompletions(ctx)
	d.reconcileTaskEnvelope(ctx)

	for i := 0; i < d.maxWorkers; i++ {
		d.wg.Add(1)
		go d.worker(ctx, i)
	}

	if d.scheduler != nil {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			if err := d.scheduler.Run(ctx); err != nil && ctx.Err() == nil {
				d.logger.Error("scheduler stopped unexpectedly", "error", err)
			}
		}()
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
	if d.faultInjector.Active() && d.faultScenario != nil && d.faultScenario.Category == fault.CategoryIPC {
		conn = fault.NewIPCFaultConn(conn, d.faultInjector, d.faultScenario)
	}
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
	if strings.TrimSpace(payload) == "" {
		d.writeResponse(conn, IPCResponse{Err: "payload is required"})
		return
	}

	parsed := ParseTaskPayload(payload)
	if parsed.Prompt == "" {
		d.writeResponse(conn, IPCResponse{Err: "payload is required"})
		return
	}
	if parsed.Type == TaskTypeResearch && d.researchRunner == nil {
		d.writeResponse(conn, IPCResponse{Err: "research runner not configured"})
		return
	}
	principal := parsed.Principal
	if principal.IsZero() {
		principal = d.fallbackPrincipal
	}
	idemKey := identity.KeyFor(principal, EncodeTaskPayload(TaskPayload{
		Type:                  parsed.Type,
		Prompt:                parsed.Prompt,
		SessionID:             parsed.SessionID,
		AgenticEnforcement:    parsed.AgenticEnforcement,
		AgenticCompletionGate: parsed.AgenticCompletionGate,
	}))

	id, existed, err := d.queue.Enqueue(ctx, payload, idemKey)
	if err != nil {
		d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("enqueue: %v", err)})
		return
	}
	if d.submitSignal != nil {
		if signalErr := d.submitSignal.RecordManualSubmitSignal(ctx, payload, id, existed); signalErr != nil {
			d.logger.Warn("daemon: submit signal bridge failed", "task_id", id, "existed", existed, "error", signalErr)
		}
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

	views := make([]TaskStatusView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, TaskStatusView{
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
		OK: true,
		Data: StatusResponse{
			Tasks:                    views,
			InactivityTimeoutSeconds: int64(d.inactivityTimeout / time.Second),
			WallClockTimeoutSeconds:  int64(d.wallClockTimeout / time.Second),
			Delivery:                 d.deliveryRouter.Status(),
		},
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
		payload := ParseTaskPayload(task.Payload)
		principal := resolveTaskLogPrincipal(payload, d.fallbackPrincipal)
		if d.deliveryRouter != nil {
			d.deliveryRouter.RegisterTaskRoute(task.ID, DeliveryRoute{
				OriginSurface:   payload.Surface,
				DeliveryTargets: parseDeliveryTargetsLenient(payload.DeliveryTargets),
			})
		}

		d.logger.Info("worker: processing task",
			"worker_id", id,
			"task_id", task.ID,
			"payload", task.Payload,
			"principal_user_id", principal.UserID,
			"principal_project_id", principal.ProjectID,
			"principal_surface", principal.Surface,
		)

		var envelopeRun TaskEnvelopeRun
		if d.taskEnvelope != nil {
			envelopeRun, err = d.taskEnvelope.Start(ctx, *task)
			if err != nil {
				d.logger.Error("worker: task envelope start failed",
					"worker_id", id,
					"task_id", task.ID,
					"degraded_observability", true,
					"error", err,
				)
			}
		}

		runCtx := ctx
		if envelopeRun != nil {
			runCtx = WithAgenticTaskID(runCtx, envelopeRun.AgenticTaskID())
		}
		if payload.AgenticCompletionGate != "" {
			agenticTaskID := int64(0)
			if envelopeRun != nil {
				agenticTaskID = envelopeRun.AgenticTaskID()
			}
			if agenticTaskID == 0 {
				d.failCompletionGate(ctx, task, envelopeRun, "agentic task id is required")
				continue
			}
			if d.completionGate == nil {
				d.failCompletionGate(ctx, task, envelopeRun, "completion gate requested but completion gate is not configured")
				continue
			}
			if gateErr := d.completionGate.Validate(ctx, *task, agenticTaskID); gateErr != nil {
				d.failCompletionGate(ctx, task, envelopeRun, gateErr.Error())
				continue
			}
		}
		result, err := d.runTaskSafely(runCtx, task)
		if err != nil {
			d.logger.Error("worker: task failed",
				"worker_id", id,
				"task_id", task.ID,
				"principal_user_id", principal.UserID,
				"principal_project_id", principal.ProjectID,
				"principal_surface", principal.Surface,
				"error", err,
			)
			failureMeta := taskFailureMetadata(err)
			if markErr := d.queue.MarkFailedWithMetadata(ctx, task.ID, err.Error(), failureMeta); markErr != nil {
				d.logger.Error("worker: mark failed", "task_id", task.ID, "error", markErr)
			} else if envelopeRun != nil {
				if envelopeErr := envelopeRun.Fail(ctx); envelopeErr != nil {
					d.logger.Error("worker: task envelope fail update",
						"task_id", task.ID,
						"agentic_task_id", envelopeRun.AgenticTaskID(),
						"degraded_observability", true,
						"error", envelopeErr,
					)
				}
			}
			d.retireSessionAfterFailure(ctx, task, failureMeta)
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
		if payload.AgenticCompletionGate != "" {
			agenticTaskID := int64(0)
			if envelopeRun != nil {
				agenticTaskID = envelopeRun.AgenticTaskID()
			}
			decision, gateErr := d.completionGate.Evaluate(ctx, *task, agenticTaskID)
			if gateErr != nil {
				d.failCompletionGate(ctx, task, envelopeRun, gateErr.Error())
				continue
			}
			if !decision.Passed {
				reason := decision.Reason
				if reason == "" {
					reason = "completion gate blocked"
				}
				d.failCompletionGate(ctx, task, envelopeRun, reason)
				continue
			}
		}
		if markErr := d.queue.MarkDone(ctx, task.ID, result.Result, result.Summary); markErr != nil {
			d.logger.Error("worker: mark done", "task_id", task.ID, "error", markErr)
		} else if envelopeRun != nil {
			if envelopeErr := envelopeRun.Succeed(ctx); envelopeErr != nil {
				d.logger.Error("worker: task envelope success update",
					"task_id", task.ID,
					"agentic_task_id", envelopeRun.AgenticTaskID(),
					"degraded_observability", true,
					"error", envelopeErr,
				)
			}
		}
		d.deliver(ctx, task.ID)
	}
}

func (d *Daemon) retireSessionAfterFailure(ctx context.Context, task *Task, meta TaskFailureMetadata) {
	if d.sessionRetirer == nil || task == nil || !meta.ShouldRetireSession || strings.TrimSpace(task.SessionID) == "" {
		return
	}
	req := SessionRetirementRequest{
		SessionID:               task.SessionID,
		FailureClass:            meta.FailureClass,
		ShouldRetireSession:     meta.ShouldRetireSession,
		SessionRetirementReason: meta.SessionRetirementReason,
		NextAction:              meta.NextAction,
	}
	if err := d.sessionRetirer.RetireSession(ctx, req); err != nil {
		d.logger.Warn("worker: session retirement record failed",
			"task_id", task.ID,
			"session_id", task.SessionID,
			"failure_class", meta.FailureClass,
			"reason", meta.SessionRetirementReason,
			"degraded_observability", true,
			"error", err,
		)
	}
}

func (d *Daemon) failCompletionGate(ctx context.Context, task *Task, envelopeRun TaskEnvelopeRun, reason string) {
	message := "completion gate blocked: " + reason
	d.logger.Error("worker: completion gate blocked", "task_id", task.ID, "error", reason)
	if markErr := d.queue.MarkFailed(ctx, task.ID, message); markErr != nil {
		d.logger.Error("worker: completion gate mark failed", "task_id", task.ID, "error", markErr)
	} else if envelopeRun != nil {
		if envelopeErr := envelopeRun.Fail(ctx); envelopeErr != nil {
			d.logger.Error("worker: task envelope fail update",
				"task_id", task.ID,
				"agentic_task_id", envelopeRun.AgenticTaskID(),
				"degraded_observability", true,
				"error", envelopeErr,
			)
		}
	}
	d.deliver(ctx, task.ID)
}

func (d *Daemon) runTaskSafely(ctx context.Context, task *Task) (result TaskResult, err error) {
	d.resetFaultRunState()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("daemon: recovered worker panic: %v", r)
		}
	}()
	if d.shouldInjectWorkerPanic() {
		panic("fault: injected worker panic")
	}
	return d.runTask(ctx, task)
}

func (d *Daemon) resetFaultRunState() {
	if resetter, ok := d.faultInjector.(interface{ ResetForRun() }); ok {
		resetter.ResetForRun()
	}
}

func (d *Daemon) shouldInjectWorkerPanic() bool {
	if !d.faultInjector.Active() || d.faultScenario == nil {
		return false
	}
	if d.faultScenario.Category != fault.CategoryIPC || d.faultScenario.FaultType != fault.FaultWorkerPanic {
		return false
	}
	return d.faultInjector.ShouldFault(d.faultScenario)
}

// deliver reads the completed task from the queue and fans out to registered sinks.
func (d *Daemon) deliver(ctx context.Context, taskID int64) {
	if d.deliveryRouter == nil {
		return
	}
	defer d.deliveryRouter.ClearTaskRoute(taskID)
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

func (d *Daemon) deliverExistingCompletions(ctx context.Context) {
	if d.deliveryRouter == nil {
		return
	}
	tasks, err := d.queue.List(ctx)
	if err != nil {
		d.logger.Error("daemon: list existing completions", "error", err)
		return
	}
	for _, task := range tasks {
		if task.Completion == nil {
			continue
		}
		d.deliver(ctx, task.ID)
	}
}

func (d *Daemon) reconcileTaskEnvelope(ctx context.Context) {
	if d.taskEnvelope == nil {
		return
	}
	reconciler, ok := d.taskEnvelope.(TaskEnvelopeReconciler)
	if !ok {
		return
	}
	if err := reconciler.Reconcile(ctx); err != nil {
		d.logger.Error("daemon: task envelope reconcile failed", "degraded_observability", true, "error", err)
	}
}

func (d *Daemon) runTask(ctx context.Context, task *Task) (TaskResult, error) {
	payload := ParseTaskPayload(task.Payload)
	captureOutput := true
	exec := func(taskCtx context.Context, sink event.Sink) (TaskResult, error) {
		if d.agentRunner == nil {
			return TaskResult{}, fmt.Errorf("daemon: task runner is nil")
		}
		return d.agentRunner(taskCtx, task.Payload, sink)
	}
	if payload.Type == TaskTypeResearch {
		if d.researchRunner == nil {
			return TaskResult{}, fmt.Errorf("research runner not configured")
		}
		captureOutput = false
		exec = func(taskCtx context.Context, sink event.Sink) (TaskResult, error) {
			result, err := d.researchRunner.Run(taskCtx, payload, sink)
			if err != nil {
				return TaskResult{}, err
			}
			sessionID := result.SessionID
			if sessionID == "" {
				sessionID = payload.SessionID
			}
			return TaskResult{Result: result.Result, Summary: result.Summary, SessionID: sessionID}, nil
		}
	}

	taskCtx, taskCancel := context.WithCancel(ctx)
	defer taskCancel()

	var manualCanceled atomic.Bool
	var manualCancelReason atomic.Value
	d.registerRunningTask(task.ID, func(reason string) {
		manualCancelReason.Store(reason)
		manualCanceled.Store(true)
		taskCancel()
	})
	defer d.unregisterRunningTask(task.ID)

	if d.wallClockTimeout > 0 {
		var wallCancel context.CancelFunc
		taskCtx, wallCancel = context.WithTimeout(taskCtx, d.wallClockTimeout)
		defer wallCancel()
	}

	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixMilli())

	var inactivityTimedOut atomic.Bool
	if d.inactivityTimeout > 0 {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.inactivityWatchdog(taskCtx, taskCancel, &lastActivity, task.ID, &inactivityTimedOut)
		}()
	}

	var output strings.Builder
	onTextFn := func(text string) {
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
	}
	result, err := exec(taskCtx, event.OnTextToSink(onTextFn))
	if err != nil {
		if manualCanceled.Load() && taskCtx.Err() != nil && ctx.Err() == nil {
			reason, _ := manualCancelReason.Load().(string)
			return TaskResult{}, taskCanceledError{reason: reason}
		}
		if taskCtx.Err() != nil && ctx.Err() == nil {
			inner := fmt.Errorf("daemon: task timed out: %w", taskCtx.Err())
			timeoutClass := TimeoutClassActiveButKilled
			if inactivityTimedOut.Load() {
				timeoutClass = TimeoutClassIdle
			}
			return TaskResult{}, taskTimeoutError{
				err:          userfacingerr.Wrap(userfacingerr.ELN110, inner, "daemon task"),
				timeoutClass: timeoutClass,
			}
		}
		return TaskResult{}, fmt.Errorf("daemon: run task: %w", err)
	}
	if result.SessionID != "" {
		if bindErr := d.queue.BindSession(ctx, task.ID, result.SessionID); bindErr != nil {
			d.logger.Debug("worker: bind session", "task_id", task.ID, "error", bindErr)
		}
	}

	if captureOutput && output.Len() > 0 {
		result.Result = output.String()
	}
	if result.Result == "" {
		result.Result = result.Summary
	}
	return result, nil
}

type taskCanceledError struct {
	reason string
}

func (e taskCanceledError) Error() string {
	reason := strings.TrimSpace(e.reason)
	if reason == "" {
		reason = "task_stop requested"
	}
	return "daemon: task canceled: " + reason
}

type taskTimeoutError struct {
	err          error
	timeoutClass TimeoutClass
}

func (e taskTimeoutError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e taskTimeoutError) Unwrap() error {
	return e.err
}

func (e taskTimeoutError) TimeoutClass() TimeoutClass {
	return e.timeoutClass
}

func failureTimeoutClass(err error) TimeoutClass {
	var timeoutErr interface{ TimeoutClass() TimeoutClass }
	if errors.As(err, &timeoutErr) {
		return timeoutErr.TimeoutClass()
	}
	return TimeoutClassNone
}

const (
	failureClassTaskTimeoutIdle   = "task_timeout_idle"
	failureClassTaskTimeoutActive = "task_timeout_active"
	failureClassTaskCanceled      = "task_canceled"
	failureClassWorkerPanic       = "worker_panic"
	failureClassProviderAuth      = "provider_auth"
	failureClassProviderRateLimit = "provider_rate_limit"
	failureClassProviderTimeout   = "provider_timeout"
	failureClassProviderError     = "provider_error"
	failureClassContextWindow     = "context_window"
	failureClassModelNotFound     = "model_not_found"
	failureClassRuntimeIO         = "runtime_io"
	failureClassToolRuntime       = "tool_runtime"

	retirementPostToolQuietTimeout = "post_tool_quiet_timeout"
	retirementWallClockTimeout     = "wall_clock_timeout"
	retirementWorkerPanic          = "worker_panic"
	retirementProviderAuth         = "provider_auth_refresh_failed"
	retirementProviderTimeout      = "provider_timeout"
	retirementRuntimeIO            = "runtime_io"

	nextActionInspectBeforeRetry     = "inspect_failure_before_retry"
	nextActionOperatorCancelled      = "operator_cancelled"
	nextActionReauthenticateProvider = "reauthenticate_provider"
	nextActionRetryLater             = "retry_later"
	nextActionCompactContext         = "compact_context_before_retry"
	nextActionSelectSupportedModel   = "select_supported_model"
	nextActionStartNewSession        = "start_new_session_or_operator_review"
)

func taskFailureMetadata(err error) TaskFailureMetadata {
	timeoutClass := failureTimeoutClass(err)
	class := taskFailureClass(err, timeoutClass)
	meta := TaskFailureMetadata{
		TimeoutClass: timeoutClass,
		FailureClass: class,
		NextAction:   taskFailureNextAction(class),
	}
	meta.ShouldRetireSession, meta.SessionRetirementReason = taskFailureRetirement(class)
	return meta
}

func taskFailureClass(err error, timeoutClass TimeoutClass) string {
	if timeoutClass == TimeoutClassIdle {
		return failureClassTaskTimeoutIdle
	}
	if timeoutClass == TimeoutClassActiveButKilled {
		return failureClassTaskTimeoutActive
	}
	var cancelErr taskCanceledError
	if errors.As(err, &cancelErr) {
		return failureClassTaskCanceled
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "recovered worker panic"):
		return failureClassWorkerPanic
	case containsAny(msg, "broken pipe", "connection reset", "connection refused", "eof"):
		return failureClassRuntimeIO
	default:
		return taskFailureClassFromProviderError(err)
	}
}

func taskFailureClassFromProviderError(err error) string {
	classified := errorclass.Classify(err, errorclass.Context{Provider: "daemon"})
	switch classified.Category {
	case errorclass.Auth, errorclass.AuthPermanent, errorclass.Billing:
		return failureClassProviderAuth
	case errorclass.RateLimit, errorclass.Overloaded:
		return failureClassProviderRateLimit
	case errorclass.Timeout, errorclass.ServerError:
		return failureClassProviderTimeout
	case errorclass.ContextOverflow, errorclass.PayloadTooLarge:
		return failureClassContextWindow
	case errorclass.ModelNotFound:
		return failureClassModelNotFound
	case errorclass.FormatError:
		return failureClassProviderError
	default:
		return failureClassToolRuntime
	}
}

func taskFailureRetirement(class string) (bool, string) {
	switch class {
	case failureClassTaskTimeoutIdle:
		return true, retirementPostToolQuietTimeout
	case failureClassTaskTimeoutActive:
		return true, retirementWallClockTimeout
	case failureClassWorkerPanic:
		return true, retirementWorkerPanic
	case failureClassProviderAuth:
		return true, retirementProviderAuth
	case failureClassProviderTimeout:
		return true, retirementProviderTimeout
	case failureClassRuntimeIO:
		return true, retirementRuntimeIO
	default:
		return false, ""
	}
}

func taskFailureNextAction(class string) string {
	switch class {
	case failureClassTaskTimeoutIdle, failureClassTaskTimeoutActive, failureClassWorkerPanic, failureClassProviderTimeout, failureClassRuntimeIO:
		return nextActionStartNewSession
	case failureClassProviderAuth:
		return nextActionReauthenticateProvider
	case failureClassProviderRateLimit:
		return nextActionRetryLater
	case failureClassContextWindow:
		return nextActionCompactContext
	case failureClassModelNotFound:
		return nextActionSelectSupportedModel
	case failureClassTaskCanceled:
		return nextActionOperatorCancelled
	default:
		return nextActionInspectBeforeRetry
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func (d *Daemon) inactivityWatchdog(ctx context.Context, cancel context.CancelFunc, lastActivity *atomic.Int64, taskID int64, timedOut *atomic.Bool) {
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
				if timedOut != nil {
					timedOut.Store(true)
				}
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
