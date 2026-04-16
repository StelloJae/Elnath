package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/audit"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/event"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/magicdocs"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/prompt"
	"github.com/stello/elnath/internal/secret"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/profile"
	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

// orchestrationOutput carries optional callbacks for event routing.
// Production CLI and daemon modes route events through an event.Bus
// with typed observers. The OnWorkflow/OnText/OnUsage fields remain
// for backward compatibility with tests that set them directly.
type orchestrationOutput struct {
	OnProgress func(daemon.ProgressEvent)
	OnWorkflow func(intent conversation.Intent, workflow string)
	OnText     func(string)
	OnUsage    func(string)
}

// terminalObserver renders typed events to stdout for interactive CLI use.
type terminalObserver struct{}

func (terminalObserver) OnEvent(e event.Event) {
	switch ev := e.(type) {
	case event.TextDeltaEvent:
		fmt.Print(ev.Content)
	case event.WorkflowProgressEvent:
		fmt.Printf("[%s → %s]\n\n", ev.Intent, ev.Workflow)
	case event.UsageProgressEvent:
		fmt.Println()
		fmt.Println(ev.Summary)
	case event.ResearchProgressEvent:
		fmt.Print(ev.Message)
	}
}

// progressObserver converts typed events back to daemon.ProgressEvent
// for the daemon task runner's onText-based progress protocol.
type progressObserver struct {
	onProgress func(daemon.ProgressEvent)
}

func (p progressObserver) OnEvent(e event.Event) {
	switch ev := e.(type) {
	case event.ToolProgressEvent:
		p.onProgress(daemon.ToolProgressEvent(ev.ToolName, ev.Preview))
	case event.WorkflowProgressEvent:
		p.onProgress(daemon.WorkflowProgressEvent(ev.Intent, ev.Workflow))
	case event.UsageProgressEvent:
		p.onProgress(daemon.UsageProgressEvent(ev.Summary))
	case event.TextDeltaEvent:
		if ev.Content != "" {
			p.onProgress(daemon.TextProgressEvent(ev.Content))
		}
	case event.ResearchProgressEvent:
		if ev.Message != "" {
			p.onProgress(daemon.TextProgressEvent(ev.Message))
		}
	}
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

// newBus creates an event.Bus wired to the appropriate observers.
// CLI mode always gets a terminalObserver; daemon mode additionally
// gets a progressObserver when OnProgress is set. Legacy OnWorkflow/
// OnText/OnUsage callbacks are wired as observers for test compat.
func newBus(output orchestrationOutput, cli bool) *event.Bus {
	bus := event.NewBus()
	hasLegacy := output.OnWorkflow != nil || output.OnText != nil || output.OnUsage != nil
	if hasLegacy {
		bus.Subscribe(legacyCallbackObserver{
			onWorkflow: output.OnWorkflow,
			onText:     output.OnText,
			onUsage:    output.OnUsage,
		})
	} else if cli {
		bus.Subscribe(terminalObserver{})
	}
	if output.OnProgress != nil {
		bus.Subscribe(progressObserver{onProgress: output.OnProgress})
	}
	return bus
}

// legacyCallbackObserver bridges typed events to the old OnWorkflow/OnText/
// OnUsage callbacks. Used by tests that set these fields directly.
type legacyCallbackObserver struct {
	onWorkflow func(conversation.Intent, string)
	onText     func(string)
	onUsage    func(string)
}

func (o legacyCallbackObserver) OnEvent(e event.Event) {
	switch ev := e.(type) {
	case event.TextDeltaEvent:
		if o.onText != nil {
			o.onText(ev.Content)
		}
	case event.WorkflowProgressEvent:
		if o.onWorkflow != nil {
			o.onWorkflow(conversation.Intent(ev.Intent), ev.Workflow)
		}
	case event.UsageProgressEvent:
		if o.onUsage != nil {
			o.onUsage(ev.Summary)
		}
	case event.ResearchProgressEvent:
		if o.onText != nil {
			o.onText(ev.Message)
		}
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
	cursorStore        *learning.CursorStore
	llmExtractor       learning.LLMExtractor
	breaker            *learning.Breaker
	learningRedactor   func(string) string
	llmComplexityGate  learning.ComplexityGate
	outcomeStore       *learning.OutcomeStore
	routingAdvisor     *learning.RoutingAdvisor
	usageTracker       *llm.UsageTracker
	researchMaxRounds  int
	researchCostCapUSD float64
	selfState          *self.SelfState
	personaExtra       string
	wikiIdx            *wiki.Index
	wikiStore          *wiki.Store
	profiles           map[string]*profile.Profile
	skillReg           *skill.Registry
	skillCreator       *skill.Creator
	skillTracker       *skill.Tracker
	gitSync            *wiki.GitSync
	workDir            string
	daemonMode         bool
	principal          identity.Principal
	auditTrail         *audit.Trail
}

type compressionHookContextWindow struct {
	inner       *conversation.ContextWindow
	hooks       *agent.HookRegistry
	tracker     *tools.ReadTracker
	activeCalls []*compressionCall
	compressMu  sync.Mutex
	mu          sync.Mutex
}

type compressionCall struct {
	autoCompressed bool
}

func newCompressionHookContextWindow(
	inner *conversation.ContextWindow,
	hooks *agent.HookRegistry,
	tracker *tools.ReadTracker,
) *compressionHookContextWindow {
	w := &compressionHookContextWindow{
		inner:   inner,
		hooks:   hooks,
		tracker: tracker,
	}
	inner.OnAutoCompress(func() {
		if w.tracker != nil {
			w.tracker.ResetDedup()
		}
		w.markAutoCompressed()
	})
	return w
}

func (w *compressionHookContextWindow) Fit(ctx context.Context, messages []llm.Message, maxTokens int) ([]llm.Message, error) {
	return w.inner.Fit(ctx, messages, maxTokens)
}

func (w *compressionHookContextWindow) CompressMessages(ctx context.Context, provider llm.Provider, messages []llm.Message, maxTokens int) ([]llm.Message, error) {
	w.compressMu.Lock()
	defer w.compressMu.Unlock()

	call := &compressionCall{}
	w.pushCall(call)
	defer w.popCall(call)

	result, err := w.inner.CompressMessages(ctx, provider, messages, maxTokens)
	if err != nil {
		return nil, err
	}
	if call.autoCompressed && w.hooks != nil {
		if hookErr := w.hooks.RunOnCompression(ctx, len(messages), len(result)); hookErr != nil {
			return nil, hookErr
		}
	}
	return result, nil
}

func (w *compressionHookContextWindow) pushCall(call *compressionCall) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.activeCalls = append(w.activeCalls, call)
}

func (w *compressionHookContextWindow) popCall(call *compressionCall) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := len(w.activeCalls) - 1; i >= 0; i-- {
		if w.activeCalls[i] != call {
			continue
		}
		w.activeCalls = append(w.activeCalls[:i], w.activeCalls[i+1:]...)
		return
	}
}

