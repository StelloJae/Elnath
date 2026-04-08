package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"github.com/mattn/go-isatty"
	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/mcp"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/onboarding"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

type commandRunner func(ctx context.Context, args []string) error

func commandRegistry() map[string]commandRunner {
	return map[string]commandRunner{
		"version": cmdVersion,
		"help":    cmdHelp,
		"run":     cmdRun,
		"setup":   cmdSetup,
		"daemon":  cmdDaemon,
		"wiki":    cmdWiki,
		"search":  cmdSearch,
	}
}

func executeCommand(ctx context.Context, name string, args []string) error {
	registry := commandRegistry()
	cmd, ok := registry[name]
	if !ok {
		fmt.Fprintln(os.Stderr, fmt.Sprintf(onboarding.T(loadLocale(), "cli.unknown_command"), name))
		return cmdHelp(ctx, args)
	}
	return cmd(ctx, args)
}

func cmdVersion(_ context.Context, _ []string) error {
	fmt.Printf("elnath %s\n", version)
	return nil
}

func cmdHelp(_ context.Context, _ []string) error {
	fmt.Println(onboarding.T(loadLocale(), "cli.help"))
	return nil
}

// loadLocale reads the locale from the existing config, defaulting to English.
func loadLocale() onboarding.Locale {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	if cfg, err := config.Load(cfgPath); err == nil && cfg.Locale != "" {
		return onboarding.Locale(cfg.Locale)
	}
	return onboarding.En
}

func cmdSetup(_ context.Context, _ []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}

	// Load existing config for locale and rerun defaults.
	locale := onboarding.En
	var rerunOpts []onboarding.Option
	rerunOpts = append(rerunOpts, onboarding.WithRerunMode())
	if existing, err := config.Load(cfgPath); err == nil {
		if existing.Locale != "" {
			locale = onboarding.Locale(existing.Locale)
		}
		rerunOpts = append(rerunOpts, onboarding.WithExistingConfig(onboarding.ExistingConfig{
			Locale:         onboarding.Locale(existing.Locale),
			APIKey:         existing.Anthropic.APIKey,
			PermissionMode: existing.Permission.Mode,
			DataDir:        existing.DataDir,
			WikiDir:        existing.WikiDir,
		}))
	}

	// Back up existing config if present.
	if _, err := os.Stat(cfgPath); err == nil {
		backupPath := cfgPath + ".bak"
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("read existing config for backup: %w", err)
		}
		if err := os.WriteFile(backupPath, data, 0o600); err != nil {
			return fmt.Errorf("write config backup: %w", err)
		}
		fmt.Printf(onboarding.T(locale, "setup.backup")+"\n", backupPath)
	}

	result, err := onboarding.Run(cfgPath, version, rerunOpts...)
	if err != nil {
		return fmt.Errorf("setup wizard: %w", err)
	}

	cfgResult := onboardingResultToConfig(result)
	return config.WriteFromResult(cfgPath, cfgResult)
}

