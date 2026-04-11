package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/telegram"
	"github.com/stello/elnath/internal/wiki"
)

func cmdDaemon(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Println(`Usage: elnath daemon <subcommand>

Subcommands:
  start              Start the daemon (blocks until stopped)
  submit <task>      Submit a task to the running daemon
  status             List queued and running tasks
  stop               Gracefully stop the running daemon
  install            Install launchd plist for auto-start`)
		return nil
	}
	switch args[0] {
	case "start":
		return cmdDaemonStart(ctx)
	case "submit":
		return cmdDaemonSubmit(ctx, args[1:])
	case "status":
		return cmdDaemonStatus(ctx)
	case "stop":
		return cmdDaemonStop(ctx)
	case "install":
		return cmdDaemonInstall(ctx)
	default:
		return fmt.Errorf("unknown daemon subcommand: %s", args[0])
	}
}

func cmdDaemonStart(ctx context.Context) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, err := core.New(cfg)
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer app.Close()

	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	app.RegisterCloser("database", db)

	provider, model, err := buildProvider(cfg)
	if err != nil {
		return fmt.Errorf("build provider: %w", err)
	}

	mode := parsePermissionMode(cfg.Permission.Mode)
	permOpts := []agent.PermissionOption{
		agent.WithMode(mode),
		agent.WithAllowList(cfg.Permission.Allow...),
		agent.WithDenyList(cfg.Permission.Deny...),
	}
	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
		approvalStore, err := daemon.NewApprovalStore(db.Main)
		if err != nil {
			return fmt.Errorf("create approval store: %w", err)
		}
		permOpts = append(permOpts, agent.WithPrompter(daemon.NewApprovalPrompter(approvalStore, 500*time.Millisecond)))
	}
	perm := agent.NewPermission(permOpts...)

	selfState, err := self.Load(cfg.DataDir)
	if err != nil {
		app.Logger.Warn("failed to load self state for daemon, using defaults", "error", err)
		selfState = self.New(cfg.DataDir)
	}
	daemonPrompt := self.BuildSystemPrompt(selfState, "")

	workDir := cfg.Daemon.WorkDir
	if workDir == "" {
		home, _ := os.UserHomeDir()
		workDir = filepath.Join(home, ".elnath", "workspace")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}
	fallbackPrincipal := identity.ResolveCLIPrincipal(cfg, extractFlagValue(os.Args, "--principal"), workDir)
	fallbackPrincipal.ProjectID = identity.ResolveProjectID(workDir, extractFlagValue(os.Args, "--project-id"))
	app.Logger.Info("daemon workspace", "dir", workDir)

	rt, err := buildExecutionRuntime(
		ctx,
		cfg,
		app,
		db,
		provider,
		model,
		daemonPrompt,
		perm,
		workDir,
		cfg.Daemon.ProtectedPaths,
		fallbackPrincipal,
	)
	if err != nil {
		return err
	}

	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		return fmt.Errorf("create queue: %w", err)
	}

	router, err := daemon.NewDeliveryRouter(db.Main, app.Logger)
	if err != nil {
		return fmt.Errorf("create delivery router: %w", err)
	}
	router.Register(daemon.NewLogSink(app.Logger))
	if rt.wikiStore != nil {
		router.Register(conversation.NewSpine(cfg.DataDir, wiki.NewIngester(rt.wikiStore, provider), app.Logger))
	}

	d := daemon.New(queue, cfg.Daemon.SocketPath, cfg.Daemon.MaxWorkers, rt.newDaemonTaskRunner(), app.Logger)
	d.WithDeliveryRouter(router)
	d.WithFallbackPrincipal(fallbackPrincipal)
	d.WithTimeouts(
		time.Duration(cfg.Daemon.InactivityTimeout)*time.Second,
		time.Duration(cfg.Daemon.WallClockTimeout)*time.Second,
	)

	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
		approvalStore, approvalErr := daemon.NewApprovalStore(db.Main)
		if approvalErr != nil {
			return fmt.Errorf("create approval store for telegram: %w", approvalErr)
		}
		bot := telegram.NewHTTPClient(cfg.Telegram.BotToken, cfg.Telegram.APIBaseURL)
		statePath := filepath.Join(cfg.DataDir, "telegram-shell-state.json")
		tgSink := telegram.NewTelegramSink(bot, cfg.Telegram.ChatID, app.Logger)
		chatResponder := telegram.NewChatResponder(provider, bot, cfg.Telegram.ChatID, app.Logger)
		classifier := conversation.NewLLMClassifier()
		shell, shellErr := telegram.NewShell(queue, approvalStore, bot, cfg.Telegram.ChatID, statePath,
			telegram.WithChatResponder(chatResponder),
			telegram.WithClassifier(classifier, provider),
			telegram.WithTaskTracker(tgSink),
			telegram.WithWorkDir(workDir),
		)
		if shellErr != nil {
			return fmt.Errorf("create telegram shell: %w", shellErr)
		}
		router.Register(tgSink)
		d.WithProgressObserver(tgSink)
		shell.SkipNotifyCompletions()

		go func() {
			if err := runTelegramShell(ctx, shell, bot, cfg.Telegram.PollTimeoutSeconds, app.Logger); err != nil && ctx.Err() == nil {
				app.Logger.Error("telegram shell stopped", "error", err)
			}
		}()
		app.Logger.Info("telegram shell embedded in daemon")
	}

	return d.Start(ctx)
}