func (w *compressionHookContextWindow) markAutoCompressed() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.activeCalls) == 0 {
		return
	}
	w.activeCalls[len(w.activeCalls)-1].autoCompressed = true
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

	var ctxWindow *conversation.ContextWindow
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
		WithConfig(cfg).
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
	profiles := make(map[string]*profile.Profile)
	if wikiStore != nil {
		var err error
		profiles, err = profile.LoadAll(wikiStore)
		if err != nil {
			app.Logger.Warn("profile load failed", "error", err)
		}
	}

	skillTracker := skill.NewTracker(cfg.DataDir)
	var skillCreator *skill.Creator
	if wikiStore != nil {
		skillCreator = skill.NewCreator(wikiStore, skillTracker, skillReg)
	}
	if skillCreator != nil {
		reg.Register(tools.NewSkillTool(skillCreator, skillReg))
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
	mgr.WithContextWindow(newCompressionHookContextWindow(
		ctxWindow,
		hooks,
		reg.ReadTracker(),
	))

	wfCfg := orchestrator.WorkflowConfig{
		Model:         model,
		MaxIterations: maxIterationsFromEnv(),
		SystemPrompt:  "",
		Hooks:         hooks,
		Permission:    perm,
	}
	learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
	learningDetector := secret.NewDetector()
	learningRedactor := learningDetector.RedactString
	learningStore := learning.NewStore(
		learningPath,
		learning.WithRedactor(learningRedactor),
	)
	cursorStore := learning.NewCursorStore(filepath.Join(cfg.DataDir, "lesson_cursors.jsonl"))
	outcomePath := filepath.Join(cfg.DataDir, "outcomes.jsonl")
	outcomeStore := learning.NewOutcomeStore(outcomePath, learning.WithOutcomeRedactor(learningRedactor))
	routingAdvisor := learning.NewRoutingAdvisor(outcomeStore)
	llmMinMessages := cfg.LLMExtraction.MinMessages
	if llmMinMessages == 0 {
		llmMinMessages = learning.DefaultComplexityGate.MinMessages
	}
	llmComplexityGate := learning.ComplexityGate{MinMessages: llmMinMessages, RequireToolCall: true}
	var llmExtractor learning.LLMExtractor
	var breaker *learning.Breaker
	if cfg.LLMExtraction.Enabled {
		breaker = learning.NewBreaker(app.Logger, learning.BreakerConfig{
			StatePath: filepath.Join(cfg.DataDir, "llm_extraction_state.json"),
		})
		lessonProvider, lessonModel := buildLessonProvider(cfg, provider)
		if lessonProvider != nil {
			var extractorOpts []learning.AnthropicExtractorOption
			if cfg.LLMExtraction.ClaudeCodeSignature {
				extractorOpts = append(extractorOpts, learning.WithSystemPrefix(
					"You are Claude Code, Anthropic's official CLI for Claude.\n\n",
				))
			}
			llmExtractor = learning.NewAnthropicExtractor(lessonProvider, lessonModel, extractorOpts...)
			app.Logger.Info("llm lesson: extractor enabled",
				"provider", lessonProvider.Name(),
				"model", lessonModel,
				"claude_code_signature", cfg.LLMExtraction.ClaudeCodeSignature,
			)
		} else {
			llmExtractor = &learning.MockLLMExtractor{}
			app.Logger.Warn("llm lesson: enabled but no provider available, falling back to mock")
		}
	}
	b := prompt.NewBuilder()
	b.Register(prompt.NewIdentityNode(100))
	b.Register(prompt.NewContextFilesNode(95))
	b.Register(prompt.NewPersonaNode(90))
	b.Register(prompt.NewLessonsNode(87, learningStore, 10, 1000))
	b.Register(prompt.NewSelfStateNode(85))
	b.Register(prompt.NewToolCatalogNode(80))
	b.Register(prompt.NewModelGuidanceNode(70))
	b.Register(prompt.NewSkillCatalogNode(65, skillReg))
	b.Register(prompt.NewSkillGuidanceNode(64))
	b.Register(prompt.NewDynamicBoundaryNode())
	b.Register(prompt.NewWikiRAGNode(60, 3))
	b.Register(prompt.NewMemoryContextNode(55, 5, 1200))
	b.Register(prompt.NewProjectContextNode(50))
	b.Register(prompt.NewBrownfieldNode(40))
	b.Register(prompt.NewGreenfieldNode(40))
	b.Register(prompt.NewSessionSummaryNode(30, 5, 800))
	b.Register(&prompt.LocaleInstructionNode{})

	return &executionRuntime{
		app:                app,
		provider:           provider,
		mgr:                mgr,
		router:             buildRouter(wfCfg),
		reg:                reg,
		wfCfg:              wfCfg,
		promptBuilder:      b,
		learningStore:      learningStore,
		cursorStore:        cursorStore,
		outcomeStore:       outcomeStore,
		routingAdvisor:     routingAdvisor,
		llmExtractor:       llmExtractor,
		breaker:            breaker,
		learningRedactor:   learningRedactor,
		llmComplexityGate:  llmComplexityGate,
		usageTracker:       usageTracker,
		researchMaxRounds:  cfg.Research.MaxRounds,
		researchCostCapUSD: cfg.Research.CostCapUSD,
		selfState:          selfState,
		personaExtra:       personaExtra,
		wikiIdx:            wikiIdx,
		wikiStore:          wikiStore,
		profiles:           profiles,
		skillReg:           skillReg,
		skillCreator:       skillCreator,
		skillTracker:       skillTracker,
		gitSync:            gitSync,
		workDir:            effectiveWorkDir,
		daemonMode:         daemonMode,
		principal:          defaultPrincipal,
		auditTrail:         auditTrail,
	}, nil
}