func cmdRun(ctx context.Context, args []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}

	// First-run onboarding: TUI wizard for terminals, text fallback for pipes/CI.
	nonInteractive := hasFlag(os.Args, "--non-interactive")
	if config.NeedsOnboarding(cfgPath) {
		isTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
		if isTTY && !nonInteractive {
			result, err := onboarding.Run(cfgPath, version)
			if err != nil {
				return fmt.Errorf("onboarding: %w", err)
			}
			cfgResult := onboardingResultToConfig(result)
			if err := config.WriteFromResult(cfgPath, cfgResult); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
		} else if nonInteractive {
			// Fully non-interactive: env vars + defaults only.
			if _, err := config.RunNonInteractiveOnboarding(cfgPath); err != nil {
				return fmt.Errorf("onboarding: %w", err)
			}
		} else {
			// Piped stdin: text-based prompts with env var priority.
			if _, err := config.RunOnboarding(cfgPath, os.Stdin, os.Stdout); err != nil {
				return fmt.Errorf("onboarding: %w", err)
			}
		}
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

	// Load self state.
	selfState, err := self.Load(cfg.DataDir)
	if err != nil {
		app.Logger.Warn("failed to load self state, using defaults", "error", err)
		selfState = self.New(cfg.DataDir)
	}

	// Apply persona preset if specified.
	personaExtra := ""
	if pName := extractPersonaFlag(os.Args); pName != "" {
		preset := self.PresetName(pName)
		persona, extra := self.Preset(preset)
		if extra != "" {
			selfState.Persona = persona
			personaExtra = extra
			app.Logger.Info("persona preset applied", "preset", pName)
		} else {
			app.Logger.Warn("unknown persona preset, using defaults", "preset", pName)
		}
	}

	provider, model, err := buildProvider(cfg)
	if err != nil {
		return core.NewUserError("No LLM provider configured. Set ELNATH_ANTHROPIC_API_KEY or add anthropic.api_key to config.yaml", err)
	}

	// Open databases.
	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	app.RegisterCloser("database", db)

	// Initialize conversation history schema.
	if err := conversation.InitSchema(db.Main); err != nil {
		return fmt.Errorf("init conversation schema: %w", err)
	}

	// Build conversation manager with all dependencies.
	historyStore := conversation.NewHistoryStore(db.Main)
	classifier := conversation.NewLLMClassifier()
	ctxWindow := conversation.NewContextWindow()
	mgr := conversation.NewManager(db.Main, cfg.DataDir).
		WithProvider(provider).
		WithClassifier(classifier).
		WithContextWindow(ctxWindow).
		WithHistoryStore(historyStore).
		WithLogger(app.Logger)

	// Build tool registry (with wiki tools if wiki is available).
	cwd, _ := os.Getwd()
	reg := buildToolRegistry(cwd)
	gitSync, wikiIdx := registerWikiTools(reg, cfg.WikiDir, db.Wiki)
	reg.Register(conversation.NewConversationSearchTool(historyStore))

	// Register cross-project search tools if projects are configured.
	if len(cfg.Projects) > 0 {
		registerCrossProjectTools(reg, cfg.Projects, app)
	}

	// Start MCP servers and register their tools (failures are non-fatal).
	if len(cfg.MCPServers) > 0 {
		registerMCPTools(ctx, reg, cfg.MCPServers, app)
	}

	// Build permission engine.
	mode := parsePermissionMode(cfg.Permission.Mode)
	perm := agent.NewPermission(
		agent.WithMode(mode),
		agent.WithAllowList(cfg.Permission.Allow...),
		agent.WithDenyList(cfg.Permission.Deny...),
		agent.WithPrompter(&cliPrompter{}),
	)

	// Build hooks from config.
	var hooks *agent.HookRegistry
	if len(cfg.Hooks) > 0 {
		hooks = agent.NewHookRegistry()
		for _, hc := range cfg.Hooks {
			hooks.Add(&agent.CommandHook{
				Matcher: hc.Matcher,
				PreCmd:  hc.PreCommand,
				PostCmd: hc.PostCommand,
			})
		}
	}

	// Build workflow router.
	wfCfg := orchestrator.WorkflowConfig{
		Model:        model,
		SystemPrompt: self.BuildSystemPromptWithPersona(selfState, "", personaExtra),
		Hooks:        hooks,
		Permission:   perm,
	}
	router := buildRouter(wfCfg)

	// Create session.
	sess, err := mgr.NewSession()
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	app.Logger.Info("session started", "id", sess.ID)

	var messages []llm.Message

	// Parse optional initial prompt from args.
	if len(args) > 0 {
		prompt := strings.Join(args, " ")
		messages, err = runOrchestrated(ctx, mgr, router, provider, reg, perm, sess, messages, prompt, wfCfg, wikiIdx, app)
		if err != nil {
			return err
		}
		if gitSync != nil {
			if cerr := gitSync.Commit("auto: wiki update"); cerr != nil {
				app.Logger.Warn("wiki git commit failed", "error", cerr)
			}
		}
	}

	// REPL loop.
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("elnath %s  (session %s)\nType your message, empty line to quit.\n\n", version, sess.ID)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}

		messages, err = runOrchestrated(ctx, mgr, router, provider, reg, perm, sess, messages, line, wfCfg, wikiIdx, app)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		if gitSync != nil {
			if cerr := gitSync.Commit("auto: wiki update"); cerr != nil {
				app.Logger.Warn("wiki git commit failed", "error", cerr)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stdin: %w", err)
	}

	// Auto-document session to wiki.
	if cfg.WikiDir != "" {
		wikiStore, werr := wiki.NewStore(cfg.WikiDir)
		if werr == nil {
			ad := wiki.NewAutoDocumenter(wikiStore, provider, app.Logger)
			if ierr := ad.IngestSession(ctx, sess.ID, messages); ierr != nil {
				app.Logger.Warn("auto-documentation failed", "error", ierr)
			} else {
				app.Logger.Info("session auto-documented to wiki", "session", sess.ID)
				if gitSync != nil {
					if cerr := gitSync.Commit("auto: document session " + sess.ID); cerr != nil {
						app.Logger.Warn("wiki git commit failed", "error", cerr)
					}
				}
			}
		}
	}

	return nil
}

