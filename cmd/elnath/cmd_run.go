package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
	"github.com/stello/elnath/internal/userfacingerr"
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
		return userfacingerr.Wrap(userfacingerr.ELN060, err, "load config")
	}
	applyGlobalFlagOverrides(cfg, os.Args)
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
		false,
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
		resolved, resolveErr := agent.ResolveSessionID(cfg.DataDir, sid)
		if resolveErr != nil {
			return fmt.Errorf("resolve --session: %w", resolveErr)
		}
		sid = resolved
		sess, err = rt.mgr.LoadSessionForPrincipal(sid, principal)
		if err != nil {
			return fmt.Errorf("resume session %s: %w", sid, err)
		}
		if err := sess.RecordResume(principal); err != nil {
			return fmt.Errorf("record session resume %s: %w", sid, err)
		}
		messages = sess.Messages
		app.Logger.Info("resumed session", "id", sess.ID, "messages", len(messages))
	} else if hasFlag(os.Args, "--continue") {
		sess, err = rt.mgr.LoadLatestSession(principal)
		if err != nil {
			return fmt.Errorf("resume latest session: %w", err)
		}
		if err := sess.RecordResume(principal); err != nil {
			return fmt.Errorf("record latest session resume %s: %w", sess.ID, err)
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

	// Non-interactive mode with piped stdin: read the full stdin as a
	// single multi-line prompt and run one task, then exit. Without this,
	// the REPL scanner below splits on newlines and terminates at the
	// first blank line — so any multi-line prompt (code blocks, etc.) is
	// silently truncated to its first paragraph.
	stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	if nonInteractive {
		if !stdinIsTTY {
			raw, readErr := io.ReadAll(os.Stdin)
			if readErr != nil {
				return fmt.Errorf("read non-interactive stdin: %w", readErr)
			}
			prompt := strings.TrimSpace(string(raw))
			if prompt != "" {
				messages, _, err = rt.runTask(ctx, sess, messages, prompt, cliOrchestrationOutput())
				if err != nil {
					return err
				}
				rt.maybeCommitWiki("auto: wiki update")
			}
		}
	} else {
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
	}

	// StartedAt/Duration omitted: interactive sessions have no bounded execution window.
	event, err := interactiveSessionIngestEvent(rt.app.Config.DataDir, sess, messages)
	if err != nil {
		app.Logger.Warn("interactive session resume history unavailable", "session_id", sess.ID, "error", err)
	} else {
		rt.maybeAutoDocumentSession(ctx, event)
	}

	return nil
}

func interactiveSessionIngestEvent(dataDir string, sess *agent.Session, messages []llm.Message) (wiki.IngestEvent, error) {
	event := wiki.IngestEvent{
		SessionID: sess.ID,
		Messages:  messages,
		Reason:    "interactive_session",
		Principal: sess.Principal.SurfaceIdentity(),
	}
	resumes, err := agent.LoadSessionResumeEvents(dataDir, sess.ID)
	if err != nil {
		return wiki.IngestEvent{}, err
	}
	for _, resume := range resumes {
		event.Resumes = append(event.Resumes, wiki.ResumeRecord{
			Surface:   resume.Surface,
			Principal: resume.Principal.SurfaceIdentity(),
			At:        resume.At,
		})
	}
	return event, nil
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
