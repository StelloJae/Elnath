package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sort"

	"github.com/stello/elnath/internal/ambient"
	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/agent/reflection"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/fault"
	"github.com/stello/elnath/internal/fault/scenarios"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/research"
	"github.com/stello/elnath/internal/scheduler"
	"github.com/stello/elnath/internal/secret"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/telegram"
	"github.com/stello/elnath/internal/userfacingerr"
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
		return cmdDaemonStatus(ctx, args[1:])
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
	applyGlobalFlagOverrides(cfg, os.Args)
	scenarioName, err := fault.CheckGuards(fault.GuardConfig{Enabled: cfg.FaultInjection.Enabled})
	if err != nil {
		return err
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
	registry := fault.NewRegistry(scenarios.All())
	inj := fault.Injector(fault.NoopInjector{})
	var activeScenario *fault.Scenario
	if scenarioName != "" {
		scenario, ok := registry.Get(scenarioName)
		if !ok {
			return fmt.Errorf("unknown fault scenario %q", scenarioName)
		}
		activeScenario = scenario
		inj = fault.NewScenarioInjector(scenario, time.Now().UnixNano())
		app.Logger.Warn("fault injection ACTIVE", "scenario", scenarioName)
		if scenario.Category == fault.CategoryLLM {
			provider = fault.NewLLMFaultHook(provider, inj, scenario)
		}
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
		selfState,
		"",
		perm,
		workDir,
		cfg.Daemon.ProtectedPaths,
		fallbackPrincipal,
		true,
	)
	if err != nil {
		return err
	}
	if activeScenario != nil && activeScenario.Category == fault.CategoryTool {
		rt.wfCfg.ToolExecutor = fault.NewToolFaultHook(rt.reg, inj, activeScenario)
	}
	autoRotateLessons(app.Logger, rt.learningStore, learning.RotateOpts{
		KeepLast: 5000,
		MaxBytes: 1 << 20,
	})

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
	d.WithFaultGuardConfig(fault.GuardConfig{Enabled: cfg.FaultInjection.Enabled})
	d.MarkFaultGuardChecked()
	d.WithFaultInjection(inj, activeScenario)
	d.WithDeliveryRouter(router)
	d.WithFallbackPrincipal(fallbackPrincipal)
	d.WithTimeouts(
		time.Duration(cfg.Daemon.InactivityTimeout)*time.Second,
		time.Duration(cfg.Daemon.WallClockTimeout)*time.Second,
	)
	if rt.wikiIdx != nil && rt.wikiStore != nil {
		researchOpts := []research.TaskRunnerOption{
			research.WithRunnerMaxRounds(cfg.Research.MaxRounds),
			research.WithRunnerCostCap(cfg.Research.CostCapUSD),
			research.WithToolRegistry(rt.reg),
			research.WithRunnerLearning(rt.learningStore),
			research.WithRunnerSelfState(selfState),
		}
		if activeScenario != nil && activeScenario.Category == fault.CategoryTool {
			researchOpts = append(researchOpts, research.WithToolExecutor(fault.NewToolFaultHook(rt.reg, inj, activeScenario)))
		}
		d.SetResearchRunner(research.NewTaskRunner(
			provider,
			model,
			rt.wikiIdx,
			rt.wikiStore,
			rt.usageTracker,
			app.Logger,
			researchOpts...,
		))
	}
	sch, scheduledPath, taskCount, err := loadScheduler(cfg, queue, app.Logger)
	if err != nil {
		app.Logger.Error("scheduler config load failed", "path", scheduledPath, "error", err)
		return err
	}
	if scheduledPath != "" {
		if sch != nil {
			d.WithScheduler(sch)
			app.Logger.Info("scheduler enabled", "path", scheduledPath, "tasks", taskCount)
		} else {
			app.Logger.Info("scheduler config empty or all disabled", "path", scheduledPath)
		}
	}

	if cfg.Ambient.Enabled && rt.wikiStore != nil {
		ambientScanner := ambient.NewScanner(rt.wikiStore, app.Logger.With("component", "ambient-scanner"))
		bootTasks, scanErr := ambientScanner.Scan()
		if scanErr != nil {
			app.Logger.Warn("ambient scan failed", "error", scanErr)
		}
		if len(bootTasks) > 0 {
			var notifyFn ambient.NotifyFunc
			if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
				tgBot := telegram.NewHTTPClient(cfg.Telegram.BotToken, cfg.Telegram.APIBaseURL)
				chatID := cfg.Telegram.ChatID
				notifyFn = func(ctx context.Context, title, body string) error {
					return tgBot.SendMessage(ctx, chatID, title+"\n\n"+body)
				}
			}

			maxConc := cfg.Ambient.MaxConcurrent
			if maxConc <= 0 {
				maxConc = 2
			}

			ambientSched := ambient.NewScheduler(ambient.Config{
				Tasks:         bootTasks,
				Runner:        ambient.TaskRunFunc(rt.newDaemonTaskRunner()),
				NotifyFn:      notifyFn,
				MaxConcurrent: maxConc,
				Logger:        app.Logger.With("component", "ambient"),
			})
			ambientSched.Start(ctx)
			defer ambientSched.Stop()
			app.Logger.Info("ambient scheduler active", "boot_tasks", len(bootTasks))
		}

		// Lesson consolidation scheduler — runs Consolidator.Run once per day
		// at 04:00 local time. This is separate from the ambient boot-task
		// scheduler above because consolidation is a deterministic pipeline
		// (not an agent-language task), and because it needs typed access to
		// Consolidator rather than a natural-language prompt.
		if rt.learningStore != nil && rt.wikiStore != nil {
			consolidationDeps, err := buildConsolidationDepsFromConfig(cfg, provider, rt.wikiStore, rt.learningStore, model)
			if err != nil {
				app.Logger.Warn("consolidation scheduler disabled", "error", err)
			} else {
				consolidator := newConsolidator(consolidationDeps, false)
				go learning.RunDailyConsolidationLoop(ctx, consolidator, ambient.TimeOfDay{Hour: 4, Minute: 0}, app.Logger)
				app.Logger.Info("consolidation scheduler active", "schedule", "daily 04:00")
			}
		}
	}

	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
		approvalStore, approvalErr := daemon.NewApprovalStore(db.Main)
		if approvalErr != nil {
			return fmt.Errorf("create approval store for telegram: %w", approvalErr)
		}
		bot := telegram.NewHTTPClient(cfg.Telegram.BotToken, cfg.Telegram.APIBaseURL)
		statePath := filepath.Join(cfg.DataDir, "telegram-shell-state.json")
		binderPath := filepath.Join(cfg.DataDir, "telegram-chat-bindings.json")
		binder, binderErr := telegram.NewChatSessionBinder(binderPath, telegram.FileSessionValidator{DataDir: cfg.DataDir})
		if binderErr != nil {
			return fmt.Errorf("telegram: init binder: %w", binderErr)
		}
		tgSink := telegram.NewTelegramSink(bot, cfg.Telegram.ChatID, app.Logger,
			telegram.WithSinkBinder(binder),
			telegram.WithRedactor(secret.NewDetector().RedactString),
		)
		chatResponder := telegram.NewChatResponder(provider, bot, cfg.Telegram.ChatID, app.Logger,
			telegram.WithOutcomeStore(rt.outcomeStore),
		)
		classifier := conversation.NewLLMClassifier()
		shell, shellErr := telegram.NewShell(queue, approvalStore, bot, cfg.Telegram.ChatID, statePath, rt.skillReg,
			telegram.WithChatResponder(chatResponder),
			telegram.WithChatSessionBinder(binder),
			telegram.WithClassifier(classifier, provider),
			telegram.WithSkillCreator(rt.skillCreator),
			telegram.WithTaskTracker(tgSink),
			telegram.WithWorkDir(workDir),
			telegram.WithLearningStore(rt.learningStore),
			telegram.WithWikiStore(rt.wikiStore),
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

func autoRotateLessons(logger *slog.Logger, store *learning.Store, opts learning.RotateOpts) {
	if store == nil {
		return
	}
	n, err := store.AutoRotateIfNeeded(opts)
	if logger == nil {
		return
	}
	if err != nil {
		logger.Warn("learning: auto-rotate failed", "error", err)
	} else if n > 0 {
		logger.Info("learning: auto-rotated lessons", "moved", n)
	}
}

func loadScheduler(cfg *config.Config, queue scheduler.Enqueuer, logger *slog.Logger) (daemon.Scheduler, string, int, error) {
	if cfg.Daemon.ScheduledTasksPath == "" {
		return nil, "", 0, nil
	}

	path := cfg.Daemon.ScheduledTasksPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(cfg.DataDir, path)
	}

	tasks, err := scheduler.LoadConfig(path)
	if err != nil {
		return nil, path, 0, fmt.Errorf("scheduler: %w", err)
	}
	if len(tasks) == 0 {
		return nil, path, 0, nil
	}

	return scheduler.New(tasks, queue, logger), path, len(tasks), nil
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
	if sessionID != "" {
		resolved, err := agent.ResolveSessionID(cfg.DataDir, sessionID)
		if err != nil {
			return fmt.Errorf("resolve --session: %w", err)
		}
		sessionID = resolved
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

func cmdDaemonStatus(ctx context.Context, args []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if hasFlag(args, "--self-heal") {
		return printSelfHealStatus(cfg)
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

// printSelfHealStatus renders a Phase 0 reflection observation summary.
// Reads the JSONL directly (no IPC) so callers can inspect history even when
// the daemon is offline.
func printSelfHealStatus(cfg *config.Config) error {
	path := cfg.SelfHealing.Path
	if path == "" {
		path = filepath.Join(cfg.DataDir, "self_heal_attempts.jsonl")
	}
	store := reflection.NewFileStore(path)
	sum, err := store.Read()
	if err != nil {
		return fmt.Errorf("read self-heal store: %w", err)
	}
	status := "enabled"
	if !cfg.SelfHealing.Enabled {
		status = "disabled"
	}
	fmt.Println("Self-Heal Observations (Phase 0)")
	fmt.Printf("  status:                 %s\n", status)
	fmt.Printf("  store:                  %s\n", path)
	fmt.Printf("  total attempts:         %d\n", sum.Total)
	if sum.Total == 0 {
		fmt.Println("  (no observations recorded yet)")
		return nil
	}
	fmt.Printf("  by finish_reason:       %s\n", formatCountMap(sum.FinishReason))
	fmt.Printf("  by error_category:      %s\n", formatCountMap(sum.ErrorCategory))
	fmt.Printf("  strategy distribution:  %s\n", formatCountMap(sum.StrategyCounts))
	fmt.Printf("  schema fail rate:       %d/%d = %.1f%%\n", sum.SchemaFailures, sum.Total, sum.SchemaFailureRate*100)
	fmt.Printf("  sample window:          %s → %s\n",
		sum.FirstTS.Format(time.RFC3339),
		sum.LastTS.Format(time.RFC3339),
	)
	return nil
}

// formatCountMap renders a map in "key=count, key=count" form sorted by key
// so repeated invocations produce deterministic output.
func formatCountMap(m map[string]int) string {
	if len(m) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, ", ")
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
		inner := fmt.Errorf("connect to daemon at %s: %w", socketPath, err)
		return nil, userfacingerr.Wrap(userfacingerr.ELN030, inner, "daemon ipc")
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

	var resp daemon.IPCResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &resp, nil
}
