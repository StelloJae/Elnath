package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/onboarding"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/wiki"
)

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
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get cwd: %w", err)
	}
	principal := identity.ResolveCLIPrincipal(cfg, extractFlagValue(os.Args, "--principal"), cwd)
	principal.ProjectID = identity.ResolveProjectID(cwd, extractFlagValue(os.Args, "--project-id"))

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
	// Build permission engine.
	mode := parsePermissionMode(cfg.Permission.Mode)
	perm := agent.NewPermission(
		agent.WithMode(mode),
		agent.WithAllowList(cfg.Permission.Allow...),
		agent.WithDenyList(cfg.Permission.Deny...),
		agent.WithPrompter(&cliPrompter{}),
	)
	rt, err := buildExecutionRuntime(
		ctx,
		cfg,
		app,
		db,
		provider,
		model,
		selfState,
		personaExtra,
		perm,
		"",
		nil,
		principal,
	)
	if err != nil {
		return err
	}

	// Resolve --continue-task <id> to a session ID.
	if taskIDStr := extractFlagValue(os.Args, "--continue-task"); taskIDStr != "" {
		taskID, parseErr := strconv.ParseInt(taskIDStr, 10, 64)
		if parseErr != nil {
			return fmt.Errorf("invalid task ID %q: %w", taskIDStr, parseErr)
		}
		sid, lookupErr := resolveTaskSession(db.Main, taskID)
		if lookupErr != nil {
			return lookupErr
		}
		os.Args = append(os.Args, "--session", sid)
	}

	// Create or resume session.
	var sess *agent.Session
	var messages []llm.Message
	if sid := extractSessionFlag(os.Args); sid != "" {
		sess, err = rt.mgr.LoadSession(sid)
		if err != nil {
			return fmt.Errorf("resume session %s: %w", sid, err)
		}
		messages = sess.Messages
		app.Logger.Info("resumed session", "id", sess.ID, "messages", len(messages))
	} else if hasFlag(os.Args, "--continue") {
		sess, err = rt.mgr.LoadLatestSession()
		if err != nil {
			return fmt.Errorf("resume latest session: %w", err)
		}
		messages = sess.Messages
		app.Logger.Info("resumed latest session", "id", sess.ID, "messages", len(messages))
	} else {
		sess, err = rt.mgr.NewSessionWithPrincipal(principal)
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		app.Logger.Info("session started", "id", sess.ID)
	}

	// Parse optional initial prompt from args.
	if promptArgs := runPromptArgs(args); len(promptArgs) > 0 {
		prompt := strings.Join(promptArgs, " ")
		messages, _, err = rt.runTask(ctx, sess, messages, prompt, cliOrchestrationOutput())
		if err != nil {
			return err
		}
		rt.maybeCommitWiki("auto: wiki update")
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

		messages, _, err = rt.runTask(ctx, sess, messages, line, cliOrchestrationOutput())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		rt.maybeCommitWiki("auto: wiki update")
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stdin: %w", err)
	}

	rt.maybeAutoDocumentSession(ctx, wiki.IngestEvent{
		SessionID: sess.ID,
		Messages:  messages,
		Reason:    "interactive_session",
		Principal: sess.Principal.SurfaceIdentity(),
	})

	return nil
}

func runPromptArgs(args []string) []string {
	var promptArgs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "--persona", "--session", "--continue-task", "--principal", "--project-id":
			if i+1 < len(args) {
				i++
			}
		case "--continue", "--non-interactive":
		default:
			promptArgs = append(promptArgs, args[i])
		}
	}
	return promptArgs
}
