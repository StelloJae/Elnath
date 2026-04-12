package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/prompt"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

type orchestrationOutput struct {
	OnProgress func(daemon.ProgressEvent)
	OnWorkflow func(intent conversation.Intent, workflow string)
	OnText     func(string)
	OnUsage    func(string)
}

func cliOrchestrationOutput() orchestrationOutput {
	return orchestrationOutput{
		OnWorkflow: func(intent conversation.Intent, workflow string) {
			fmt.Printf("[%s → %s]\n\n", intent, workflow)
		},
		OnText: func(s string) { fmt.Print(s) },
		OnUsage: func(summary string) {
			fmt.Println()
			fmt.Println(summary)
		},
	}
}

func (o orchestrationOutput) emitWorkflow(intent conversation.Intent, workflow string) {
	if o.OnProgress != nil {
		o.OnProgress(daemon.WorkflowProgressEvent(string(intent), workflow))
	}
	if o.OnWorkflow != nil {
		o.OnWorkflow(intent, workflow)
	}
}

func (o orchestrationOutput) emitText(text string) {
	if text == "" {
		return
	}
	if o.OnProgress != nil {
		if ev, ok := daemon.ParseProgressEvent(text); ok {
			o.OnProgress(ev)
		} else if ev := daemon.TextProgressEvent(text); ev.Message != "" {
			o.OnProgress(ev)
		}
	}
	if o.OnText != nil {
		o.OnText(text)
	}
}

func (o orchestrationOutput) emitUsage(summary string) {
	if summary == "" {
		return
	}
	if o.OnProgress != nil {
		o.OnProgress(daemon.UsageProgressEvent(summary))
	}
	if o.OnUsage != nil {
		o.OnUsage(summary)
	}
}

type executionRuntime struct {
	app           *core.App
	provider      llm.Provider
	mgr           *conversation.Manager
	router        *orchestrator.Router
	reg           *tools.Registry
	wfCfg         orchestrator.WorkflowConfig
	promptBuilder *prompt.Builder
	selfState     *self.SelfState
	personaExtra  string
	wikiIdx       *wiki.Index
	wikiStore     *wiki.Store
	gitSync       *wiki.GitSync
	workDir       string
	principal     identity.Principal
}

type routeAuditRecord struct {
	Timestamp        string              `json:"timestamp"`
	SessionID        string              `json:"session_id"`
	Input            string              `json:"input"`
	EstimatedFiles   int                 `json:"estimated_files"`
	ExistingCode     bool                `json:"existing_code"`
	VerificationHint bool                `json:"verification_hint"`
	Intent           conversation.Intent `json:"intent"`
	Workflow         string              `json:"workflow"`
}

func buildExecutionRuntime(
	ctx context.Context,
	cfg *config.Config,
	app *core.App,
	db *core.DB,
	provider llm.Provider,
	model string,
	selfState *self.SelfState,
	personaExtra string,
	perm *agent.Permission,
	workDir string,
	protectedPaths []string,
	defaultPrincipal identity.Principal,
) (*executionRuntime, error) {
	if err := conversation.InitSchema(db.Main); err != nil {
		return nil, fmt.Errorf("init conversation schema: %w", err)
	}

	historyStore := conversation.NewHistoryStore(db.Main)
	classifier := conversation.NewLLMClassifier()

	var ctxWindow conversation.ContextWindowManager
	if cfg.CompressThreshold > 0 {
		ctxWindow = conversation.NewContextWindowWithThreshold(cfg.CompressThreshold)
	} else {
		ctxWindow = conversation.NewContextWindow()
	}

	mgr := conversation.NewManager(db.Main, cfg.DataDir).
		WithProvider(provider).
		WithClassifier(classifier).
		WithContextWindow(ctxWindow).
		WithMaxContextTokens(cfg.MaxContextTokens).
		WithHistoryStore(historyStore).
		WithLogger(app.Logger)

	effectiveWorkDir := workDir
	if effectiveWorkDir == "" {
		effectiveWorkDir, _ = os.Getwd()
	}
	guard := tools.NewPathGuard(effectiveWorkDir, protectedPaths)
	reg := buildToolRegistry(guard)
	gitSync, wikiIdx := registerWikiTools(reg, cfg.WikiDir, db.Wiki)
	reg.Register(conversation.NewConversationSearchTool(historyStore))

	if len(cfg.Projects) > 0 {
		registerCrossProjectTools(reg, cfg.Projects, app)
	}
	if len(cfg.MCPServers) > 0 {
		registerMCPTools(ctx, reg, cfg.MCPServers, app)
	}

	var wikiStore *wiki.Store
	if cfg.WikiDir != "" {
		if ws, err := wiki.NewStore(cfg.WikiDir); err == nil {
			wikiStore = ws
		}
	}
	if selfState == nil {
		selfState = self.New(cfg.DataDir)
	}

	hooks := buildHookRegistry(cfg.Hooks)
	wfCfg := orchestrator.WorkflowConfig{
		Model:        model,
		SystemPrompt: "",
		Hooks:        hooks,
		Permission:   perm,
	}
	b := prompt.NewBuilder()
	b.Register(prompt.NewIdentityNode(100))
	b.Register(prompt.NewPersonaNode(90))
	b.Register(prompt.NewToolCatalogNode(80))
	b.Register(prompt.NewModelGuidanceNode(70))
	b.Register(prompt.NewDynamicBoundaryNode())
	b.Register(prompt.NewWikiRAGNode(60, 3))
	b.Register(prompt.NewProjectContextNode(50))
	b.Register(prompt.NewBrownfieldNode(40))
	b.Register(prompt.NewSessionSummaryNode(30, 5, 800))

	return &executionRuntime{
		app:           app,
		provider:      provider,
		mgr:           mgr,
		router:        buildRouter(wfCfg),
		reg:           reg,
		wfCfg:         wfCfg,
		promptBuilder: b,
		selfState:     selfState,
		personaExtra:  personaExtra,
		wikiIdx:       wikiIdx,
		wikiStore:     wikiStore,
		gitSync:       gitSync,
		workDir:       effectiveWorkDir,
		principal:     defaultPrincipal,
	}, nil
}

