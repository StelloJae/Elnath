package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	researchpkg "github.com/stello/elnath/internal/research"
)

func cmdResearch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return researchHelp()
	}
	switch args[0] {
	case "start":
		return researchStart(ctx, args[1:])
	case "status":
		return researchStatus(ctx, args[1:])
	case "result":
		return researchResult(ctx, args[1:])
	case "help", "--help", "-h":
		return researchHelp()
	default:
		return fmt.Errorf("unknown research subcommand: %s", args[0])
	}
}

func researchHelp() error {
	fmt.Println(`Usage: elnath research <subcommand> [args]

Subcommands:
  start <topic>     Queue a new research task for <topic>
  status            List all research tasks
  result <task_id>  Show final result of a completed research task
  help              Show this help`)
	return nil
}

func researchStart(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("topic required")
	}
	topic := strings.TrimSpace(strings.Join(args, " "))
	if topic == "" {
		return fmt.Errorf("topic required")
	}
	cfg, err := loadResearchConfig()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get cwd: %w", err)
	}
	principal := identity.ResolveCLIPrincipal(cfg, extractFlagValue(os.Args, "--principal"), cwd)
	principal.ProjectID = identity.ResolveProjectID(cwd, extractFlagValue(os.Args, "--project-id"))

	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Type:      daemon.TaskTypeResearch,
		Prompt:    topic,
		Surface:   principal.Surface,
		Principal: principal,
	})
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	resp, err := sendIPCRequest(cfg.Daemon.SocketPath, daemon.IPCRequest{
		Command: "submit",
		Payload: json.RawMessage(payloadJSON),
	})
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Err)
	}

	data, _ := json.Marshal(resp.Data)
	var result struct {
		TaskID  float64 `json:"task_id"`
		Existed bool    `json:"existed"`
	}
	if err := json.Unmarshal(data, &result); err == nil && result.TaskID > 0 {
		if result.Existed {
			fmt.Printf("Research task #%d already running (deduplicated)\n", int64(result.TaskID))
			return nil
		}
		fmt.Printf("Research task queued: %d (topic: %s)\n", int64(result.TaskID), topic)
		return nil
	}
	fmt.Printf("Research task queued: %s\n", string(data))
	return nil
}

func researchStatus(_ context.Context, _ []string) error {
	cfg, err := loadResearchConfig()
	if err != nil {
		return err
	}
	resp, err := sendIPCRequest(cfg.Daemon.SocketPath, daemon.IPCRequest{Command: "status"})
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Err)
	}

	data, _ := json.Marshal(resp.Data)
	var result struct {
		Tasks []struct {
			ID        float64 `json:"id"`
			Status    string  `json:"status"`
			Payload   string  `json:"payload"`
			UpdatedAt float64 `json:"updated_at"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("decode status response: %w", err)
	}

	rows := 0
	fmt.Printf("%-6s %-10s %-40s %s\n", "ID", "STATUS", "TOPIC", "AGE")
	for _, task := range result.Tasks {
		payload := daemon.ParseTaskPayload(task.Payload)
		if payload.Type != daemon.TaskTypeResearch {
			continue
		}
		rows++
		fmt.Printf("%-6.0f %-10s %-40s %s\n", task.ID, task.Status, truncate(payload.Prompt, 40), formatResearchAge(task.UpdatedAt))
	}
	if rows == 0 {
		fmt.Println("No research tasks.")
	}
	return nil
}

func researchResult(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("task_id required")
	}
	wantID := args[0]
	cfg, err := loadResearchConfig()
	if err != nil {
		return err
	}
	resp, err := sendIPCRequest(cfg.Daemon.SocketPath, daemon.IPCRequest{Command: "status"})
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Err)
	}

	data, _ := json.Marshal(resp.Data)
	var result struct {
		Tasks []struct {
			ID      float64 `json:"id"`
			Status  string  `json:"status"`
			Payload string  `json:"payload"`
			Summary string  `json:"summary"`
			Result  string  `json:"result"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("decode status response: %w", err)
	}

	for _, task := range result.Tasks {
		if strconv.FormatInt(int64(task.ID), 10) != wantID {
			continue
		}
		payload := daemon.ParseTaskPayload(task.Payload)
		if payload.Type != daemon.TaskTypeResearch {
			break
		}
		if task.Status != "done" {
			fmt.Printf("Task %s status: %s\n", wantID, task.Status)
			return nil
		}

		var rr researchpkg.ResearchResult
		if err := json.Unmarshal([]byte(task.Result), &rr); err != nil {
			fmt.Println("Summary:", task.Summary)
			return nil
		}
		fmt.Printf("Topic: %s\n", rr.Topic)
		fmt.Printf("Total cost: $%.4f\n", rr.TotalCost)
		fmt.Println("Summary:")
		fmt.Println(rr.Summary)
		fmt.Printf("\nRounds: %d\n", len(rr.Rounds))
		for i, round := range rr.Rounds {
			fmt.Printf("-- Round %d --\n", i+1)
			fmt.Printf("Hypothesis: %s\n", round.Hypothesis.Statement)
			fmt.Printf("Supported: %t\n", round.Result.Supported)
			fmt.Printf("Confidence: %s\n", round.Result.Confidence)
			fmt.Printf("Findings: %s\n", round.Result.Findings)
		}
		return nil
	}
	return fmt.Errorf("task %s not found", wantID)
}

func loadResearchConfig() (*config.Config, error) {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

func formatResearchAge(raw any) string {
	switch v := raw.(type) {
	case float64:
		return time.Since(time.UnixMilli(int64(v))).Round(time.Second).String() + " ago"
	case int64:
		return time.Since(time.UnixMilli(v)).Round(time.Second).String() + " ago"
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return "-"
		}
		return time.Since(t).Round(time.Second).String() + " ago"
	default:
		return "-"
	}
}