// runOrchestrated processes a user message through intent classification → routing → workflow execution.
func runOrchestrated(
	ctx context.Context,
	mgr *conversation.Manager,
	router *orchestrator.Router,
	provider llm.Provider,
	reg *tools.Registry,
	perm *agent.Permission,
	sess *agent.Session,
	messages []llm.Message,
	userInput string,
	wfCfg orchestrator.WorkflowConfig,
	wikiIdx *wiki.Index,
	app *core.App,
) ([]llm.Message, error) {
	// Classify intent and prepare messages via conversation manager.
	prepared, intent, err := mgr.SendMessage(ctx, sess.ID, userInput)
	if err != nil {
		app.Logger.Warn("conversation manager fallback", "error", err)
		prepared = append(messages, llm.NewUserMessage(userInput))
		intent = conversation.IntentUnclear
	}

	// Route intent to workflow.
	wf := router.Route(intent, nil)
	if wf == nil {
		return nil, fmt.Errorf("no workflow available for intent %q", intent)
	}

	app.Logger.Info("routed",
		"intent", string(intent),
		"workflow", wf.Name(),
		"session", sess.ID,
	)
	fmt.Printf("[%s → %s]\n", intent, wf.Name())

	// Inject wiki RAG context into system prompt.
	cfg := wfCfg
	if wikiIdx != nil {
		if ragCtx := wiki.BuildRAGContext(ctx, wikiIdx, userInput, 3); ragCtx != "" {
			cfg.SystemPrompt += "\n\n" + ragCtx
		}
	}

	// Execute workflow.
	input := orchestrator.WorkflowInput{
		Message:  userInput,
		Messages: prepared,
		Session:  sess,
		Tools:    reg,
		Provider: provider,
		Config:   cfg,
		OnText:   func(s string) { fmt.Print(s) },
	}

	fmt.Println()
	result, err := wf.Run(ctx, input)
	fmt.Println()
	if err != nil {
		return nil, fmt.Errorf("workflow %s: %w", wf.Name(), err)
	}

	if summary := llm.FormatUsageSummary(wfCfg.Model, result.Usage); summary != "" {
		fmt.Println(summary)
	}

	// Persist new messages.
	if err := sess.AppendMessages(result.Messages[len(prepared):]); err != nil {
		app.Logger.Warn("session persist failed", "error", err)
	}

	return result.Messages, nil
}

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

	provider, _, err := buildProvider(cfg)
	if err != nil {
		return fmt.Errorf("build provider: %w", err)
	}

	cwd, _ := os.Getwd()
	reg := buildToolRegistry(cwd)
	registerWikiTools(reg, cfg.WikiDir, db.Wiki)

	mode := parsePermissionMode(cfg.Permission.Mode)
	perm := agent.NewPermission(
		agent.WithMode(mode),
		agent.WithAllowList(cfg.Permission.Allow...),
		agent.WithDenyList(cfg.Permission.Deny...),
	)

	selfState, err := self.Load(cfg.DataDir)
	if err != nil {
		app.Logger.Warn("failed to load self state for daemon, using defaults", "error", err)
		selfState = self.New(cfg.DataDir)
	}
	daemonPrompt := self.BuildSystemPrompt(selfState, "")

	factory := func(factoryCtx context.Context) (*agent.Agent, error) {
		return agent.New(provider, reg,
			agent.WithPermission(perm),
			agent.WithSystemPrompt(daemonPrompt),
			agent.WithLogger(app.Logger),
		), nil
	}

	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		return fmt.Errorf("create queue: %w", err)
	}

	d := daemon.New(queue, cfg.Daemon.SocketPath, cfg.Daemon.MaxWorkers, factory, app.Logger)
	return d.Start(ctx)
}