func buildHookRegistry(cfgHooks []config.HookConfig) *agent.HookRegistry {
	if len(cfgHooks) == 0 {
		return nil
	}

	hooks := agent.NewHookRegistry()
	for _, hc := range cfgHooks {
		hooks.Add(&agent.CommandHook{
			Matcher: hc.Matcher,
			PreCmd:  hc.PreCommand,
			PostCmd: hc.PostCommand,
		})
	}
	return hooks
}

func (rt *executionRuntime) runTask(
	ctx context.Context,
	sess *agent.Session,
	messages []llm.Message,
	userInput string,
	output orchestrationOutput,
) ([]llm.Message, string, error) {
	prepared, intent, err := rt.mgr.SendMessage(ctx, sess.ID, userInput)
	if err != nil {
		rt.app.Logger.Warn("conversation manager fallback", "error", err)
		prepared = append(messages, llm.NewUserMessage(userInput))
		intent = conversation.IntentUnclear
	} else {
		sess.Messages = append(sess.Messages, llm.NewUserMessage(userInput))
	}

	routeCtx := buildRoutingContext(userInput)
	wf := rt.router.Route(intent, routeCtx)
	if wf == nil {
		return nil, "", fmt.Errorf("no workflow available for intent %q", intent)
	}

	rt.app.Logger.Info("routed",
		"intent", string(intent),
		"workflow", wf.Name(),
		"session", sess.ID,
	)
	output.emitWorkflow(intent, wf.Name())
	rt.appendRouteAudit(routeAuditRecord{
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		SessionID:        sess.ID,
		Input:            userInput,
		EstimatedFiles:   routeCtx.EstimatedFiles,
		ExistingCode:     routeCtx.ExistingCode,
		VerificationHint: routeCtx.VerificationHint,
		Intent:           intent,
		Workflow:         wf.Name(),
	})

	promptMessages := prepared
	if len(promptMessages) > 0 && promptMessages[len(promptMessages)-1].Role == llm.RoleUser {
		promptMessages = promptMessages[:len(promptMessages)-1]
	}
	renderState := &prompt.RenderState{
		SessionID:    sess.ID,
		UserInput:    userInput,
		Self:         rt.selfState,
		Messages:     promptMessages,
		WikiIdx:      rt.wikiIdx,
		TokenBudget:  0,
		PersonaExtra: rt.personaExtra,
		Model:        rt.wfCfg.Model,
		Provider:     rt.provider.Name(),
		ToolNames:    rt.reg.Names(),
		WorkDir:      rt.workDir,
		ExistingCode: routeCtx.ExistingCode,
		VerifyHint:   routeCtx.VerificationHint,
	}
	systemPrompt, err := rt.promptBuilder.Build(ctx, renderState)
	if err != nil {
		return nil, "", fmt.Errorf("prompt build: %w", err)
	}
	cfg := rt.wfCfg
	cfg.SystemPrompt = systemPrompt

	input := orchestrator.WorkflowInput{
		Message:  userInput,
		Messages: prepared,
		Session:  sess,
		Tools:    rt.reg,
		Provider: rt.provider,
		Config:   cfg,
		OnText:   output.emitText,
	}
	if wf.Name() == "research" && rt.wikiIdx != nil && rt.wikiStore != nil {
		input.Extra = &orchestrator.ResearchDeps{
			WikiIndex:  rt.wikiIdx,
			WikiStore:  rt.wikiStore,
			MaxRounds:  5,
			CostCapUSD: 1.0,
		}
	}

	result, err := wf.Run(ctx, input)
	if err != nil {
		return nil, "", fmt.Errorf("workflow %s: %w", wf.Name(), err)
	}

	if usage := llm.FormatUsageSummary(rt.wfCfg.Model, result.Usage); usage != "" {
		output.emitUsage(usage)
	}

	if err := sess.AppendMessages(result.Messages[len(prepared):]); err != nil {
		rt.app.Logger.Warn("session persist failed", "error", err)
	}

	return result.Messages, result.Summary, nil
}

