package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/audit"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/prompt"
	"github.com/stello/elnath/internal/secret"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/skill"
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
	app                *core.App
	provider           llm.Provider
	mgr                *conversation.Manager
	router             *orchestrator.Router
	reg                *tools.Registry
	wfCfg              orchestrator.WorkflowConfig
	promptBuilder      *prompt.Builder
	learningStore      *learning.Store
	usageTracker       *llm.UsageTracker
	researchMaxRounds  int
	researchCostCapUSD float64
	selfState          *self.SelfState
	personaExtra       string
	wikiIdx            *wiki.Index
	wikiStore          *wiki.Store
	skillReg           *skill.Registry
	gitSync            *wiki.GitSync
	workDir            string
	daemonMode         bool
	principal          identity.Principal
	auditTrail         *audit.Trail
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
	daemonMode bool,
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
	skillReg := skill.NewRegistry()
	if wikiStore != nil {
		if err := skillReg.Load(wikiStore); err != nil {
			app.Logger.Warn("skill registry load failed", "error", err)
		}
	}
	if selfState == nil {
		selfState = self.New(cfg.DataDir)
	}
	usageTracker, err := llm.NewUsageTracker(db.Main)
	if err != nil {
		return nil, fmt.Errorf("create usage tracker: %w", err)
	}

	hooks := buildHookRegistry(cfg.Hooks)
	if hooks == nil {
		hooks = agent.NewHookRegistry()
	}

	auditPath := filepath.Join(cfg.DataDir, "audit.jsonl")
	auditTrail, err := audit.NewTrail(auditPath)
	if err != nil {
		app.Logger.Warn("audit trail unavailable", "error", err)
	} else {
		app.RegisterCloser("audit trail", auditTrail)
	}
	hooks.Add(secret.NewSecretScanHook(secret.NewDetector(), auditTrail))

	wfCfg := orchestrator.WorkflowConfig{
		Model:         model,
		MaxIterations: maxIterationsFromEnv(),
		SystemPrompt:  "",
		Hooks:         hooks,
		Permission:    perm,
	}
	learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
	learningDetector := secret.NewDetector()
	learningStore := learning.NewStore(
		learningPath,
		learning.WithRedactor(learningDetector.RedactString),
	)
	b := prompt.NewBuilder()
	b.Register(prompt.NewIdentityNode(100))
	b.Register(prompt.NewContextFilesNode(95))
	b.Register(prompt.NewPersonaNode(90))
	b.Register(prompt.NewLessonsNode(87, learningStore, 10, 1000))
	b.Register(prompt.NewSelfStateNode(85))
	b.Register(prompt.NewToolCatalogNode(80))
	b.Register(prompt.NewModelGuidanceNode(70))
	b.Register(prompt.NewSkillCatalogNode(65, skillReg))
	b.Register(prompt.NewDynamicBoundaryNode())
	b.Register(prompt.NewWikiRAGNode(60, 3))
	b.Register(prompt.NewMemoryContextNode(55, 5, 1200))
	b.Register(prompt.NewProjectContextNode(50))
	b.Register(prompt.NewBrownfieldNode(40))
	b.Register(prompt.NewSessionSummaryNode(30, 5, 800))

	return &executionRuntime{
		app:                app,
		provider:           provider,
		mgr:                mgr,
		router:             buildRouter(wfCfg),
		reg:                reg,
		wfCfg:              wfCfg,
		promptBuilder:      b,
		learningStore:      learningStore,
		usageTracker:       usageTracker,
		researchMaxRounds:  cfg.Research.MaxRounds,
		researchCostCapUSD: cfg.Research.CostCapUSD,
		selfState:          selfState,
		personaExtra:       personaExtra,
		wikiIdx:            wikiIdx,
		wikiStore:          wikiStore,
		skillReg:           skillReg,
		gitSync:            gitSync,
		workDir:            effectiveWorkDir,
		daemonMode:         daemonMode,
		principal:          defaultPrincipal,
		auditTrail:         auditTrail,
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

func (rt *executionRuntime) learningDeps() *orchestrator.LearningDeps {
	if rt.learningStore == nil || benchmarkModeEnabled() {
		return nil
	}
	return &orchestrator.LearningDeps{
		Store:     rt.learningStore,
		SelfState: rt.selfState,
		Logger:    rt.app.Logger,
	}
}

func (rt *executionRuntime) runTask(
	ctx context.Context,
	sess *agent.Session,
	messages []llm.Message,
	userInput string,
	output orchestrationOutput,
) ([]llm.Message, string, error) {
	userInput = normalizeSkillInput(userInput)
	if rt.skillReg != nil && strings.HasPrefix(userInput, "/") {
		result, summary, handled, err := rt.trySkillExecution(ctx, sess, messages, userInput, output)
		if handled {
			return result, summary, err
		}
	}

	prepared, intent, err := rt.mgr.SendMessage(ctx, sess.ID, userInput)
	if err != nil {
		rt.app.Logger.Warn("conversation manager fallback", "error", err)
		prepared = append(messages, llm.NewUserMessage(userInput))
		intent = conversation.IntentUnclear
	} else {
		sess.Messages = append(sess.Messages, llm.NewUserMessage(userInput))
	}

	routeCtx := buildRoutingContext(userInput)
	routeCtx.BenchmarkMode = benchmarkModeEnabled()
	routeCtx.ProjectID = rt.principal.ProjectID
	pref, err := wiki.LoadWorkflowPreference(rt.wikiStore, routeCtx.ProjectID)
	if err != nil {
		rt.app.Logger.Warn("routing preference unavailable, using base routing",
			"project_id", routeCtx.ProjectID,
			"error", err,
		)
		pref = nil
	}
	wf := rt.router.Route(intent, routeCtx, pref)
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
		SessionID:     sess.ID,
		UserInput:     userInput,
		Self:          rt.selfState,
		Messages:      promptMessages,
		WikiIdx:       rt.wikiIdx,
		TokenBudget:   0,
		PersonaExtra:  rt.personaExtra,
		Model:         rt.wfCfg.Model,
		Provider:      rt.provider.Name(),
		ToolNames:     rt.reg.Names(),
		WorkDir:       rt.workDir,
		ExistingCode:  routeCtx.ExistingCode,
		VerifyHint:    routeCtx.VerificationHint,
		BenchmarkMode: routeCtx.BenchmarkMode,
		TaskLanguage:  taskLanguageFromEnv(),
		DaemonMode:    rt.daemonMode,
		MessageCount:  len(prepared),
	}
	systemPrompt, err := rt.promptBuilder.Build(ctx, renderState)
	if err != nil {
		return nil, "", fmt.Errorf("prompt build: %w", err)
	}
	cfg := rt.wfCfg
	cfg.SystemPrompt = systemPrompt

	input := orchestrator.WorkflowInput{
		Message:  userInput,
		Messages: promptMessages,
		Session:  sess,
		Tools:    rt.reg,
		Provider: rt.provider,
		Config:   cfg,
		OnText:   output.emitText,
	}
	switch wf.Name() {
	case "single", "team", "ralph", "autopilot":
		input.Learning = rt.learningDeps()
	}
	if wf.Name() == "research" && rt.wikiIdx != nil && rt.wikiStore != nil {
		input.Extra = &orchestrator.ResearchDeps{
			WikiIndex:     rt.wikiIdx,
			WikiStore:     rt.wikiStore,
			UsageTracker:  rt.usageTracker,
			LearningStore: rt.learningStore,
			SelfState:     rt.selfState,
			MaxRounds:     rt.researchMaxRounds,
			CostCapUSD:    rt.researchCostCapUSD,
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

func (rt *executionRuntime) trySkillExecution(
	ctx context.Context,
	sess *agent.Session,
	messages []llm.Message,
	input string,
	output orchestrationOutput,
) ([]llm.Message, string, bool, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return nil, "", false, nil
	}
	skillName := strings.TrimPrefix(fields[0], "/")
	sk, ok := rt.skillReg.Get(skillName)
	if !ok {
		return nil, "", false, nil
	}

	args := parseSkillArgs(sk.Trigger, fields[1:])

	rt.app.Logger.Info("executing skill", "name", skillName, "args", args)
	output.emitText(fmt.Sprintf("Executing skill: %s\n", skillName))

	result, err := rt.skillReg.Execute(ctx, skill.ExecuteParams{
		SkillName:  skillName,
		Args:       args,
		Provider:   rt.provider,
		ToolReg:    rt.reg,
		Model:      rt.wfCfg.Model,
		OnText:     output.emitText,
		Permission: rt.wfCfg.Permission,
		Hooks:      rt.wfCfg.Hooks,
	})
	if err != nil {
		return nil, "", true, fmt.Errorf("skill %q: %w", skillName, err)
	}
	if usage := llm.FormatUsageSummary(rt.wfCfg.Model, result.Usage); usage != "" {
		output.emitUsage(usage)
	}

	delta := make([]llm.Message, 0, len(result.Messages)+1)
	delta = append(delta, llm.NewUserMessage(input))
	if len(result.Messages) > 0 {
		transcript := result.Messages
		if transcript[0].Role == llm.RoleUser && transcript[0].Text() == "Execute this skill." {
			transcript = transcript[1:]
		}
		delta = append(delta, transcript...)
	}
	if len(delta) == 1 {
		delta = append(delta, llm.NewAssistantMessage(result.Output))
	}
	if err := sess.AppendMessages(delta); err != nil {
		rt.app.Logger.Warn("session persist failed", "error", err)
	}
	updated := append(messages, delta...)
	sess.Messages = updated
	return updated, result.Output, true, nil
}

func parseSkillArgs(trigger string, values []string) map[string]string {
	args := make(map[string]string)
	placeholders := make([]string, 0)
	for _, part := range strings.Fields(trigger) {
		if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
			placeholders = append(placeholders, strings.TrimPrefix(strings.TrimSuffix(part, ">"), "<"))
		}
	}

	idx := 0
	for i, name := range placeholders {
		if idx >= len(values) {
			args[name] = ""
			continue
		}
		if i == len(placeholders)-1 {
			args[name] = strings.Join(values[idx:], " ")
			idx = len(values)
			continue
		}
		args[name] = values[idx]
		idx++
	}
	return args
}

func normalizeSkillInput(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "[Skill:") {
		return trimmed
	}
	end := strings.Index(trimmed, "]")
	if end == -1 {
		return trimmed
	}
	remainder := strings.TrimSpace(trimmed[end+1:])
	if strings.HasPrefix(remainder, "/") {
		return remainder
	}
	return trimmed
}