// buildLessonProvider returns the provider + model used for lesson extraction.
// Priority:
//  1. A dedicated Anthropic credential (cfg.LLMExtraction.APIKey, then
//     cfg.Anthropic.APIKey) — isolates lesson traffic.
//  2. The main provider (Codex OAuth / OpenAI / etc.) — shared auth, no
//     duplicate credential required.
//
// The returned model string may be empty, in which case the provider's own
// default is used.
func buildLessonProvider(cfg *config.Config, mainProvider llm.Provider) (llm.Provider, string) {
	if cfg == nil {
		return mainProvider, ""
	}
	apiKey := strings.TrimSpace(cfg.LLMExtraction.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(cfg.Anthropic.APIKey)
	}
	if apiKey != "" {
		var opts []llm.AnthropicOption
		if cfg.Anthropic.BaseURL != "" {
			opts = append(opts, llm.WithAnthropicBaseURL(cfg.Anthropic.BaseURL))
		}
		if cfg.Anthropic.Timeout > 0 {
			opts = append(opts, llm.WithAnthropicTimeout(time.Duration(cfg.Anthropic.Timeout)*time.Second))
		}
		model := cfg.LLMExtraction.Model
		if model == "" {
			model = llm.ResolveModel("haiku")
		}
		resolved := llm.ResolveModel(model)
		return llm.NewAnthropicProvider(apiKey, resolved, opts...), resolved
	}
	return mainProvider, cfg.LLMExtraction.Model
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
		Store:          rt.learningStore,
		SelfState:      rt.selfState,
		Logger:         rt.app.Logger,
		LLMExtractor:   rt.llmExtractor,
		CursorStore:    rt.cursorStore,
		Breaker:        rt.breaker,
		ComplexityGate: rt.llmComplexityGate,
		Redact:         rt.learningRedactor,
	}
}

