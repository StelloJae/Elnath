package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/tools"
)

func cmdTask(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Println(`Usage: elnath task <subcommand>

Subcommands:
  list               List recent tasks (last 20)
  show <id>          Show task details
  monitor <id>       Show or wait for task monitor snapshot
  output <id>        Read bounded task output
  stop <id>          Stop a pending task
  resume <id>        Resume the session created by a task`)
		return nil
	}
	switch args[0] {
	case "list":
		return cmdTaskList(ctx)
	case "show":
		return cmdTaskShow(ctx, args[1:])
	case "monitor":
		return cmdTaskMonitor(ctx, args[1:])
	case "output":
		return cmdTaskOutput(ctx, args[1:])
	case "stop":
		return cmdTaskStop(ctx, args[1:])
	case "resume":
		return cmdTaskResume(ctx, args[1:])
	default:
		return fmt.Errorf("unknown task subcommand: %s", args[0])
	}
}

func cmdTaskList(ctx context.Context) error {
	cfg, db, err := openTaskDB()
	if err != nil {
		return err
	}
	defer db.Close()
	_ = cfg

	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		return fmt.Errorf("open queue: %w", err)
	}

	tasks, err := queue.List(ctx)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}

	limit := 20
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}

	if len(tasks) == 0 {
		fmt.Println("No tasks.")
		return nil
	}

	fmt.Printf("%-6s  %-10s  %-16s  %-20s  %s\n",
		"ID", "STATUS", "SESSION", "CREATED", "PAYLOAD")
	fmt.Printf("%-6s  %-10s  %-16s  %-20s  %s\n",
		"------", "----------", "----------------", "--------------------",
		"------------------------------------------------------------")
	for _, t := range tasks {
		payload := truncate(t.Payload, 80)
		sessionID := truncate(t.SessionID, 16)
		created := formatTimestamp(t.CreatedAt)
		fmt.Printf("%-6d  %-10s  %-16s  %-20s  %s\n",
			t.ID, string(t.Status), sessionID, created, payload)
	}
	return nil
}

func cmdTaskShow(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath task show <id>")
	}
	taskID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid task ID %q: %w", args[0], err)
	}

	_, db, err := openTaskDB()
	if err != nil {
		return err
	}
	defer db.Close()

	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		return fmt.Errorf("open queue: %w", err)
	}

	task, err := queue.Get(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get task %d: %w", taskID, err)
	}

	fmt.Printf("ID:           %d\n", task.ID)
	fmt.Printf("Status:       %s\n", task.Status)
	fmt.Printf("Session ID:   %s\n", task.SessionID)
	fmt.Printf("Payload:      %s\n", truncate(task.Payload, 80))
	fmt.Printf("Summary:      %s\n", task.Summary)
	fmt.Printf("Created:      %s\n", formatTimestamp(task.CreatedAt))
	if !task.StartedAt.IsZero() && task.StartedAt.UnixMilli() > 0 {
		fmt.Printf("Started:      %s\n", formatTimestamp(task.StartedAt))
	}
	if !task.CompletedAt.IsZero() && task.CompletedAt.UnixMilli() > 0 {
		fmt.Printf("Completed:    %s\n", formatTimestamp(task.CompletedAt))
	}
	return nil
}

func cmdTaskMonitor(ctx context.Context, args []string) error {
	db, queue, err := openTaskQueue()
	if err != nil {
		return err
	}
	defer db.Close()
	return cmdTaskMonitorWithQueue(ctx, queue, args)
}

func cmdTaskOutput(ctx context.Context, args []string) error {
	db, queue, err := openTaskQueue()
	if err != nil {
		return err
	}
	defer db.Close()
	return cmdTaskOutputWithQueue(ctx, queue, args)
}

func cmdTaskStop(ctx context.Context, args []string) error {
	db, queue, err := openTaskQueue()
	if err != nil {
		return err
	}
	defer db.Close()
	return cmdTaskStopWithQueue(ctx, queue, args)
}

func cmdTaskMonitorWithQueue(ctx context.Context, queue *daemon.Queue, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath task monitor <id> [--json] [--max-chars N] [--wait --since-updated-at TS --timeout-ms MS]")
	}
	taskID, err := parseTaskID(args[0])
	if err != nil {
		return err
	}
	params := map[string]any{"id": taskID}
	jsonOut := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--wait":
			params["wait_for_update"] = true
		case "--max-chars":
			value, next, err := parseIntFlag(args, i, "--max-chars")
			if err != nil {
				return err
			}
			params["max_chars"] = value
			i = next
		case "--timeout-ms":
			value, next, err := parseIntFlag(args, i, "--timeout-ms")
			if err != nil {
				return err
			}
			params["timeout_ms"] = value
			i = next
		case "--since-updated-at":
			value, next, err := parseStringFlag(args, i, "--since-updated-at")
			if err != nil {
				return err
			}
			params["since_updated_at"] = value
			i = next
		default:
			return fmt.Errorf("unknown task monitor flag: %s", args[i])
		}
	}

	output, err := executeTaskTool(ctx, daemon.NewTaskMonitorTool(queue), params)
	if err != nil {
		return err
	}
	if jsonOut {
		fmt.Println(output)
		return nil
	}
	var view taskMonitorCLIOutput
	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return fmt.Errorf("task monitor: parse output: %w", err)
	}
	printTaskMonitor(view)
	return nil
}