func cmdDaemonSubmit(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath daemon submit <task description>")
	}
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	payload := strings.Join(args, " ")
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
	var result map[string]interface{}
	if json.Unmarshal(data, &result) == nil {
		if id, ok := result["task_id"]; ok {
			fmt.Printf("Task submitted: ID %v\n", id)
			return nil
		}
	}
	fmt.Printf("Task submitted: %s\n", string(data))
	return nil
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
			ID      float64 `json:"id"`
			Status  string  `json:"status"`
			Payload string  `json:"payload"`
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

	fmt.Printf("%-6s  %-12s  %s\n", "ID", "STATUS", "PAYLOAD")
	fmt.Printf("%-6s  %-12s  %s\n", "------", "------------", "------------------------------------------------------------")
	for _, t := range result.Tasks {
		payload := t.Payload
		if len(payload) > 60 {
			payload = payload[:57] + "..."
		}
		fmt.Printf("%-6.0f  %-12s  %s\n", t.ID, t.Status, payload)
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

func cmdSearch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath search <query>")
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

	if err := conversation.InitSchema(db.Main); err != nil {
		return fmt.Errorf("init conversation schema: %w", err)
	}

	store := conversation.NewHistoryStore(db.Main)
	query := strings.Join(args, " ")
	results, err := store.Search(ctx, query, 20)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for i, r := range results {
		fmt.Printf("%d. [%s] session:%s (%s)\n   %s\n",
			i+1, r.Role, r.SessionID,
			r.CreatedAt.Format("2006-01-02 15:04"),
			r.Snippet)
	}
	return nil
}

func cmdWiki(ctx context.Context, args []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	store, err := wiki.NewStore(cfg.WikiDir)
	if err != nil {
		return fmt.Errorf("wiki store: %w", err)
	}

	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	idx, err := wiki.NewIndex(db.Wiki)
	if err != nil {
		return fmt.Errorf("wiki index: %w", err)
	}

	if len(args) == 0 {
		fmt.Println(`Usage: elnath wiki <subcommand> [args]

Subcommands:
  search <query>   Search wiki pages
  lint             Check wiki health
  rebuild          Rebuild FTS index
  list             List all pages`)
		return nil
	}

	switch args[0] {
	case "search":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath wiki search <query>")
		}
		query := strings.Join(args[1:], " ")
		results, err := idx.Search(ctx, wiki.SearchOpts{Query: query, Limit: 10})
		if err != nil {
			return fmt.Errorf("wiki search: %w", err)
		}
		if len(results) == 0 {
			fmt.Println("No results found.")
			return nil
		}
		for i, r := range results {
			fmt.Printf("%d. [%.2f] %s — %s\n", i+1, r.Score, r.Page.Path, r.Page.Title)
			for _, h := range r.Highlights {
				fmt.Printf("   %s\n", h)
			}
		}

	case "lint":
		linter := wiki.NewLinter(store, idx)
		issues, err := linter.Lint(ctx)
		if err != nil {
			return fmt.Errorf("wiki lint: %w", err)
		}
		if len(issues) == 0 {
			fmt.Println("Wiki is healthy — no issues found.")
			return nil
		}
		for _, issue := range issues {
			fmt.Printf("[%s] %s: %s\n", issue.Severity, issue.Path, issue.Message)
		}

	case "rebuild":
		if err := idx.Rebuild(store); err != nil {
			return fmt.Errorf("wiki rebuild: %w", err)
		}
		fmt.Println("Wiki FTS index rebuilt.")

	case "list":
		pages, err := store.List()
		if err != nil {
			return fmt.Errorf("wiki list: %w", err)
		}
		if len(pages) == 0 {
			fmt.Println("No wiki pages found.")
			return nil
		}
		for _, p := range pages {
			fmt.Printf("  %s — %s [%s]\n", p.Path, p.Title, p.Type)
		}

	default:
		return fmt.Errorf("unknown wiki subcommand: %s", args[0])
	}

	return nil
}

// ---- helpers ----