func cmdDaemonSubmit(ctx context.Context, args []string) error {
	sessionID, prompt, err := parseDaemonSubmitArgs(args)
	if err != nil {
		return err
	}
	if prompt == "" {
		return fmt.Errorf("usage: elnath daemon submit [--session <session-id>] <task description>")
	}
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get cwd: %w", err)
	}
	principal := identity.ResolveCLIPrincipal(cfg, extractFlagValue(os.Args, "--principal"), cwd)
	principal.ProjectID = identity.ResolveProjectID(cwd, extractFlagValue(os.Args, "--project-id"))

	payload := daemon.EncodeTaskPayload(daemon.TaskPayload{
		Prompt:    prompt,
		SessionID: sessionID,
		Surface:   principal.Surface,
		Principal: principal,
	})
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req := daemon.IPCRequest{
		Command: "submit",
		Payload: json.RawMessage(payloadJSON),
	}
	resp, err := sendIPCRequest(cfg.Daemon.SocketPath, req)
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
	if json.Unmarshal(data, &result) == nil && result.TaskID > 0 {
		if result.Existed {
			fmt.Printf("Task #%d already running (deduplicated)\n", int64(result.TaskID))
			return nil
		}
		fmt.Printf("Task #%d enqueued\n", int64(result.TaskID))
		return nil
	}
	fmt.Printf("Task submitted: %s\n", string(data))
	return nil
}

func parseDaemonSubmitArgs(args []string) (sessionID string, prompt string, err error) {
	var parts []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--session" {
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("usage: elnath daemon submit [--session <session-id>] <task description>")
			}
			sessionID = args[i+1]
			i++
			continue
		}
		if args[i] == "--config" || args[i] == "--principal" || args[i] == "--project-id" {
			if i+1 < len(args) {
				i++
			}
			continue
		}
		parts = append(parts, args[i])
	}
	prompt = strings.TrimSpace(strings.Join(parts, " "))
	return sessionID, prompt, nil
}

func cmdDaemonStatus(ctx context.Context) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	req := daemon.IPCRequest{Command: "status"}
	resp, err := sendIPCRequest(cfg.Daemon.SocketPath, req)
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
			SessionID string  `json:"session_id"`
			Progress  string  `json:"progress"`
			Summary   string  `json:"summary"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		fmt.Printf("Raw response: %s\n", string(data))
		return nil
	}
	if len(result.Tasks) == 0 {
		fmt.Println("No tasks.")
		return nil
	}

	fmt.Printf("%-6s  %-12s  %-16s  %-28s  %-28s  %s\n", "ID", "STATUS", "SESSION", "PROGRESS", "SUMMARY", "PAYLOAD")
	fmt.Printf("%-6s  %-12s  %-16s  %-28s  %-28s  %s\n", "------", "------------", "----------------", "----------------------------", "----------------------------", "------------------------------------------------------------")
	for _, t := range result.Tasks {
		payload := t.Payload
		if len(payload) > 60 {
			payload = payload[:57] + "..."
		}
		progress := daemon.RenderProgress(t.Progress)
		if len(progress) > 28 {
			progress = progress[:25] + "..."
		}
		sessionID := t.SessionID
		if len(sessionID) > 16 {
			sessionID = sessionID[:13] + "..."
		}
		summary := t.Summary
		if len(summary) > 28 {
			summary = summary[:25] + "..."
		}
		fmt.Printf("%-6.0f  %-12s  %-16s  %-28s  %-28s  %s\n", t.ID, t.Status, sessionID, progress, summary, payload)
	}
	return nil
}

func cmdDaemonStop(ctx context.Context) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	req := daemon.IPCRequest{Command: "stop"}
	resp, err := sendIPCRequest(cfg.Daemon.SocketPath, req)
	if err != nil {
		return fmt.Errorf("ipc: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Err)
	}
	fmt.Println("Daemon stop requested.")
	return nil
}

func cmdDaemonInstall(_ context.Context) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	plistPath, err := daemon.InstallPlist(binaryPath, cfg.Daemon.SocketPath)
	if err != nil {
		return fmt.Errorf("install plist: %w", err)
	}
	fmt.Printf("Installed launchd plist: %s\n", plistPath)
	fmt.Println("Run: launchctl load", plistPath)
	return nil
}

func sendIPCRequest(socketPath string, req daemon.IPCRequest) (*daemon.IPCResponse, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon at %s: %w", socketPath, err)
	}
	defer conn.Close()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("daemon closed connection without response")
	}

	var resp daemon.IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &resp, nil
}