func cmdTaskOutputWithQueue(ctx context.Context, queue *daemon.Queue, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath task output <id> [--json] [--field result|progress|summary|payload] [--max-chars N] [--block --timeout-ms MS]")
	}
	taskID, err := parseTaskID(args[0])
	if err != nil {
		return err
	}
	params := map[string]any{"id": taskID}
	jsonOut := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--block":
			params["block"] = true
		case "--field":
			value, next, err := parseStringFlag(args, i, "--field")
			if err != nil {
				return err
			}
			params["field"] = value
			i = next
		case "--max-chars":
			value, next, err := parseIntFlag(args, i, "--max-chars")
			if err != nil {
				return err
			}
			params["max_chars"] = value
			i = next
		case "--timeout-ms":
			value, next, err := parseIntFlag(args, i, "--timeout-ms")
			if err != nil {
				return err
			}
			params["timeout_ms"] = value
			i = next
		default:
			return fmt.Errorf("unknown task output flag: %s", args[i])
		}
	}

	output, err := executeTaskTool(ctx, daemon.NewTaskOutputTool(queue), params)
	if err != nil {
		return err
	}
	if jsonOut {
		fmt.Println(output)
		return nil
	}
	var view taskOutputCLIOutput
	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return fmt.Errorf("task output: parse output: %w", err)
	}
	printTaskOutput(view)
	return nil
}

func cmdTaskStopWithQueue(ctx context.Context, queue *daemon.Queue, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath task stop <id> [--json] [--reason TEXT]")
	}
	taskID, err := parseTaskID(args[0])
	if err != nil {
		return err
	}
	params := map[string]any{"id": taskID}
	jsonOut := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--reason":
			value, next, err := parseStringFlag(args, i, "--reason")
			if err != nil {
				return err
			}
			params["reason"] = value
			i = next
		default:
			return fmt.Errorf("unknown task stop flag: %s", args[i])
		}
	}
	task, err := queue.Get(ctx, taskID)
	if err != nil {
		return fmt.Errorf("task stop: %w", err)
	}
	if task.Status == daemon.StatusRunning {
		return fmt.Errorf("task stop: CLI wrapper supports pending tasks only; running task cancellation requires daemon runtime support; use elnath task monitor %d to inspect progress", taskID)
	}

	output, err := executeTaskTool(ctx, daemon.NewTaskStopTool(queue), params)
	if err != nil {
		return err
	}
	if jsonOut {
		fmt.Println(output)
		return nil
	}
	var view taskStopCLIOutput
	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return fmt.Errorf("task stop: parse output: %w", err)
	}
	printTaskStop(view)
	return nil
}

func cmdTaskResume(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath task resume <id>")
	}
	taskID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid task ID %q: %w", args[0], err)
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	sid, err := resolveTaskSession(db.Main, taskID)
	if err != nil {
		return err
	}

	os.Args = append(os.Args, "--session", sid)
	return cmdRun(ctx, nil)
}

func resolveTaskSession(mainDB *sql.DB, taskID int64) (string, error) {
	queue, err := daemon.NewQueue(mainDB)
	if err != nil {
		return "", fmt.Errorf("open queue: %w", err)
	}

	task, err := queue.Get(context.Background(), taskID)
	if err != nil {
		return "", fmt.Errorf("task %d not found: %w", taskID, err)
	}
	if task.SessionID == "" {
		return "", fmt.Errorf("task %d has no session bound", taskID)
	}
	return task.SessionID, nil
}

type taskTool interface {
	Execute(context.Context, json.RawMessage) (*tools.Result, error)
}

func executeTaskTool(ctx context.Context, tool taskTool, params map[string]any) (string, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("task: marshal params: %w", err)
	}
	result, err := tool.Execute(ctx, raw)
	if err != nil {
		return "", err
	}
	if result.IsError {
		return "", fmt.Errorf("%s", result.Output)
	}
	return result.Output, nil
}

type taskMonitorCLIOutput struct {
	TaskID             int64  `json:"task_id"`
	Status             string `json:"status"`
	RetrievalStatus    string `json:"retrieval_status"`
	Terminal           bool   `json:"terminal"`
	NextPollSeconds    int    `json:"next_poll_seconds"`
	ObservedAt         string `json:"observed_at"`
	UpdatedAt          string `json:"updated_at"`
	Progress           string `json:"progress"`
	Summary            string `json:"summary"`
	ResultTail         string `json:"result_tail"`
	ResultTotalChars   int    `json:"result_total_chars"`
	ResultTruncated    bool   `json:"result_truncated"`
	TimeoutClass       string `json:"timeout_class"`
	IdleTimeoutCount   int    `json:"idle_timeout_count"`
	ActiveTimeoutCount int    `json:"active_timeout_count"`
}