func buildProvider(cfg *config.Config) (llm.Provider, string, error) {
	reg := llm.NewRegistry()
	var model string

	if cfg.Anthropic.APIKey != "" {
		var opts []llm.AnthropicOption
		if cfg.Anthropic.BaseURL != "" {
			opts = append(opts, llm.WithAnthropicBaseURL(cfg.Anthropic.BaseURL))
		}
		if cfg.Anthropic.Timeout > 0 {
			opts = append(opts, llm.WithAnthropicTimeout(time.Duration(cfg.Anthropic.Timeout)*time.Second))
		}
		m := cfg.Anthropic.Model
		if m == "" {
			m = "claude-sonnet-4-6"
		}
		reg.Register("anthropic", llm.NewAnthropicProvider(cfg.Anthropic.APIKey, m, opts...))
		if model == "" {
			model = m
		}
	}

	codexToken, codexModel, codexAccountID := loadCodexAuth()
	if codexToken != "" {
		reg.Register("openai-responses", llm.NewResponsesProvider(codexToken, codexModel, codexAccountID))
		if model == "" {
			model = codexModel
		}
	}

	if cfg.OpenAI.APIKey != "" {
		var opts []llm.OpenAIOption
		if cfg.OpenAI.BaseURL != "" {
			opts = append(opts, llm.WithOpenAIBaseURL(cfg.OpenAI.BaseURL))
		}
		m := cfg.OpenAI.Model
		if m == "" {
			m = "gpt-4o"
		}
		reg.Register("openai", llm.NewOpenAIProvider(cfg.OpenAI.APIKey, m, opts...))
		if model == "" {
			model = m
		}
	}

	if cfg.Ollama.Model != "" || cfg.Ollama.BaseURL != "" {
		var opts []llm.OllamaOption
		if cfg.Ollama.BaseURL != "" {
			opts = append(opts, llm.WithOllamaBaseURL(cfg.Ollama.BaseURL))
		}
		m := cfg.Ollama.Model
		if m == "" {
			m = "llama3.2"
		}
		reg.Register("ollama", llm.NewOllamaProvider(cfg.Ollama.APIKey, m, opts...))
		if model == "" {
			model = m
		}
	}

	if len(reg.List()) == 0 {
		return nil, "", fmt.Errorf("no LLM provider configured: set ELNATH_ANTHROPIC_API_KEY or ELNATH_OPENAI_API_KEY")
	}

	canonical := llm.ResolveModel(model)
	detectedProvider := llm.DetectProvider(canonical)

	// Codex Responses provider preferred over plain OpenAI for the same model names.
	if detectedProvider == "openai" && codexToken != "" {
		p, err := reg.Get("openai-responses")
		if err == nil {
			return p, canonical, nil
		}
	}

	p, resolvedModel, err := reg.ForModel(model)
	if err != nil {
		return nil, "", err
	}
	return p, resolvedModel, nil
}

func loadCodexAuth() (token, model, accountID string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		return "", "", ""
	}
	var auth struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil || auth.AuthMode != "chatgpt" || auth.Tokens.AccessToken == "" {
		return "", "", ""
	}
	accountID = auth.Tokens.AccountID
	cfgData, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err == nil {
		for _, line := range strings.Split(string(cfgData), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "model") && !strings.HasPrefix(line, "model_") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					model = strings.Trim(strings.TrimSpace(parts[1]), "\"")
				}
			}
		}
	}
	if model == "" {
		model = "gpt-4o"
	}
	return auth.Tokens.AccessToken, model, accountID
}

func buildRouter(cfg orchestrator.WorkflowConfig) *orchestrator.Router {
	workflows := map[string]orchestrator.Workflow{
		"single":    orchestrator.NewSingleWorkflow(),
		"team":      orchestrator.NewTeamWorkflow(),
		"autopilot": orchestrator.NewAutopilotWorkflow(),
		"ralph":     orchestrator.NewRalphWorkflow(),
		"research":  orchestrator.NewResearchWorkflow(),
	}
	return orchestrator.NewRouter(workflows)
}

func registerWikiTools(reg *tools.Registry, wikiDir string, wikiDB *sql.DB) (*wiki.GitSync, *wiki.Index) {
	if wikiDir == "" || wikiDB == nil {
		return nil, nil
	}
	store, err := wiki.NewStore(wikiDir)
	if err != nil {
		return nil, nil
	}
	idx, err := wiki.NewIndex(wikiDB)
	if err != nil {
		return nil, nil
	}
	reg.Register(wiki.NewWikiSearchTool(idx))
	reg.Register(wiki.NewWikiReadTool(store))
	reg.Register(wiki.NewWikiWriteTool(store))

	gs := wiki.NewGitSync(wikiDir, nil)
	if err := gs.Init(); err != nil {
		return nil, idx
	}
	return gs, idx
}

