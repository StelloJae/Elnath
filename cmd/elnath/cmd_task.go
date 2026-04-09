package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
)

func cmdTask(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Println(`Usage: elnath task <subcommand>

Subcommands:
  list               List recent tasks (last 20)
  show <id>          Show task details
  resume <id>        Resume the session created by a task`)
		return nil
	}
	switch args[0] {
	case "list":
		return cmdTaskList(ctx)
	case "show":
		return cmdTaskShow(ctx, args[1:])
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
