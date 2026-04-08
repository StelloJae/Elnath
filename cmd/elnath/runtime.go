package main

import (
	"context"
	"fmt"
	"os"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

type orchestrationOutput struct {
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

type executionRuntime struct {
	app       *core.App
	provider  llm.Provider
	mgr       *conversation.Manager
	router    *orchestrator.Router
	reg       *tools.Registry
	wfCfg     orchestrator.WorkflowConfig
	wikiIdx   *wiki.Index
	wikiStore *wiki.Store
	gitSync   *wiki.GitSync
}

func buildExecutionRuntime(
	ctx context.Context,
	cfg *config.Config,
	app *core.App,
	db *core.DB,
	provider llm.Provider,
	model string,
	systemPrompt string,
	perm *agent.Permission,
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

	cwd, _ := os.Getwd()
	reg := buildToolRegistry(cwd)
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

	hooks := buildHookRegistry(cfg.Hooks)
	wfCfg := orchestrator.WorkflowConfig{
		Model:        model,
		SystemPrompt: systemPrompt,
		Hooks:        hooks,
		Permission:   perm,
	}

	return &executionRuntime{
		app:       app,
		provider:  provider,
		mgr:       mgr,
		router:    buildRouter(wfCfg),
		reg:       reg,
		wfCfg:     wfCfg,
		wikiIdx:   wikiIdx,
		wikiStore: wikiStore,
		gitSync:   gitSync,
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
	}

	routeCtx := &orchestrator.RoutingContext{EstimatedFiles: estimateFiles(userInput)}
	wf := rt.router.Route(intent, routeCtx)
	if wf == nil {
		return nil, "", fmt.Errorf("no workflow available for intent %q", intent)
	}

	rt.app.Logger.Info("routed",
		"intent", string(intent),
		"workflow", wf.Name(),
		"session", sess.ID,
	)
	if output.OnWorkflow != nil {
		output.OnWorkflow(intent, wf.Name())
	}

	cfg := rt.wfCfg
	if rt.wikiIdx != nil {
		if ragCtx := wiki.BuildRAGContext(ctx, rt.wikiIdx, userInput, 3); ragCtx != "" {
			cfg.SystemPrompt += "\n\n" + ragCtx
		}
	}

	input := orchestrator.WorkflowInput{
		Message:  userInput,
		Messages: prepared,
		Session:  sess,
		Tools:    rt.reg,
		Provider: rt.provider,
		Config:   cfg,
		OnText:   output.OnText,
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

	if usage := llm.FormatUsageSummary(rt.wfCfg.Model, result.Usage); usage != "" && output.OnUsage != nil {
		output.OnUsage(usage)
	}

	if err := sess.AppendMessages(result.Messages[len(prepared):]); err != nil {
		rt.app.Logger.Warn("session persist failed", "error", err)
	}

	return result.Messages, result.Summary, nil
}

func (rt *executionRuntime) newDaemonTaskRunner() daemon.TaskRunner {
	return func(ctx context.Context, payload string, onText func(string)) (string, error) {
		sess, err := rt.mgr.NewSession()
		if err != nil {
			return "", fmt.Errorf("create session: %w", err)
		}

		messages, summary, err := rt.runTask(ctx, sess, nil, payload, orchestrationOutput{
			OnText: onText,
		})
		if err != nil {
			return "", err
		}

		rt.maybeCommitWiki("auto: wiki update")
		rt.maybeAutoDocumentSession(ctx, sess.ID, messages)
		return summary, nil
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