func (rt *executionRuntime) runTask(
	ctx context.Context,
	sess *agent.Session,
	messages []llm.Message,
	userInput string,
	output orchestrationOutput,
) ([]llm.Message, string, error) {
	bus := newBus(output, !rt.daemonMode)

	if rt.app.Config.MagicDocs.Enabled && rt.wikiStore != nil {
		md := magicdocs.New(magicdocs.Config{
			Enabled:   true,
			Store:     rt.wikiStore,
			Provider:  rt.provider,
			Model:     rt.app.Config.MagicDocs.Model,
			Logger:    rt.app.Logger.With("component", "magic-docs"),
			SessionID: sess.ID,
		})
		bus.Subscribe(md.Observer())
		md.Start(ctx)
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := md.Close(closeCtx); err != nil {
				rt.app.Logger.Warn("magic-docs close error", "error", err)
			}
		}()
	}

	userInput = normalizeSkillInput(userInput)
	if rt.skillReg != nil && strings.HasPrefix(userInput, "/") {
		result, summary, handled, err := rt.trySkillExecution(ctx, sess, messages, userInput, bus, output)
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
	bus.Emit(event.WorkflowProgressEvent{
		Base:     event.NewBase(),
		Intent:   string(intent),
		Workflow: wf.Name(),
	})
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
		Locale:        rt.mgr.LastLocale(sess.ID),
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
		ProjectID:     routeCtx.ProjectID,
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
		Sink:     bus,
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

	wfStart := time.Now()
	result, err := wf.Run(ctx, input)
	elapsed := time.Since(wfStart)
	if err != nil {
		return nil, "", fmt.Errorf("workflow %s: %w", wf.Name(), err)
	}

	if rt.outcomeStore != nil && routeCtx.ProjectID != "" && learning.ShouldRecord(result.FinishReason) {
		record := learning.OutcomeRecord{
			ProjectID:      routeCtx.ProjectID,
			Intent:         string(intent),
			Workflow:       result.Workflow,
			FinishReason:   result.FinishReason,
			Success:        learning.IsSuccessful(result.FinishReason),
			Duration:       elapsed.Seconds(),
			Cost:           0,
			Iterations:     result.Iterations,
			InputSnippet:   runeSnippet(userInput, 100),
			EstimatedFiles: routeCtx.EstimatedFiles,
			ExistingCode:   routeCtx.ExistingCode,
			PreferenceUsed: pref != nil,
		}
		if appendErr := rt.outcomeStore.Append(record); appendErr != nil {
			rt.app.Logger.Warn("outcome store: append failed", "error", appendErr)
		}
		if rotErr := rt.outcomeStore.AutoRotateIfNeeded(300); rotErr != nil {
			rt.app.Logger.Warn("outcome store: auto-rotate failed", "error", rotErr)
		}

		if pref, advErr := rt.routingAdvisor.Advise(routeCtx.ProjectID); advErr == nil && pref != nil {
			if saveErr := wiki.SaveWorkflowPreference(rt.wikiStore, routeCtx.ProjectID, pref); saveErr != nil {
				rt.app.Logger.Warn("routing advisor: wiki save failed", "error", saveErr)
			}
		}
	}

	if usage := llm.FormatUsageSummary(rt.wfCfg.Model, result.Usage); usage != "" {
		bus.Emit(event.UsageProgressEvent{Base: event.NewBase(), Summary: usage})
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
	bus *event.Bus,
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
	bus.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: fmt.Sprintf("Executing skill: %s\n", skillName)})

	result, err := rt.skillReg.Execute(ctx, skill.ExecuteParams{
		SkillName:  skillName,
		Args:       args,
		Provider:   rt.provider,
		ToolReg:    rt.reg,
		Model:      rt.wfCfg.Model,
		Sink:       bus,
		Permission: rt.wfCfg.Permission,
		Hooks:      rt.wfCfg.Hooks,
		Locale:     rt.mgr.LastLocale(sess.ID),
	})
	if err != nil {
		rt.recordSkillUsage(sess.ID, skillName, false)
		return nil, "", true, fmt.Errorf("skill %q: %w", skillName, err)
	}
	rt.recordSkillUsage(sess.ID, skillName, true)
	if usage := llm.FormatUsageSummary(rt.wfCfg.Model, result.Usage); usage != "" {
		bus.Emit(event.UsageProgressEvent{Base: event.NewBase(), Summary: usage})
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

func (rt *executionRuntime) recordSkillUsage(sessionID, skillName string, success bool) {
	if rt == nil || rt.skillTracker == nil {
		return
	}
	if err := rt.skillTracker.RecordUsage(skill.UsageRecord{
		SkillName: skillName,
		SessionID: sessionID,
		Success:   success,
	}); err != nil && rt.app != nil && rt.app.Logger != nil {
		rt.app.Logger.Warn("skill usage tracking failed", "skill", skillName, "error", err)
	}
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
	return func(ctx context.Context, payload string, sink event.Sink) (daemon.TaskResult, error) {
		taskPayload := daemon.ParseTaskPayload(payload)
		userInput := taskPayload.Prompt
		if userInput == "" {
			return daemon.TaskResult{}, fmt.Errorf("daemon task payload is empty")
		}
		if taskPayload.Type == daemon.TaskTypeSkillPromote {
			if rt.skillCreator == nil || rt.wikiStore == nil {
				return daemon.TaskResult{}, fmt.Errorf("skill promotion: creator or wiki store not configured")
			}
			consolidator := skill.NewConsolidator(rt.skillCreator, rt.skillTracker, rt.skillReg, rt.wikiStore, skill.DefaultConsolidatorConfig())
			result, err := consolidator.Run(ctx)
			if err != nil {
				return daemon.TaskResult{}, fmt.Errorf("skill promotion: %w", err)
			}
			summary := fmt.Sprintf("promoted %d, cleaned %d", len(result.Promoted), len(result.Cleaned))
			return daemon.TaskResult{Result: summary, Summary: summary}, nil
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
			OnProgress: daemonProgressFromSink(sink),
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

// daemonProgressFromSink converts an event.Sink into a daemon.ProgressEvent
// callback. The progressObserver on the bus converts typed events back to
// daemon.ProgressEvent, and this callback encodes them for the wire.
func daemonProgressFromSink(sink event.Sink) func(daemon.ProgressEvent) {
	if sink == nil {
		return nil
	}
	return func(ev daemon.ProgressEvent) {
		raw := daemon.EncodeProgressEvent(ev)
		if raw != "" {
			sink.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: raw})
		}
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

func runeSnippet(s string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes])
}