func (rt *executionRuntime) newDaemonTaskRunner() daemon.TaskRunner {
	return func(ctx context.Context, payload string, onText func(string)) (daemon.TaskResult, error) {
		taskPayload := daemon.ParseTaskPayload(payload)
		userInput := taskPayload.Prompt
		if userInput == "" {
			return daemon.TaskResult{}, fmt.Errorf("daemon task payload is empty")
		}

		var (
			sess     *agent.Session
			messages []llm.Message
			err      error
		)
		if taskPayload.SessionID != "" {
			sess, err = rt.mgr.LoadSession(taskPayload.SessionID)
			if err != nil {
				return daemon.TaskResult{}, fmt.Errorf("load session %s: %w", taskPayload.SessionID, err)
			}
			messages = sess.Messages
		} else {
			principal := taskPayload.Principal
			if principal.IsZero() {
				principal = rt.principal
			}
			if principal.IsZero() {
				principal = identity.LegacyPrincipal()
			}
			sess, err = rt.mgr.NewSessionWithPrincipal(principal)
			if err != nil {
				return daemon.TaskResult{}, fmt.Errorf("create session: %w", err)
			}
		}

		messages, summary, err := rt.runTask(ctx, sess, messages, userInput, orchestrationOutput{
			OnProgress: func(ev daemon.ProgressEvent) {
				if onText == nil {
					return
				}
				if raw := daemon.EncodeProgressEvent(ev); raw != "" {
					onText(raw)
				}
			},
		})
		if err != nil {
			return daemon.TaskResult{}, err
		}

		rt.maybeCommitWiki("auto: wiki update")
		rt.maybeAutoDocumentSession(ctx, sess.ID, messages)
		return daemon.TaskResult{
			Result:    summary,
			Summary:   summary,
			SessionID: sess.ID,
		}, nil
	}
}

func (rt *executionRuntime) maybeCommitWiki(message string) {
	if rt.gitSync == nil {
		return
	}
	if err := rt.gitSync.Commit(message); err != nil {
		rt.app.Logger.Warn("wiki git commit failed", "error", err)
	}
}

func (rt *executionRuntime) maybeAutoDocumentSession(ctx context.Context, sessionID string, messages []llm.Message) {
	if rt.wikiStore == nil {
		return
	}

	ad := wiki.NewAutoDocumenter(rt.wikiStore, rt.provider, rt.app.Logger)
	if err := ad.IngestSession(ctx, sessionID, messages); err != nil {
		rt.app.Logger.Warn("auto-documentation failed", "error", err)
		return
	}

	rt.app.Logger.Info("session auto-documented to wiki", "session", sessionID)
	rt.maybeCommitWiki("auto: document session " + sessionID)
}

func (rt *executionRuntime) appendRouteAudit(record routeAuditRecord) {
	path := os.Getenv("ELNATH_EVAL_AUDIT_LOG")
	if path == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		rt.app.Logger.Warn("route audit mkdir failed", "path", path, "error", err)
		return
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		rt.app.Logger.Warn("route audit open failed", "path", path, "error", err)
		return
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		rt.app.Logger.Warn("route audit marshal failed", "error", err)
		return
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		rt.app.Logger.Warn("route audit write failed", "path", path, "error", err)
	}
}