type taskOutputCLIOutput struct {
	TaskID          int64  `json:"task_id"`
	Status          string `json:"status"`
	RetrievalStatus string `json:"retrieval_status"`
	Terminal        bool   `json:"terminal"`
	Field           string `json:"field"`
	MaxChars        int    `json:"max_chars"`
	TotalChars      int    `json:"total_chars"`
	Truncated       bool   `json:"truncated"`
	Content         string `json:"content"`
}

type taskStopCLIOutput struct {
	TaskID         int64  `json:"task_id"`
	Stopped        bool   `json:"stopped"`
	Accepted       bool   `json:"accepted"`
	Terminal       bool   `json:"terminal"`
	PreviousStatus string `json:"previous_status"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	FollowupTool   string `json:"followup_tool"`
}

func printTaskMonitor(view taskMonitorCLIOutput) {
	fmt.Printf("ID:           %d\n", view.TaskID)
	fmt.Printf("Status:       %s\n", view.Status)
	fmt.Printf("Retrieval:    %s\n", view.RetrievalStatus)
	fmt.Printf("Terminal:     %t\n", view.Terminal)
	fmt.Printf("Updated:      %s\n", emptyDash(view.UpdatedAt))
	fmt.Printf("Observed:     %s\n", emptyDash(view.ObservedAt))
	fmt.Printf("Next poll:    %ds\n", view.NextPollSeconds)
	if view.Progress != "" {
		fmt.Printf("Progress:     %s\n", view.Progress)
	}
	if view.Summary != "" {
		fmt.Printf("Summary:      %s\n", view.Summary)
	}
	if view.TimeoutClass != "" || view.IdleTimeoutCount != 0 || view.ActiveTimeoutCount != 0 {
		fmt.Printf("Timeouts:     class=%s idle=%d active=%d\n", emptyDash(view.TimeoutClass), view.IdleTimeoutCount, view.ActiveTimeoutCount)
	}
	if view.ResultTail != "" {
		fmt.Printf("Result tail:  %s\n", view.ResultTail)
		fmt.Printf("Result chars: %d truncated=%t\n", view.ResultTotalChars, view.ResultTruncated)
	}
}

func printTaskOutput(view taskOutputCLIOutput) {
	fmt.Printf("ID:           %d\n", view.TaskID)
	fmt.Printf("Status:       %s\n", view.Status)
	fmt.Printf("Retrieval:    %s\n", view.RetrievalStatus)
	fmt.Printf("Terminal:     %t\n", view.Terminal)
	fmt.Printf("Field:        %s\n", view.Field)
	fmt.Printf("Max chars:    %d\n", view.MaxChars)
	fmt.Printf("Total chars:  %d\n", view.TotalChars)
	fmt.Printf("Truncated:    %t\n", view.Truncated)
	fmt.Printf("Content:\n%s\n", view.Content)
}

func printTaskStop(view taskStopCLIOutput) {
	fmt.Printf("ID:              %d\n", view.TaskID)
	fmt.Printf("Accepted:        %t\n", view.Accepted)
	fmt.Printf("Stopped:         %t\n", view.Stopped)
	fmt.Printf("Terminal:        %t\n", view.Terminal)
	fmt.Printf("Previous status: %s\n", view.PreviousStatus)
	fmt.Printf("Status:          %s\n", view.Status)
	if view.FollowupTool != "" {
		fmt.Printf("Follow-up:       %s\n", view.FollowupTool)
	}
	fmt.Printf("Reason:          %s\n", view.Reason)
}

func parseTaskID(raw string) (int64, error) {
	taskID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid task ID %q: %w", raw, err)
	}
	if taskID <= 0 {
		return 0, fmt.Errorf("invalid task ID %q: must be positive", raw)
	}
	return taskID, nil
}

func parseIntFlag(args []string, i int, name string) (int, int, error) {
	value, next, err := parseStringFlag(args, i, name)
	if err != nil {
		return 0, i, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, i, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, next, nil
}

func parseStringFlag(args []string, i int, name string) (string, int, error) {
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("%s requires a value", name)
	}
	return args[i+1], i + 1, nil
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func openTaskQueue() (*core.DB, *daemon.Queue, error) {
	_, db, err := openTaskDB()
	if err != nil {
		return nil, nil, err
	}
	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("open queue: %w", err)
	}
	return db, queue, nil
}

func openTaskDB() (*config.Config, *core.DB, error) {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	return cfg, db, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() || t.UnixMilli() == 0 {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}