func registerCrossProjectTools(reg *tools.Registry, projects []config.ProjectRef, app *core.App) {
	xps := wiki.NewCrossProjectSearcher()
	xcs := conversation.NewCrossProjectConversationSearcher()
	for _, p := range projects {
		pDB, err := core.OpenDB(p.DataDir)
		if err != nil {
			app.Logger.Warn("cross-project: skip project, cannot open db",
				"project", p.Name, "data_dir", p.DataDir, "error", err)
			continue
		}
		app.RegisterCloser("cross-project-db:"+p.Name, pDB)
		pIdx, err := wiki.NewIndex(pDB.Wiki)
		if err != nil {
			app.Logger.Warn("cross-project: skip project, cannot open wiki index",
				"project", p.Name, "error", err)
		} else {
			xps.AddProject(p.Name, pIdx)
		}
		if err := conversation.InitSchema(pDB.Main); err == nil {
			xcs.AddProject(p.Name, conversation.NewHistoryStore(pDB.Main))
		}
	}
	if xps.Len() > 0 {
		reg.Register(wiki.NewCrossProjectSearchTool(xps))
	}
	if xcs.Len() > 0 {
		reg.Register(conversation.NewCrossProjectConversationSearchTool(xcs))
	}
}

// registerMCPTools starts each configured MCP server, lists its tools, and
// registers them in the tool registry. Failures are non-fatal: a server that
// fails to start or initialize is logged and skipped.
func registerMCPTools(ctx context.Context, reg *tools.Registry, servers []config.MCPServerConfig, app *core.App) {
	for _, sc := range servers {
		client, err := mcp.NewClient(ctx, sc.Command, sc.Args, sc.Env, app.Logger)
		if err != nil {
			app.Logger.Warn("mcp: failed to start server", slog.String("name", sc.Name), slog.String("error", err.Error()))
			continue
		}
		app.RegisterCloser("mcp:"+sc.Name, client)

		if err := client.Initialize(ctx); err != nil {
			app.Logger.Warn("mcp: failed to initialize server", slog.String("name", sc.Name), slog.String("error", err.Error()))
			continue
		}

		toolInfos, err := client.ListTools(ctx)
		if err != nil {
			app.Logger.Warn("mcp: failed to list tools", slog.String("name", sc.Name), slog.String("error", err.Error()))
			continue
		}

		for _, info := range toolInfos {
			reg.Register(mcp.NewTool(client, info))
			app.Logger.Info("mcp: registered tool", slog.String("server", sc.Name), slog.String("tool", info.Name))
		}
	}
}

func buildToolRegistry(workDir string) *tools.Registry {
	reg := tools.NewRegistry()
	reg.Register(tools.NewBashTool(workDir))
	reg.Register(tools.NewReadTool(workDir))
	reg.Register(tools.NewWriteTool(workDir))
	reg.Register(tools.NewEditTool(workDir))
	reg.Register(tools.NewGlobTool(workDir))
	reg.Register(tools.NewGrepTool(workDir))
	reg.Register(tools.NewGitTool(workDir))
	reg.Register(tools.NewWebFetchTool())
	return reg
}

func parsePermissionMode(mode string) agent.PermissionMode {
	switch mode {
	case "accept_edits":
		return agent.ModeAcceptEdits
	case "plan":
		return agent.ModePlan
	case "bypass":
		return agent.ModeBypass
	default:
		return agent.ModeDefault
	}
}

func defaultSystemPrompt() string {
	// Fallback when self model is not loaded.
	return "You are Elnath, an autonomous AI assistant.\n" +
		"You have access to tools for reading and writing files, executing shell commands,\n" +
		"searching the web, and interacting with git repositories.\n" +
		"Be concise, accurate, and helpful."
}

// onboardingResultToConfig converts an onboarding wizard Result to a config OnboardingResult.
func onboardingResultToConfig(result *onboarding.Result) *config.OnboardingResult {
	var mcpServers []config.MCPServerConfig
	for _, s := range result.MCPServers {
		mcpServers = append(mcpServers, config.MCPServerConfig{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
		})
	}
	return &config.OnboardingResult{
		APIKey:         result.APIKey,
		Locale:         string(result.Locale),
		DataDir:        result.DataDir,
		WikiDir:        result.WikiDir,
		PermissionMode: result.PermissionMode,
		MCPServers:     mcpServers,
	}
}

// cliPrompter asks the user for interactive permission approval.
type cliPrompter struct{}

func (p *cliPrompter) Prompt(_ context.Context, toolName string, _ json.RawMessage) (bool, error) {
	fmt.Printf("\nAllow tool %q? [y/N] ", toolName)
	var resp string
	fmt.Scanln(&resp)
	return strings.ToLower(strings.TrimSpace(resp)) == "y", nil
}
