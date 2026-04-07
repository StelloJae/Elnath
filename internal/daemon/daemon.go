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
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/llm"
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

// AgentFactory creates a fresh agent for each task execution.
// This abstraction lets the daemon work without knowing about specific providers.
type AgentFactory func(ctx context.Context) (*agent.Agent, error)

// Daemon runs background task processing with Unix domain socket IPC.
type Daemon struct {
	queue        *Queue
	listener     net.Listener
	socketPath   string
	maxWorkers   int
	agentFactory AgentFactory
	logger       *slog.Logger
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// New creates a Daemon. Call Start to begin listening and processing.
func New(queue *Queue, socketPath string, maxWorkers int, factory AgentFactory, logger *slog.Logger) *Daemon {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Daemon{
		queue:        queue,
		socketPath:   socketPath,
		maxWorkers:   maxWorkers,
		agentFactory: factory,
		logger:       logger,
	}
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

	id, err := d.queue.Enqueue(ctx, payload)
	if err != nil {
		d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("enqueue: %v", err)})
		return
	}

	d.writeResponse(conn, IPCResponse{
		OK:   true,
		Data: map[string]int64{"task_id": id},
	})
}

func (d *Daemon) handleStatus(ctx context.Context, conn net.Conn) {
	tasks, err := d.queue.List(ctx)
	if err != nil {
		d.writeResponse(conn, IPCResponse{Err: fmt.Sprintf("list: %v", err)})
		return
	}

	type taskView struct {
		ID        int64  `json:"id"`
		Payload   string `json:"payload"`
		Status    string `json:"status"`
		Result    string `json:"result,omitempty"`
		CreatedAt int64  `json:"created_at"`
	}

	views := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, taskView{
			ID:        t.ID,
			Payload:   t.Payload,
			Status:    string(t.Status),
			Result:    t.Result,
			CreatedAt: t.CreatedAt.UnixMilli(),
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

		d.logger.Info("worker: processing task",
			"worker_id", id,
			"task_id", task.ID,
			"payload", task.Payload,
		)

		result, err := d.runTask(ctx, task)
		if err != nil {
			d.logger.Error("worker: task failed",
				"worker_id", id,
				"task_id", task.ID,
				"error", err,
			)
			if markErr := d.queue.MarkFailed(ctx, task.ID, err.Error()); markErr != nil {
				d.logger.Error("worker: mark failed", "task_id", task.ID, "error", markErr)
			}
			continue
		}

		d.logger.Info("worker: task completed",
			"worker_id", id,
			"task_id", task.ID,
		)
		if markErr := d.queue.MarkDone(ctx, task.ID, result); markErr != nil {
			d.logger.Error("worker: mark done", "task_id", task.ID, "error", markErr)
		}
	}
}

func (d *Daemon) runTask(ctx context.Context, task *Task) (string, error) {
	ag, err := d.agentFactory(ctx)
	if err != nil {
		return "", fmt.Errorf("daemon: create agent: %w", err)
	}

	messages := []llm.Message{llm.NewUserMessage(task.Payload)}

	var output strings.Builder
	result, err := ag.Run(ctx, messages, func(text string) {
		output.WriteString(text)
	})
	if err != nil {
		return "", fmt.Errorf("daemon: agent run: %w", err)
	}

	if output.Len() > 0 {
		return output.String(), nil
	}

	if len(result.Messages) > 0 {
		last := result.Messages[len(result.Messages)-1]
		return last.Text(), nil
	}

	return "", nil
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