func benchmarkModeEnabled() bool {
	return os.Getenv("ELNATH_BENCHMARK_MODE") == "1"
}

func taskLanguageFromEnv() string {
	return os.Getenv("ELNATH_TASK_LANGUAGE")
}

func maxIterationsFromEnv() int {
	raw := os.Getenv("ELNATH_MAX_ITERATIONS")
	if raw == "" {
		return 50
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 50
	}
	return n
}

func (rt *executionRuntime) newDaemonTaskRunner() daemon.AgentTaskRunner {
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
		principal := taskPayload.Principal
		if principal.IsZero() {
			principal = rt.principal
		}
		if taskPayload.SessionID != "" {
			sess, err = rt.mgr.LoadSessionForPrincipal(taskPayload.SessionID, principal)
			if err != nil {
				return daemon.TaskResult{}, fmt.Errorf("load session %s: %w", taskPayload.SessionID, err)
			}
			if principal.IsZero() {
				principal = sess.Principal
			}
			if err := sess.RecordResume(principal); err != nil {
				return daemon.TaskResult{}, fmt.Errorf("record resume %s: %w", taskPayload.SessionID, err)
			}
			messages = sess.Messages
		} else {
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

func (rt *executionRuntime) maybeAutoDocumentSession(ctx context.Context, event wiki.IngestEvent) {
	if rt.wikiStore == nil {
		return
	}

	ing := wiki.NewIngester(rt.wikiStore, rt.provider)
	if err := ing.IngestSession(ctx, event); err != nil {
		rt.app.Logger.Warn("auto-documentation failed", "error", err)
		return
	}

	rt.app.Logger.Info("session auto-documented to wiki", "session", event.SessionID)
	rt.maybeCommitWiki("auto: document session " + event.SessionID)
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
