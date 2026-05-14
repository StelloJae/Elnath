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
	"github.com/stello/elnath/internal/agent/reflection"
	"github.com/stello/elnath/internal/agentic"
	agenticactors "github.com/stello/elnath/internal/agentic/actors"
	agenticapprovals "github.com/stello/elnath/internal/agentic/approvals"
	agenticenqueue "github.com/stello/elnath/internal/agentic/enqueue"
	agenticmemory "github.com/stello/elnath/internal/agentic/memory"
	agenticpolicy "github.com/stello/elnath/internal/agentic/policy"
	agentictools "github.com/stello/elnath/internal/agentic/tools"
	agenticverification "github.com/stello/elnath/internal/agentic/verification"
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
	"github.com/stello/elnath/internal/profile"
	"github.com/stello/elnath/internal/prompt"
	"github.com/stello/elnath/internal/research"
	"github.com/stello/elnath/internal/scheduler"
	"github.com/stello/elnath/internal/secret"
	"github.com/stello/elnath/internal/self"
	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
	"github.com/stello/elnath/internal/worktree"
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
	terminalEventHandlers := map[string]func(event.Event){
		event.TextDeltaEvent{}.EventType(): func(e event.Event) {
			fmt.Print(e.(event.TextDeltaEvent).Content)
		},
		event.WorkflowProgressEvent{}.EventType(): func(e event.Event) {
			ev := e.(event.WorkflowProgressEvent)
			fmt.Printf("[%s → %s]\n\n", ev.Intent, ev.Workflow)
		},
		event.UsageProgressEvent{}.EventType(): func(e event.Event) {
			ev := e.(event.UsageProgressEvent)
			fmt.Println()
			fmt.Println(ev.Summary)
		},
		event.ResearchProgressEvent{}.EventType(): func(e event.Event) {
			fmt.Print(e.(event.ResearchProgressEvent).Message)
		},
	}
	if handler, ok := terminalEventHandlers[e.EventType()]; ok {
		handler(e)
	}
}

// progressObserver converts typed events back to daemon.ProgressEvent
// for the daemon task runner's onText-based progress protocol.
type progressObserver struct {
	onProgress func(daemon.ProgressEvent)
}

func (p progressObserver) OnEvent(e event.Event) {
	progressEventHandlers := map[string]func(event.Event){
		event.ToolProgressEvent{}.EventType(): func(e event.Event) {
			ev := e.(event.ToolProgressEvent)
			p.onProgress(daemon.ToolProgressEvent(ev.ToolName, ev.Preview))
		},
		event.WorkflowProgressEvent{}.EventType(): func(e event.Event) {
			ev := e.(event.WorkflowProgressEvent)
			p.onProgress(daemon.WorkflowProgressEvent(ev.Intent, ev.Workflow))
		},
		event.UsageProgressEvent{}.EventType(): func(e event.Event) {
			p.onProgress(daemon.UsageProgressEvent(e.(event.UsageProgressEvent).Summary))
		},
		event.TextDeltaEvent{}.EventType(): func(e event.Event) {
			ev := e.(event.TextDeltaEvent)
			if ev.Content != "" {
				p.onProgress(daemon.TextProgressEvent(ev.Content))
			}
		},
		event.ResearchProgressEvent{}.EventType(): func(e event.Event) {
			ev := e.(event.ResearchProgressEvent)
			if ev.Message != "" {
				p.onProgress(daemon.TextProgressEvent(ev.Message))
			}
		},
	}
	if handler, ok := progressEventHandlers[e.EventType()]; ok {
		handler(e)
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
	legacyEventHandlers := map[string]func(event.Event){
		event.TextDeltaEvent{}.EventType(): func(e event.Event) {
			if o.onText != nil {
				o.onText(e.(event.TextDeltaEvent).Content)
			}
		},
		event.WorkflowProgressEvent{}.EventType(): func(e event.Event) {
			if o.onWorkflow != nil {
				ev := e.(event.WorkflowProgressEvent)
				o.onWorkflow(conversation.Intent(ev.Intent), ev.Workflow)
			}
		},
		event.UsageProgressEvent{}.EventType(): func(e event.Event) {
			if o.onUsage != nil {
				o.onUsage(e.(event.UsageProgressEvent).Summary)
			}
		},
		event.ResearchProgressEvent{}.EventType(): func(e event.Event) {
			if o.onText != nil {
				o.onText(e.(event.ResearchProgressEvent).Message)
			}
		},
	}
	if handler, ok := legacyEventHandlers[e.EventType()]; ok {
		handler(e)
	}
}

type executionRuntime struct {
	app                *core.App
	db                 *core.DB
	provider           llm.Provider
	mgr                *conversation.Manager
	router             *orchestrator.Router
	reg                *tools.Registry
	guard              *tools.PathGuard
	wfCfg              orchestrator.WorkflowConfig
	promptBuilder      *prompt.Builder
	learningStore      *learning.Store
	cursorStore        *learning.CursorStore
	llmExtractor       learning.LLMExtractor
	breaker            *learning.Breaker
	learningRedactor   func(string) string
	llmComplexityGate  learning.ComplexityGate
	agenticStore       *agentic.Store
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
	reflectPool        *reflection.Pool
	reflectStore       *reflection.FileStore
	reflectModel       string
	reflectMaxTurns    int
	reflectTimeout     time.Duration
	completionRetryMax int
	completionCtxMu    sync.Mutex
	completionCtxs     map[int64]completionContractSummary
	planModeController *agent.PlanModeController
	taskStopTool       *daemon.TaskStopTool
}

type pendingUserQuestionValidator struct {
	store *learning.OutcomeStore
}

func (v pendingUserQuestionValidator) ValidateUserQuestionAnswer(_ context.Context, sessionID, requestID string) (daemon.UserQuestionAnswerValidation, error) {
	if v.store == nil {
		return daemon.UserQuestionAnswerValidation{}, fmt.Errorf("outcome store unavailable")
	}
	records, err := v.store.Recent(0)
	if err != nil {
		return daemon.UserQuestionAnswerValidation{}, err
	}
	question, ok := learning.FindPendingUserQuestion(records, sessionID, requestID)
	if !ok {
		return daemon.UserQuestionAnswerValidation{}, nil
	}
	return daemon.UserQuestionAnswerValidation{
		Found:         true,
		Question:      question.Question,
		QuestionChars: question.QuestionChars,
	}, nil
}

type delegateEnqueueRuntimeService struct {
	service *agenticenqueue.Service
}

func (s delegateEnqueueRuntimeService) EnqueueDelegated(ctx context.Context, req agentictools.DelegateEnqueueRequest) (*agentictools.DelegateEnqueueResult, error) {
	if s.service == nil {
		return nil, fmt.Errorf("enqueue service unavailable")
	}
	result, err := s.service.Enqueue(ctx, agenticenqueue.Request{
		TaskID:                  req.TaskID,
		OperatorID:              req.OperatorID,
		Reason:                  req.Reason,
		RequestedEnforcement:    req.RequestedEnforcement,
		RequestedCompletionGate: req.RequestedCompletionGate,
	})
	if err != nil {
		return nil, err
	}
	out := &agentictools.DelegateEnqueueResult{
		QueueTaskID: result.QueueTaskID,
		Existed:     result.Existed,
	}
	if result.Decision != nil {
		out.DecisionID = result.Decision.ID
		out.DecisionStatus = result.Decision.Status
	}
	return out, nil
}

type agenticRuntimeEnforcementKey struct{}

func withAgenticRuntimeEnforcement(ctx context.Context, mode string) context.Context {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return ctx
	}
	return context.WithValue(ctx, agenticRuntimeEnforcementKey{}, mode)
}

func agenticRuntimeEnforcementFromContext(ctx context.Context) (string, bool) {
	mode, ok := ctx.Value(agenticRuntimeEnforcementKey{}).(string)
	return strings.TrimSpace(mode), ok && strings.TrimSpace(mode) != ""
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

// resolveProviderContextWindow looks up the active model's input-context limit
// from the provider's model catalog so the conversation manager can size its
// compaction threshold against the real window instead of the static 100K
// fallback. Returns 0 when the provider does not advertise the model — the
// manager treats that as "unknown" and falls back to MaxContextTokens.
func resolveProviderContextWindow(provider llm.Provider, model string) int {
	if provider == nil || model == "" {
		return 0
	}
	for _, m := range provider.Models() {
		if m.ID == model {
			return m.ContextWindow
		}
	}
	return 0
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
		// Surface a CLI-visible marker so the user/daemon log sees that a Stage 2
		// compaction just happened (mirrors Claude Code's "Conversation compacted"
		// signal). In daemon mode stderr is captured into daemon.log by launchd.
		fmt.Fprintln(os.Stderr, "[context compacted]")
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
	if err := agentic.InitSchema(db.Main); err != nil {
		return nil, fmt.Errorf("init agentic schema: %w", err)
	}
	agenticStore := agentic.NewStore(db.Main)

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
		WithProviderContextWindow(resolveProviderContextWindow(provider, model)).
		WithMemoryLimitMB(conversation.DefaultMemoryLimitMB).
		WithHistoryStore(historyStore).
		WithConfig(cfg).
		WithLogger(app.Logger)

	effectiveWorkDir := workDir
	if effectiveWorkDir == "" {
		effectiveWorkDir, _ = os.Getwd()
	}
	guard := tools.NewPathGuard(effectiveWorkDir, protectedPaths)
	var rt *executionRuntime
	// buildBashRunnerForConfig returns a shareable facade. Stateful
	// sandbox/proxy runners are created inside each Run so daemon workers do
	// not share proxy decision buffers or drain goroutines.
	runner, err := buildBashRunnerForConfig(cfg)
	if err != nil {
		return nil, err
	}
	app.RegisterCloser("bash runner", bashRunnerCloser{runner: runner})
	learningDetector := secret.NewDetector()
	learningRedactor := learningDetector.RedactString
	outcomePath := filepath.Join(cfg.DataDir, "outcomes.jsonl")
	outcomeStore := learning.NewOutcomeStore(outcomePath, learning.WithOutcomeRedactor(learningRedactor))
	reg := buildToolRegistryWithSecondaryCaller(guard, runner, llm.NewDynamicSecondaryModelCaller(func() llm.Provider {
		if rt == nil {
			return provider
		}
		return rt.provider
	}))
	processManager := tools.NewProcessManager(guard)
	app.RegisterCloser("process manager", processManagerCloser{manager: processManager})
	reg.Register(tools.NewProcessStartTool(processManager))
	reg.Register(tools.NewProcessMonitorTool(processManager))
	reg.Register(tools.NewProcessWaitTool(processManager))
	reg.Register(tools.NewProcessStopTool(processManager))
	planModeController := agent.NewPlanModeController(perm)
	reg.Register(agent.NewEnterPlanModeTool(planModeController))
	reg.Register(agent.NewExitPlanModeTool(planModeController))
	reg.Register(agent.NewAskUserQuestionTool())
	reg.Register(learning.NewUserQuestionListTool(outcomeStore))
	reg.Register(learning.NewUserQuestionWaitTool(outcomeStore))
	taskQueue, err := daemon.NewQueueNoRecover(db.Main)
	if err != nil {
		return nil, fmt.Errorf("open task queue tools: %w", err)
	}
	reg.Register(daemon.NewTaskCreateTool(taskQueue))
	reg.Register(daemon.NewUserQuestionAnswerToolWithValidator(taskQueue, pendingUserQuestionValidator{store: outcomeStore}))
	reg.Register(daemon.NewTaskListTool(taskQueue))
	reg.Register(daemon.NewTaskGetTool(taskQueue))
	taskStopTool := daemon.NewTaskStopTool(taskQueue)
	reg.Register(taskStopTool)
	reg.Register(daemon.NewTaskOutputTool(taskQueue))
	reg.Register(daemon.NewTaskMonitorTool(taskQueue))
	reg.Register(daemon.NewTaskUpdateTool(taskQueue))
	reg.Register(agentictools.NewActorGraphTool(agenticStore))
	reg.Register(agentictools.NewTaskEvidenceTool(agenticStore))
	reg.Register(agentictools.NewDelegateCreateTool(agenticStore))
	reg.Register(agentictools.NewDelegateListTool(agenticStore))
	reg.Register(agentictools.NewDelegateStatusTool(agenticStore, taskQueue))
	reg.Register(agentictools.NewDelegateEnqueueTool(agenticStore, delegateEnqueueRuntimeService{service: agenticenqueue.NewService(agenticStore, taskQueue, agenticenqueue.Options{
		EnforcementMode:    cfg.Agentic.Enforcement.Mode,
		CompletionGateMode: cfg.Agentic.CompletionGate.Mode,
	})}))
	reg.Register(agentictools.NewActorMessageSendTool(agenticStore))
	reg.Register(agentictools.NewActorMessageListTool(agenticStore))
	schedulePath := resolveRuntimeScheduledTasksPath(cfg)
	reg.Register(scheduler.NewScheduleCreateTool(schedulePath))
	reg.Register(scheduler.NewScheduleListTool(schedulePath))
	reg.Register(scheduler.NewScheduleDeleteTool(schedulePath))
	worktreeManager := worktree.NewManager(effectiveWorkDir)
	reg.Register(worktree.NewEnterTool(worktreeManager))
	reg.Register(worktree.NewListTool(worktreeManager))
	reg.Register(worktree.NewRunTool(worktreeManager, runner))
	reg.Register(worktree.NewPruneTool(worktreeManager))
	reg.Register(worktree.NewExitTool(worktreeManager))
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
		var opts []wiki.StoreOption
		if wikiIdx != nil {
			opts = append(opts, wiki.WithIndex(wikiIdx))
		}
		if ws, err := wiki.NewStore(cfg.WikiDir, opts...); err == nil {
			wikiStore = ws
		}
	}
	skillReg := skill.NewRegistry()
	if wikiStore != nil {
		if err := skillReg.Load(wikiStore); err != nil {
			app.Logger.Warn("skill registry load failed", "error", err)
		}
	}
	homeDir, _ := os.UserHomeDir()
	rootOpts := skill.CompatibleSkillRootOptions{DisablePluginCache: !config.SkillsPluginCacheEnabled(cfg)}
	for _, root := range skill.DefaultCompatibleSkillRootsWithOptions(effectiveWorkDir, homeDir, rootOpts) {
		if err := skillReg.LoadCompatibleSkillRoots([]skill.CompatibleSkillRoot{root}); err != nil {
			app.Logger.Warn("compatible skill registry load failed", "root", root.Path, "source", root.Source, "error", err)
		}
	}
	reg.Register(newCommandCatalogTool(skillReg))
	reg.Register(skill.NewCatalogTool(skillReg))
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
	reg.Register(skill.NewInvocationTool(skill.InvocationToolConfig{
		Registry: skillReg,
		ProviderResolver: func() llm.Provider {
			if rt == nil {
				return provider
			}
			return rt.provider
		},
		Tools: reg,
		ModelResolver: func() string {
			if rt == nil {
				return model
			}
			return rt.wfCfg.Model
		},
		Permission: perm,
		Hooks:      hooks,
		Locale:     string(loadLocale()),
	}))

	auditPath := filepath.Join(cfg.DataDir, "audit.jsonl")
	auditTrail, err := audit.NewTrail(auditPath)
	if err != nil {
		app.Logger.Warn("audit trail unavailable", "error", err)
	} else {
		app.RegisterCloser("audit trail", auditTrail)
	}

	var (
		reflectPool     *reflection.Pool
		reflectStore    *reflection.FileStore
		reflectModel    string
		reflectMaxTurns int
		reflectTimeout  time.Duration
	)
	if cfg.SelfHealing.Enabled {
		path := cfg.SelfHealing.Path
		if path == "" {
			path = filepath.Join(cfg.DataDir, "self_heal_attempts.jsonl")
		}
		reflectStore = reflection.NewFileStore(path)
		reflectModel = cfg.SelfHealing.Model
		if reflectModel == "" {
			reflectModel = model
		}
		reflectMaxTurns = cfg.SelfHealing.MaxTurns
		if reflectMaxTurns <= 0 {
			reflectMaxTurns = 20
		}
		reflectTimeout = time.Duration(cfg.SelfHealing.TimeoutSeconds) * time.Second
		if reflectTimeout <= 0 {
			reflectTimeout = 15 * time.Second
		}
		engine := reflection.NewLLMEngine(provider, reflectModel,
			reflection.WithEngineTimeout(reflectTimeout),
			reflection.WithEngineMaxTurns(reflectMaxTurns),
		)
		concurrency := cfg.SelfHealing.MaxConcurrent
		if concurrency <= 0 {
			concurrency = 2
		}
		queueSize := cfg.SelfHealing.QueueSize
		if queueSize <= 0 {
			queueSize = 10
		}
		reflectPool = reflection.NewPool(engine, reflectStore, concurrency, queueSize,
			reflection.WithPoolLogger(app.Logger.With("component", "reflection")),
		)
		app.RegisterCloser("reflection pool", reflectionPoolCloser{pool: reflectPool})
		app.Logger.Info("self-healing observer enabled",
			"path", path,
			"model", reflectModel,
			"max_concurrent", concurrency,
			"queue_size", queueSize,
		)
	}
	completionRetryMax := 0
	if cfg.SelfHealing.Enabled && !cfg.SelfHealing.ObserveOnly {
		completionRetryMax = cfg.SelfHealing.CompletionRetryMax
		if completionRetryMax < 0 {
			completionRetryMax = 0
		}
		if completionRetryMax > maxCompletionRetryAttempts {
			completionRetryMax = maxCompletionRetryAttempts
		}
	}
	hooks.Add(secret.NewSecretScanHook(secret.NewDetector(), auditTrail))
	wrappedCtxWindow := newCompressionHookContextWindow(
		ctxWindow,
		hooks,
		reg.ReadTracker(),
	)
	mgr.WithContextWindow(wrappedCtxWindow)

	providerCW := resolveProviderContextWindow(provider, model)
	compressionBudget := conversation.ResolveCompressionBudget(providerCW, cfg.MaxContextTokens)
	wfCfg := orchestrator.WorkflowConfig{
		Model:                model,
		MaxIterations:        maxIterationsFromEnv(),
		SystemPrompt:         "",
		ReasoningEffort:      cfg.Reasoning.Effort,
		ReasoningEffortMode:  cfg.Reasoning.EffortMode,
		ToolExposureMode:     cfg.Tools.ExposureMode,
		Hooks:                hooks,
		Permission:           perm,
		ContextWindow:        wrappedCtxWindow,
		CompressionMaxTokens: compressionBudget,
		CorrectionScope:      runtimeCorrectionScopeFromEnv(),
		VerificationPolicy:   runtimeVerificationPolicyFromEnv(),
	}
	learningPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
	learningStore := learning.NewStore(
		learningPath,
		learning.WithRedactor(learningRedactor),
	)
	cursorStore := learning.NewCursorStore(filepath.Join(cfg.DataDir, "lesson_cursors.jsonl"))
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
	b.Register(prompt.NewChatSystemPromptNode(96))
	b.Register(prompt.NewContextFilesNode(95))
	b.Register(prompt.NewPersonaNode(90))
	b.Register(prompt.NewLessonsNode(87, learningStore, 10, 1000))
	b.Register(prompt.NewSelfStateNode(85))
	b.Register(prompt.NewToolCatalogNode(80))
	b.Register(prompt.NewChatToolGuideNode(78))
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

	rt = &executionRuntime{
		app:                app,
		db:                 db,
		provider:           provider,
		mgr:                mgr,
		router:             buildRouter(wfCfg),
		reg:                reg,
		guard:              guard,
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
		agenticStore:       agenticStore,
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
		reflectPool:        reflectPool,
		reflectStore:       reflectStore,
		reflectModel:       reflectModel,
		reflectMaxTurns:    reflectMaxTurns,
		reflectTimeout:     reflectTimeout,
		completionRetryMax: completionRetryMax,
		planModeController: planModeController,
		taskStopTool:       taskStopTool,
	}
	reg.Register(newRuntimeCommandTool(func() *executionRuntime { return rt }))
	return rt, nil
}

func (rt *executionRuntime) bindRunningTaskCanceller(canceller daemon.RunningTaskCanceller) {
	if rt == nil || rt.taskStopTool == nil {
		return
	}
	rt.taskStopTool.WithRunningCanceller(canceller)
}

// reflectionPoolCloser adapts *reflection.Pool to the core.Closer interface,
// draining pending reflection work with a 30s grace window (spec §3.3
// safeguards) during app shutdown.
type reflectionPoolCloser struct {
	pool *reflection.Pool
}

func (c reflectionPoolCloser) Close() error {
	if c.pool == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.pool.Shutdown(ctx)
}

// buildReflectionEnqueuer returns an agent.ReflectionEnqueuer closure that
// adapts agent.ReflectionInput to reflection.Input + StoreMeta and forwards
// to the daemon-level pool. Returns nil when self-healing is disabled.
//
// Principal/ProjectID resolution mirrors the routing path at lines 767-772:
// session.Principal wins when it carries a real ProjectID (the "unknown"
// sentinel from identity.LegacyPrincipal must not override rt.principal), so
// Telegram-routed and follow-up tasks record observations under the caller's
// identity rather than the daemon fallback workspace. The enrichment fields
// are passthrough-only — trigger, skip, and strategy evaluation logic remain
// untouched (spec §3.1 Phase 0 observe-only invariant).
func (rt *executionRuntime) buildReflectionEnqueuer(sess *agent.Session, userInput string) agent.ReflectionEnqueuer {
	if rt.reflectPool == nil {
		return nil
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	toolNames := []string{}
	if rt.reg != nil {
		toolNames = rt.reg.Names()
	}
	subject := runeSnippet(userInput, 80)
	fp := reflection.ComputeFingerprint(subject, toolNames)
	principalUserID := rt.principal.UserID
	projectID := rt.principal.ProjectID
	if sess != nil {
		if sp := sess.Principal.UserID; sp != "" {
			principalUserID = sp
		}
		if sp := sess.Principal.ProjectID; sp != "" && sp != "unknown" {
			projectID = sp
		}
	}
	return func(in agent.ReflectionInput) {
		rt.reflectPool.Enqueue(
			reflection.Input{
				Transcript:   in.Messages,
				ErrorSummary: in.ErrorSummary,
				TaskMeta: reflection.TaskMeta{
					SessionID: sessionID,
					Principal: principalUserID,
					ProjectID: projectID,
				},
				Fingerprint:   fp,
				FinishReason:  string(in.FinishReason),
				ErrorCategory: string(in.ErrCategory),
			},
			reflection.StoreMeta{
				TS:        time.Now().UTC(),
				SessionID: sessionID,
				Principal: principalUserID,
				ProjectID: projectID,
			},
		)
	}
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
	deps := &orchestrator.LearningDeps{
		Store:          rt.learningStore,
		SelfState:      rt.selfState,
		Logger:         rt.app.Logger,
		LLMExtractor:   rt.llmExtractor,
		CursorStore:    rt.cursorStore,
		Breaker:        rt.breaker,
		ComplexityGate: rt.llmComplexityGate,
		Redact:         rt.learningRedactor,
	}
	if rt.agenticStore != nil {
		deps.MemoryGate = agenticmemory.NewGate(rt.agenticStore)
	}
	return deps
}

func (rt *executionRuntime) configureAgenticToolGateway(
	ctx context.Context,
	cfg orchestrator.WorkflowConfig,
	requestedMode string,
	taskID int64,
	hasTaskID bool,
) (context.Context, orchestrator.WorkflowConfig, error) {
	requestedMode = strings.ToLower(strings.TrimSpace(requestedMode))
	if requestedMode == "" {
		return ctx, cfg, nil
	}
	if requestedMode != config.AgenticEnforcementModeGateway {
		return ctx, cfg, fmt.Errorf("unsupported agentic enforcement mode %q", requestedMode)
	}
	if !agenticGatewayConfigPermitted(rt.app.Config) {
		return ctx, cfg, fmt.Errorf("agentic gateway enforcement requested but config maximum is %q", agenticEnforcementConfigMode(rt.app.Config))
	}
	if !hasTaskID || taskID == 0 {
		return ctx, cfg, fmt.Errorf("agentic gateway enforcement requested but agentic task id is required")
	}
	exec, err := rt.newAgenticGatewayExecutor(cfg.ToolExecutor)
	if err != nil {
		return ctx, cfg, err
	}
	cfg.ToolExecutor = exec
	ctx = agentictools.WithContext(ctx, agentictools.Context{TaskID: taskID})
	return ctx, cfg, nil
}

func (rt *executionRuntime) newAgenticGatewayExecutor(base tools.Executor) (tools.Executor, error) {
	if rt == nil || rt.db == nil || rt.db.Main == nil || rt.agenticStore == nil {
		return nil, fmt.Errorf("agentic gateway enforcement requested but agentic runtime store is not configured")
	}
	if base == nil {
		base = rt.reg
	}
	if base == nil {
		return nil, fmt.Errorf("agentic gateway enforcement requested but base tool executor is not configured")
	}
	approvalStore, err := daemon.NewApprovalStore(rt.db.Main)
	if err != nil {
		return nil, fmt.Errorf("agentic gateway approval store: %w", err)
	}
	approvalBridge := agenticapprovals.NewBridge(rt.db.Main, rt.agenticStore, approvalStore)
	return agentictools.NewGateway(base, rt.agenticStore, agenticpolicy.NewEvaluator(), approvalBridge), nil
}

func agenticGatewayConfigPermitted(cfg *config.Config) bool {
	return agenticEnforcementConfigMode(cfg) == config.AgenticEnforcementModeGateway
}

func agenticEnforcementConfigMode(cfg *config.Config) string {
	if cfg == nil {
		return config.AgenticEnforcementModeObserve
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Agentic.Enforcement.Mode))
	if mode == "" {
		return config.AgenticEnforcementModeObserve
	}
	return mode
}

func agenticCompletionGateConfigMode(cfg *config.Config) string {
	if cfg == nil {
		return config.AgenticCompletionGateModeObserve
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Agentic.CompletionGate.Mode))
	if mode == "" {
		return config.AgenticCompletionGateModeObserve
	}
	return mode
}

func (rt *executionRuntime) runTask(
	ctx context.Context,
	sess *agent.Session,
	messages []llm.Message,
	userInput string,
	output orchestrationOutput,
) ([]llm.Message, string, error) {
	ctx = rt.toolContextForSession(ctx, sess)
	bus := newBus(output, !rt.daemonMode)

	if rt.app.Config.MagicDocs.Enabled && rt.wikiStore != nil {
		md := magicdocs.New(magicdocs.Config{
			Enabled:       true,
			Store:         rt.wikiStore,
			Provider:      rt.provider,
			Model:         rt.app.Config.MagicDocs.Model,
			Logger:        rt.app.Logger.With("component", "magic-docs"),
			SessionID:     sess.ID,
			PromptBuilder: rt.promptBuilder,
			Self:          rt.selfState,
			WikiIdx:       rt.wikiIdx,
			PersonaExtra:  rt.personaExtra,
			ProviderName:  rt.provider.Name(),
			WorkDir:       rt.workDir,
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
	if result, summary, handled, err := rt.tryLocalSlashCommand(sess, messages, userInput, bus); handled {
		return result, summary, err
	}
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
	// Prefer the session principal's ProjectID so daemon-routed tasks
	// (Telegram, follow-ups) record outcomes under the caller's project,
	// not the daemon fallback principal's workspace-derived ID.
	// The "unknown" sentinel from identity.LegacyPrincipal must not override
	// a real rt.principal value.
	routeCtx.ProjectID = rt.principal.ProjectID
	if sess != nil {
		if sp := sess.Principal.ProjectID; sp != "" && sp != "unknown" {
			routeCtx.ProjectID = sp
		}
	}
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
		SessionID:      sess.ID,
		UserInput:      userInput,
		Self:           rt.selfState,
		Principal:      rt.principal,
		Messages:       promptMessages,
		WikiIdx:        rt.wikiIdx,
		TokenBudget:    0,
		Locale:         rt.mgr.LastLocale(sess.ID),
		PersonaExtra:   rt.personaExtra,
		Model:          rt.wfCfg.Model,
		Provider:       rt.provider.Name(),
		ToolNames:      rt.reg.Names(),
		WorkDir:        rt.workDir,
		SessionWorkDir: rt.sessionRenderWorkDir(sess),
		ExistingCode:   routeCtx.ExistingCode,
		VerifyHint:     routeCtx.VerificationHint,
		BenchmarkMode:  routeCtx.BenchmarkMode,
		TaskLanguage:   taskLanguageFromEnv(),
		DaemonMode:     rt.daemonMode,
		MessageCount:   len(prepared),
		ProjectID:      routeCtx.ProjectID,
	}
	systemPrompt, err := rt.promptBuilder.Build(ctx, renderState)
	if err != nil {
		return nil, "", fmt.Errorf("prompt build: %w", err)
	}
	cfg := rt.wfCfg
	cfg.SystemPrompt = systemPrompt
	cfg.ReflectionEnqueuer = rt.buildReflectionEnqueuer(sess, userInput)

	agenticTaskID, hasAgenticTask := daemon.AgenticTaskIDFromContext(ctx)
	if mode, ok := agenticRuntimeEnforcementFromContext(ctx); ok {
		var err error
		ctx, cfg, err = rt.configureAgenticToolGateway(ctx, cfg, mode, agenticTaskID, hasAgenticTask)
		if err != nil {
			return nil, "", err
		}
	}
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
		if input.Learning != nil && hasAgenticTask {
			input.Learning.AgenticTaskID = agenticTaskID
		}
	}
	if wf.Name() == "ralph" && rt.agenticStore != nil && hasAgenticTask {
		input.AgenticTaskID = agenticTaskID
		input.VerificationRecorder = agenticverification.NewRecorder(rt.agenticStore)
	}
	if wf.Name() == "team" && rt.agenticStore != nil && hasAgenticTask {
		input.AgenticTaskID = agenticTaskID
		input.ActorRecorder = agenticactors.NewRecorder(rt.agenticStore)
	}
	if wf.Name() == "research" && rt.wikiIdx != nil && rt.wikiStore != nil {
		input.Extra = &orchestrator.ResearchDeps{
			WikiIndex:     rt.wikiIdx,
			WikiStore:     rt.wikiStore,
			UsageTracker:  rt.usageTracker,
			LearningStore: rt.learningStore,
			SelfState:     rt.selfState,
			MemoryGate:    agenticmemory.NewGate(rt.agenticStore),
			AgenticTaskID: agenticTaskID,
			Redact:        rt.learningRedactor,
			MaxRounds:     rt.researchMaxRounds,
			CostCapUSD:    rt.researchCostCapUSD,
		}
	}

	wfStart := time.Now()
	result, err := wf.Run(ctx, input)
	elapsed := time.Since(wfStart)
	if err != nil {
		rt.recordOutcome(ctx, outcomeInput{
			agenticTaskID:  agenticTaskID,
			routeCtx:       routeCtx,
			intent:         intent,
			workflow:       wf.Name(),
			finishReason:   "error",
			success:        false,
			elapsed:        elapsed,
			userInput:      userInput,
			preferenceUsed: pref != nil,
			sessionID:      sess.ID,
			maxIterations:  rt.wfCfg.MaxIterations,
		})
		return nil, "", fmt.Errorf("workflow %s: %w", wf.Name(), err)
	}

	completionSummary := withProviderCapabilities(summarizeCompletionContract(routeCtx, cfg, result), rt.provider)
	result, completionSummary = rt.maybeRunCompletionRetry(ctx, wf, input, result, completionSummary)
	if hasAgenticTask {
		rt.rememberAgenticCompletionContext(agenticTaskID, completionSummary)
	}
	if learning.ShouldRecord(result.FinishReason) {
		rt.recordOutcome(ctx, outcomeInput{
			agenticTaskID:  agenticTaskID,
			routeCtx:       routeCtx,
			intent:         intent,
			workflow:       result.Workflow,
			finishReason:   result.FinishReason,
			success:        learning.IsSuccessful(result.FinishReason),
			elapsed:        elapsed,
			iterations:     result.Iterations,
			userInput:      userInput,
			preferenceUsed: pref != nil,
			sessionID:      sess.ID,
			maxIterations:  rt.wfCfg.MaxIterations,
			inputTokens:    result.Usage.InputTokens,
			outputTokens:   result.Usage.OutputTokens,
			toolStats:      result.ToolStats,
			completion:     completionSummary,
		})
	}

	calls, errs := summarizeToolUses(result.ToolStats)
	if usage := llm.FormatUsageSummary(rt.wfCfg.Model, result.Usage, calls, errs); usage != "" {
		bus.Emit(event.UsageProgressEvent{Base: event.NewBase(), Summary: usage})
	}

	if err := sess.AppendMessages(result.Messages[len(prepared):]); err != nil {
		rt.app.Logger.Warn("session persist failed", "error", err)
	}

	return result.Messages, result.Summary, nil
}

func (rt *executionRuntime) toolContextForSession(ctx context.Context, sess *agent.Session) context.Context {
	if sess == nil {
		return ctx
	}
	ctx = tools.WithSessionID(ctx, sess.ID)
	if benchmarkModeEnabled() {
		if dir := benchmarkEnvDir(); dir != "" {
			ctx = tools.WithSessionEnvDir(ctx, dir)
		}
		return tools.WithRootSessionWorkDir(ctx)
	}
	return ctx
}

func resolveRuntimeScheduledTasksPath(cfg *config.Config) string {
	if cfg == nil {
		return "scheduled_tasks.yaml"
	}
	path := strings.TrimSpace(cfg.Daemon.ScheduledTasksPath)
	if path == "" {
		path = "scheduled_tasks.yaml"
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cfg.DataDir, path)
}

// sessionRenderWorkDir returns the cwd path advertised to the LLM through
// prompt nodes. It points at the per-session workspace subdir so the model
// does not learn the shared root path; otherwise the LLM would stamp the
// root into bash working_dir overrides and defeat session isolation
// (FU-WorkspaceScope dogfood probe #335). Benchmark mode uses rt.workDir so
// probe workspaces stay rooted at the cloned target repo and do not create
// Elnath session dirs inside it. Falls back to rt.workDir on any guard / mkdir
// failure so prompt rendering never aborts a turn.
func (rt *executionRuntime) sessionRenderWorkDir(sess *agent.Session) string {
	if rt == nil || rt.guard == nil || sess == nil {
		if rt != nil {
			return rt.workDir
		}
		return ""
	}
	if benchmarkModeEnabled() {
		return rt.workDir
	}
	dir, err := rt.guard.EnsureSessionWorkDir(sess.ID)
	if err != nil {
		rt.app.Logger.Warn("session render workdir ensure failed",
			"session", sess.ID,
			"error", err,
		)
		return rt.workDir
	}
	return dir
}

// maybePurgeSessionWorkspace removes the per-session workspace subdir after a
// daemon task finishes. Default policy is "immediate" — the dir is wiped to
// prevent cross-session contamination and unbounded disk growth. Setting
// cfg.Daemon.WorkspaceRetention to "keep" disables the purge so a follow-up
// task resuming the same sessionID can still see prior tool artifacts.
// Failures are logged, never returned: cleanup must not mask the task result.
func (rt *executionRuntime) maybePurgeSessionWorkspace(sess *agent.Session) {
	if rt == nil || rt.guard == nil || sess == nil {
		return
	}
	retention := strings.TrimSpace(rt.app.Config.Daemon.WorkspaceRetention)
	if retention != "" && retention != "immediate" {
		return
	}
	if err := rt.guard.PurgeSessionWorkDir(sess.ID); err != nil {
		rt.app.Logger.Warn("session workspace purge failed",
			"session", sess.ID,
			"error", err,
		)
	}
}

// recordOutcome appends a learning outcome and, on success, asks the routing
// advisor for an updated preference. Safe to call with a nil outcomeStore or an
// empty ProjectID; both make the call a no-op so error paths stay cheap.
// outcomeInput aggregates the fields recordOutcome writes to outcomes.jsonl.
// Using a struct keeps the arg list manageable now that the P3 learning-
// observability extension adds session/usage/tool telemetry.
type outcomeInput struct {
	agenticTaskID  int64
	routeCtx       *orchestrator.RoutingContext
	intent         conversation.Intent
	workflow       string
	finishReason   string
	success        bool
	elapsed        time.Duration
	iterations     int
	userInput      string
	preferenceUsed bool
	sessionID      string
	maxIterations  int
	inputTokens    int
	outputTokens   int
	toolStats      []agent.ToolStat
	completion     completionContractSummary
}

func (rt *executionRuntime) recordOutcome(ctx context.Context, in outcomeInput) {
	if rt.outcomeStore == nil || in.routeCtx == nil || in.routeCtx.ProjectID == "" {
		return
	}
	if in.agenticTaskID != 0 && !rt.agenticOutcomeVerified(ctx, in.agenticTaskID) {
		return
	}
	record := learning.OutcomeRecord{
		ProjectID:                in.routeCtx.ProjectID,
		Intent:                   string(in.intent),
		Workflow:                 in.workflow,
		FinishReason:             in.finishReason,
		Success:                  in.success,
		Duration:                 in.elapsed.Seconds(),
		Cost:                     0,
		Iterations:               in.iterations,
		InputSnippet:             runeSnippet(in.userInput, 100),
		EstimatedFiles:           in.routeCtx.EstimatedFiles,
		ExistingCode:             in.routeCtx.ExistingCode,
		PreferenceUsed:           in.preferenceUsed,
		SessionID:                in.sessionID,
		MaxIterations:            in.maxIterations,
		InputTokens:              in.inputTokens,
		OutputTokens:             in.outputTokens,
		ToolStats:                agentToolStatsToLearning(in.toolStats),
		VerificationHint:         in.completion.VerificationHint,
		VerificationObserved:     in.completion.VerificationObserved,
		VerificationCommand:      in.completion.VerificationCommand,
		VerificationClass:        in.completion.VerificationClass,
		VerificationOwnership:    in.completion.VerificationOwnership,
		CompletionWarning:        in.completion.CompletionWarning,
		UserInputRequired:        in.completion.UserInputRequired,
		ReasoningEffort:          in.completion.ReasoningEffort,
		ReasoningEffortMode:      in.completion.ReasoningEffortMode,
		ReasoningEffortReason:    in.completion.ReasoningEffortReason,
		ProviderName:             in.completion.ProviderName,
		ProviderEffort:           in.completion.ProviderEffort,
		ProviderEffortNote:       in.completion.ProviderEffortNote,
		LoadedDeferredTools:      append([]string(nil), in.completion.LoadedDeferredTools...),
		SkillCatalogReceipts:     completionSkillCatalogReceiptsToLearning(in.completion.SkillCatalogReceipts),
		SkillExecutionReceipts:   completionSkillExecutionReceiptsToLearning(in.completion.SkillExecutionReceipts),
		CommandCatalogReceipts:   completionCommandCatalogReceiptsToLearning(in.completion.CommandCatalogReceipts),
		ShellCommandReceipts:     completionShellCommandReceiptsToLearning(in.completion.ShellCommandReceipts),
		ToolSearchReceipts:       completionToolSearchReceiptsToLearning(in.completion.ToolSearchReceipts),
		ControlToolReceipts:      completionControlToolReceiptsToLearning(in.completion.ControlToolReceipts),
		ConditionalSkillMatches:  completionSkillMatchesToLearning(in.completion.ConditionalSkillMatches),
		CorrectionAttempted:      in.completion.CorrectionAttempted,
		CorrectionAttempts:       in.completion.CorrectionAttempts,
		CorrectionMaxAttempts:    in.completion.CorrectionMaxAttempts,
		CorrectionDecision:       in.completion.CorrectionDecision,
		CorrectionReason:         in.completion.CorrectionReason,
		CorrectionStatus:         in.completion.CorrectionStatus,
		CorrectionFailureFamily:  in.completion.CorrectionFailureFamily,
		CorrectionAttemptDetails: completionCorrectionAttemptDetailsToLearning(in.completion.CorrectionAttemptDetails),
		RetryDecision:            in.completion.RetryDecision,
		RetryReason:              in.completion.RetryReason,
		RecoveryScopeLabel:       in.completion.RecoveryScopeLabel,
		AllowedRecoveryPaths:     append([]string(nil), in.completion.AllowedRecoveryPaths...),
		ForbiddenRecoveryPaths:   append([]string(nil), in.completion.ForbiddenRecoveryPaths...),
		MutatedPaths:             append([]string(nil), in.completion.MutatedPaths...),
		OutOfScopeChangedFiles:   append([]string(nil), in.completion.OutOfScopeChangedFiles...),
	}
	if appendErr := rt.outcomeStore.Append(record); appendErr != nil {
		rt.app.Logger.Warn("outcome store: append failed", "error", appendErr)
	}
	if rotErr := rt.outcomeStore.AutoRotateIfNeeded(300); rotErr != nil {
		rt.app.Logger.Warn("outcome store: auto-rotate failed", "error", rotErr)
	}
	if !in.success {
		return
	}
	if rt.routingAdvisor == nil {
		return
	}
	advPref, advErr := rt.routingAdvisor.Advise(in.routeCtx.ProjectID)
	if advErr != nil || advPref == nil {
		return
	}
	if saveErr := wiki.SaveWorkflowPreference(rt.wikiStore, in.routeCtx.ProjectID, advPref); saveErr != nil {
		rt.app.Logger.Warn("routing advisor: wiki save failed", "error", saveErr)
	}
}

func completionCorrectionAttemptDetailsToLearning(src []completionCorrectionAttemptReceipt) []learning.CorrectionAttemptReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]learning.CorrectionAttemptReceipt, 0, len(src))
	for _, detail := range src {
		out = append(out, learning.CorrectionAttemptReceipt{
			Attempt:             detail.Attempt,
			Decision:            detail.Decision,
			Reason:              detail.Reason,
			Status:              detail.Status,
			FailureFamily:       detail.FailureFamily,
			VerificationCommand: detail.VerificationCommand,
			CompletionWarning:   detail.CompletionWarning,
			ChangedFiles:        append([]string(nil), detail.ChangedFiles...),
			OutOfScopeFiles:     append([]string(nil), detail.OutOfScopeFiles...),
		})
	}
	return out
}

func completionSkillMatchesToLearning(src []completionConditionalSkillMatch) []learning.ConditionalSkillMatch {
	if len(src) == 0 {
		return nil
	}
	out := make([]learning.ConditionalSkillMatch, 0, len(src))
	for _, match := range src {
		out = append(out, learning.ConditionalSkillMatch{
			SkillName:  match.SkillName,
			Pattern:    match.Pattern,
			Path:       match.Path,
			Source:     match.Source,
			TrustLevel: match.TrustLevel,
			External:   match.External,
		})
	}
	return out
}

func completionSkillCatalogReceiptsToLearning(src []completionSkillCatalogReceipt) []learning.SkillCatalogReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]learning.SkillCatalogReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, learning.SkillCatalogReceipt{
			Tool:               receipt.Tool,
			Action:             receipt.Action,
			ReadOnly:           receipt.ReadOnly,
			RegistryAvailable:  receipt.RegistryAvailable,
			TotalSkills:        receipt.TotalSkills,
			ReturnedSkills:     receipt.ReturnedSkills,
			ReturnedMatches:    receipt.ReturnedMatches,
			TrustFilterApplied: receipt.TrustFilterApplied,
			AllowTrustLevels:   append([]string(nil), receipt.AllowTrustLevels...),
			MaxResults:         receipt.MaxResults,
			Query:              receipt.Query,
			Skill:              receipt.Skill,
			PathCount:          receipt.PathCount,
			CWDSet:             receipt.CWDSet,
			IncludePrompt:      receipt.IncludePrompt,
		})
	}
	return out
}

func completionSkillExecutionReceiptsToLearning(src []completionSkillExecutionReceipt) []learning.SkillExecutionReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]learning.SkillExecutionReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, learning.SkillExecutionReceipt{
			Tool:                receipt.Tool,
			Action:              receipt.Action,
			Skill:               receipt.Skill,
			Status:              receipt.Status,
			Provider:            receipt.Provider,
			Model:               receipt.Model,
			ReasoningEffort:     receipt.ReasoningEffort,
			ReasoningEffortMode: receipt.ReasoningEffortMode,
			PermissionMode:      receipt.PermissionMode,
			MaxIterations:       receipt.MaxIterations,
			RequiredTools:       append([]string(nil), receipt.RequiredTools...),
			AvailableTools:      append([]string(nil), receipt.AvailableTools...),
			ToolFilterApplied:   receipt.ToolFilterApplied,
			BaseDir:             receipt.BaseDir,
			Source:              receipt.Source,
			TrustLevel:          receipt.TrustLevel,
			External:            receipt.External,
			UserInvocable:       receipt.UserInvocable,
		})
	}
	return out
}

func completionCommandCatalogReceiptsToLearning(src []completionCommandCatalogReceipt) []learning.CommandCatalogReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]learning.CommandCatalogReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, learning.CommandCatalogReceipt{
			Tool:                  receipt.Tool,
			Action:                receipt.Action,
			ReadOnly:              receipt.ReadOnly,
			RegistryAvailable:     receipt.RegistryAvailable,
			ExecutionAvailable:    receipt.ExecutionAvailable,
			ExecutionPolicy:       receipt.ExecutionPolicy,
			TotalCommands:         receipt.TotalCommands,
			ReturnedCommands:      receipt.ReturnedCommands,
			ExecutableCommands:    receipt.ExecutableCommands,
			ModelCallableCommands: receipt.ModelCallableCommands,
			IncludeHidden:         receipt.IncludeHidden,
			MaxResults:            receipt.MaxResults,
			Query:                 receipt.Query,
			Command:               receipt.Command,
			FollowupTool:          receipt.FollowupTool,
		})
	}
	return out
}

func completionShellCommandReceiptsToLearning(src []completionShellCommandReceipt) []learning.ShellCommandReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]learning.ShellCommandReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, learning.ShellCommandReceipt{
			Tool:                  receipt.Tool,
			Action:                receipt.Action,
			ExecutionPolicy:       receipt.ExecutionPolicy,
			CommandIntent:         receipt.CommandIntent,
			IntentSource:          receipt.IntentSource,
			CommandClass:          receipt.CommandClass,
			Status:                receipt.Status,
			Classification:        receipt.Classification,
			TimedOut:              receipt.TimedOut,
			Canceled:              receipt.Canceled,
			IsError:               receipt.IsError,
			TimeoutMS:             receipt.TimeoutMS,
			WorkingDirSet:         receipt.WorkingDirSet,
			CommandLen:            receipt.CommandLen,
			BackgroundRecommended: receipt.BackgroundRecommended,
		})
	}
	return out
}

func completionToolSearchReceiptsToLearning(src []completionToolSearchReceipt) []learning.ToolSearchReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]learning.ToolSearchReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, learning.ToolSearchReceipt{
			Tool:               receipt.Tool,
			Action:             receipt.Action,
			ReadOnly:           receipt.ReadOnly,
			RegistryAvailable:  receipt.RegistryAvailable,
			ExecutionAvailable: receipt.ExecutionAvailable,
			ExecutionPolicy:    receipt.ExecutionPolicy,
			TotalTools:         receipt.TotalTools,
			ReturnedMatches:    receipt.ReturnedMatches,
			DeferredMatches:    receipt.DeferredMatches,
			MaxResults:         receipt.MaxResults,
			AllowNamesCount:    receipt.AllowNamesCount,
			Query:              receipt.Query,
		})
	}
	return out
}

func completionControlToolReceiptsToLearning(src []completionControlToolReceipt) []learning.ControlToolReceipt {
	if len(src) == 0 {
		return nil
	}
	out := make([]learning.ControlToolReceipt, 0, len(src))
	for _, receipt := range src {
		out = append(out, learning.ControlToolReceipt{
			Tool:                    receipt.Tool,
			Action:                  receipt.Action,
			ReadOnly:                receipt.ReadOnly,
			Persistent:              receipt.Persistent,
			RequestID:               receipt.RequestID,
			SessionID:               receipt.SessionID,
			QueueBacked:             receipt.QueueBacked,
			RegistryBacked:          receipt.RegistryBacked,
			ExecutionAvailable:      receipt.ExecutionAvailable,
			ExecutionPolicy:         receipt.ExecutionPolicy,
			CommandIntent:           receipt.CommandIntent,
			IntentSource:            receipt.IntentSource,
			FollowupTool:            receipt.FollowupTool,
			TaskID:                  receipt.TaskID,
			ParentTaskID:            receipt.ParentTaskID,
			ChildTaskID:             receipt.ChildTaskID,
			QueueTaskID:             receipt.QueueTaskID,
			ProcessID:               receipt.ProcessID,
			DecisionID:              receipt.DecisionID,
			DecisionStatus:          receipt.DecisionStatus,
			Status:                  receipt.Status,
			PreviousStatus:          receipt.PreviousStatus,
			Terminal:                receipt.Terminal,
			TimedOut:                receipt.TimedOut,
			ExitCode:                receipt.ExitCode,
			Found:                   receipt.Found,
			TimeoutMS:               receipt.TimeoutMS,
			WaitMS:                  receipt.WaitMS,
			WaitElapsedMS:           receipt.WaitElapsedMS,
			WaitTimedOut:            receipt.WaitTimedOut,
			WatchText:               receipt.WatchText,
			WatchMatched:            receipt.WatchMatched,
			WatchStream:             receipt.WatchStream,
			CWD:                     receipt.CWD,
			TailBytes:               receipt.TailBytes,
			StdoutRawBytes:          receipt.StdoutRawBytes,
			StderrRawBytes:          receipt.StderrRawBytes,
			StdoutTruncated:         receipt.StdoutTruncated,
			StderrTruncated:         receipt.StderrTruncated,
			StopSignal:              receipt.StopSignal,
			EdgeType:                receipt.EdgeType,
			Enqueued:                receipt.Enqueued,
			Deduplicated:            receipt.Deduplicated,
			TotalReturned:           receipt.TotalReturned,
			Limit:                   receipt.Limit,
			Field:                   receipt.Field,
			RetrievalStatus:         receipt.RetrievalStatus,
			MaxChars:                receipt.MaxChars,
			TotalChars:              receipt.TotalChars,
			Truncated:               receipt.Truncated,
			Name:                    receipt.Name,
			Path:                    receipt.Path,
			Branch:                  receipt.Branch,
			RegistryPath:            receipt.RegistryPath,
			Runner:                  receipt.Runner,
			IsError:                 receipt.IsError,
			Removed:                 receipt.Removed,
			DryRun:                  receipt.DryRun,
			Total:                   receipt.Total,
			TaskName:                receipt.TaskName,
			TaskCountBefore:         receipt.TaskCountBefore,
			TaskCountAfter:          receipt.TaskCountAfter,
			PreviousMode:            receipt.PreviousMode,
			CurrentMode:             receipt.CurrentMode,
			Restored:                receipt.Restored,
			ReadOnlyAfterTransition: receipt.ReadOnlyAfterTransition,
			FromActorID:             receipt.FromActorID,
			ToActorID:               receipt.ToActorID,
			ActorID:                 receipt.ActorID,
			HandoffID:               receipt.HandoffID,
			Box:                     receipt.Box,
			Delivered:               receipt.Delivered,
			Command:                 receipt.Command,
			Args:                    append([]string(nil), receipt.Args...),
			StateMutation:           receipt.StateMutation,
			Question:                receipt.Question,
			QuestionChars:           receipt.QuestionChars,
			AnswerChars:             receipt.AnswerChars,
			OptionCount:             receipt.OptionCount,
			AllowFreeText:           receipt.AllowFreeText,
			TimeoutSeconds:          receipt.TimeoutSeconds,
		})
	}
	return out
}

func (rt *executionRuntime) agenticOutcomeVerified(ctx context.Context, taskID int64) bool {
	if taskID == 0 {
		return true
	}
	if rt.agenticStore == nil {
		rt.app.Logger.Warn("agentic outcome skipped: missing agentic store", "task_id", taskID)
		return false
	}
	runs, err := rt.agenticStore.ListVerificationRunsByTask(ctx, taskID)
	if err != nil {
		rt.app.Logger.Warn("agentic outcome skipped: verification lookup failed", "task_id", taskID, "error", err)
		return false
	}
	var latest *agentic.VerificationRun
	for i := range runs {
		run := runs[i]
		if latest == nil || run.ID > latest.ID {
			latest = &run
		}
	}
	if latest == nil {
		rt.app.Logger.Warn("agentic outcome skipped: missing verification run", "task_id", taskID)
		return false
	}
	if latest.Verdict != agentic.VerificationVerdictPassed {
		rt.app.Logger.Warn("agentic outcome skipped: verification not passed", "task_id", taskID, "verification_run_id", latest.ID, "verdict", latest.Verdict)
		return false
	}
	return true
}

// agentToolStatsToLearning converts the agent-level tool-stat type into the
// learning-package type stored in outcomes.jsonl. Returns nil when the input
// has no entries so the JSON encoder honors omitempty on the outcome field.
func agentToolStatsToLearning(src []agent.ToolStat) []learning.AgentToolStat {
	if len(src) == 0 {
		return nil
	}
	dst := make([]learning.AgentToolStat, len(src))
	for i, s := range src {
		dst[i] = learning.AgentToolStat{
			Name:      s.Name,
			Calls:     s.Calls,
			Errors:    s.Errors,
			TotalTime: s.TotalTime,
		}
	}
	return dst
}

// recordSetupOutcome logs a failure outcome for daemon-task setup errors
// (session load rejection, resume record failure, session creation failure)
// that abort before a routing decision is made. Without this, such failures
// would be invisible to the routing advisor and to `elnath explain last`.
func (rt *executionRuntime) recordSetupOutcome(
	ctx context.Context,
	p identity.Principal,
	userInput string,
	finishReason string,
	elapsed time.Duration,
) {
	if rt.outcomeStore == nil || p.ProjectID == "" {
		return
	}
	if agenticTaskID, ok := daemon.AgenticTaskIDFromContext(ctx); ok && !rt.agenticOutcomeVerified(ctx, agenticTaskID) {
		return
	}
	record := learning.OutcomeRecord{
		ProjectID:    p.ProjectID,
		FinishReason: finishReason,
		Success:      false,
		Duration:     elapsed.Seconds(),
		InputSnippet: runeSnippet(userInput, 100),
	}
	if err := rt.outcomeStore.Append(record); err != nil {
		rt.app.Logger.Warn("outcome store: setup append failed", "error", err)
	}
}

// skillPromptPipeline adapts prompt.Builder + session-scoped RenderState
// to the skill.PromptPrefixRenderer contract. The skill package cannot
// import internal/prompt (SkillCatalogNode already pulls prompt → skill),
// so runtime materialises the RenderState here on behalf of each skill
// invocation.
type skillPromptPipeline struct {
	builder      *prompt.Builder
	self         *self.SelfState
	wikiIdx      *wiki.Index
	personaExtra string
	providerName string
	model        string
	workDir      string
	daemonMode   bool
	principal    identity.Principal
}

func (p *skillPromptPipeline) RenderPromptPrefix(ctx context.Context, inv skill.SkillInvocation) (string, error) {
	if p == nil || p.builder == nil {
		return "", nil
	}
	state := &prompt.RenderState{
		SessionID:    inv.SessionID,
		UserInput:    inv.UserInput,
		Self:         p.self,
		Principal:    p.principal,
		WikiIdx:      p.wikiIdx,
		PersonaExtra: p.personaExtra,
		Model:        p.model,
		Provider:     p.providerName,
		WorkDir:      p.workDir,
		DaemonMode:   p.daemonMode,
	}
	return p.builder.Build(ctx, state)
}

// researchPromptPipeline adapts prompt.Builder + session-scoped RenderState
// to the research.PromptPrefixRenderer contract. The research package
// cannot import internal/prompt (the prompt builder pulls research-adjacent
// nodes), so runtime materialises the RenderState here on behalf of each
// stage invocation. Phase 7.1 GAP-RESEARCH-01.
type researchPromptPipeline struct {
	builder      *prompt.Builder
	self         *self.SelfState
	wikiIdx      *wiki.Index
	personaExtra string
	providerName string
	model        string
	workDir      string
	daemonMode   bool
	principal    identity.Principal
}

func (p *researchPromptPipeline) RenderPromptPrefix(ctx context.Context, inv research.Invocation) (string, error) {
	if p == nil || p.builder == nil {
		return "", nil
	}
	state := &prompt.RenderState{
		SessionID:    inv.SessionID,
		UserInput:    inv.UserInput,
		Self:         p.self,
		Principal:    p.principal,
		WikiIdx:      p.wikiIdx,
		PersonaExtra: p.personaExtra,
		Model:        p.model,
		Provider:     p.providerName,
		WorkDir:      p.workDir,
		DaemonMode:   p.daemonMode,
	}
	return p.builder.Build(ctx, state)
}

func (rt *executionRuntime) newResearchPromptPipeline() *researchPromptPipeline {
	return &researchPromptPipeline{
		builder:      rt.promptBuilder,
		self:         rt.selfState,
		wikiIdx:      rt.wikiIdx,
		personaExtra: rt.personaExtra,
		providerName: rt.provider.Name(),
		model:        rt.wfCfg.Model,
		workDir:      rt.workDir,
		daemonMode:   rt.daemonMode,
		principal:    rt.principal,
	}
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
	sk, ok := runtimeSkillBySlashToken(rt.skillReg, fields[0])
	if !ok {
		return nil, "", false, nil
	}
	skillName := sk.Name
	if !sk.UserInvocable() {
		return nil, "", true, fmt.Errorf("skill %q is not user-invocable", skillName)
	}

	args := skillInvocationArgs(sk, fields[1:])

	rt.app.Logger.Info("executing skill", "name", skillName, "args", args)
	bus.Emit(event.TextDeltaEvent{Base: event.NewBase(), Content: fmt.Sprintf("Executing skill: %s\n", skillName)})

	pipeline := &skillPromptPipeline{
		builder:      rt.promptBuilder,
		self:         rt.selfState,
		wikiIdx:      rt.wikiIdx,
		personaExtra: rt.personaExtra,
		providerName: rt.provider.Name(),
		model:        rt.wfCfg.Model,
		workDir:      rt.workDir,
		daemonMode:   rt.daemonMode,
		principal:    rt.principal,
	}

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
		Pipeline:   pipeline,
		SessionID:  sess.ID,
		UserInput:  input,
	})
	if err != nil {
		rt.recordSkillUsage(sess.ID, skillName, false)
		return nil, "", true, fmt.Errorf("skill %q: %w", skillName, err)
	}
	rt.recordSkillUsage(sess.ID, skillName, true)
	// skill.ExecuteResult does not currently surface tool aggregates; pass
	// zero counts so the tools segment is omitted (no false-zero claim).
	if usage := llm.FormatUsageSummary(rt.wfCfg.Model, result.Usage, 0, 0); usage != "" {
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

func runtimeSkillBySlashToken(reg *skill.Registry, token string) (*skill.Skill, bool) {
	if reg == nil {
		return nil, false
	}
	name := strings.TrimPrefix(strings.TrimSpace(token), "/")
	if name == "" {
		return nil, false
	}
	if sk, ok := reg.Get(name); ok {
		return sk, true
	}
	slashToken := "/" + name
	for _, sk := range reg.List() {
		triggerFields := strings.Fields(strings.TrimSpace(sk.Trigger))
		if len(triggerFields) == 0 {
			continue
		}
		if triggerFields[0] == slashToken {
			return sk, true
		}
	}
	return nil, false
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

func skillInvocationArgs(sk *skill.Skill, values []string) map[string]string {
	args := make(map[string]string)
	if sk == nil {
		return args
	}
	for key, value := range parseSkillArgs(sk.Trigger, values) {
		args[key] = value
	}
	for key, value := range parseSkillNamedArgs(sk.ArgumentNames, values) {
		args[key] = value
	}
	if raw := strings.Join(values, " "); raw != "" {
		args["arguments"] = raw
		args["ARGUMENTS"] = raw
		args["args"] = raw
	}
	return args
}

func parseSkillNamedArgs(names []string, values []string) map[string]string {
	args := make(map[string]string)
	idx := 0
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if idx >= len(values) {
			args[name] = ""
			continue
		}
		if i == len(names)-1 {
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

func benchmarkEnvDir() string {
	if dir := strings.TrimSpace(os.Getenv("ELNATH_BENCHMARK_ENV_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("ELNATH_DATA_DIR")); dir != "" {
		return filepath.Join(dir, "benchmark-env")
	}
	return ""
}

func taskLanguageFromEnv() string {
	return os.Getenv("ELNATH_TASK_LANGUAGE")
}

func runtimeCorrectionScopeFromEnv() orchestrator.CorrectionScope {
	return orchestrator.CorrectionScope{
		Label:          strings.TrimSpace(os.Getenv("ELNATH_CORRECTION_SCOPE_LABEL")),
		AllowedPaths:   splitEnvPathList(os.Getenv("ELNATH_CORRECTION_SCOPE_ALLOWED_PATHS")),
		ForbiddenPaths: splitEnvPathList(os.Getenv("ELNATH_CORRECTION_SCOPE_FORBIDDEN_PATHS")),
	}
}

func runtimeVerificationPolicyFromEnv() orchestrator.VerificationPolicy {
	return orchestrator.VerificationPolicy{
		Class:     strings.TrimSpace(os.Getenv("ELNATH_VERIFICATION_CLASS")),
		Ownership: strings.TrimSpace(os.Getenv("ELNATH_VERIFICATION_OWNERSHIP")),
	}
}

func splitEnvPathList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
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
		start := time.Now()
		taskPayload := daemon.ParseTaskPayload(payload)
		userInput := taskPayload.Prompt
		if userInput == "" {
			return daemon.TaskResult{}, fmt.Errorf("daemon task payload is empty")
		}
		preparedCtx, prepErr := rt.prepareDaemonAgenticEnforcement(ctx, taskPayload)
		if prepErr != nil {
			return daemon.TaskResult{}, prepErr
		}
		ctx = preparedCtx
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
				rt.recordSetupOutcome(ctx, principal, userInput, "load_session_failed", time.Since(start))
				return daemon.TaskResult{}, fmt.Errorf("load session %s: %w", taskPayload.SessionID, err)
			}
			if principal.IsZero() {
				principal = sess.Principal
			}
			if err := sess.RecordResume(principal); err != nil {
				rt.recordSetupOutcome(ctx, principal, userInput, "record_resume_failed", time.Since(start))
				return daemon.TaskResult{}, fmt.Errorf("record resume %s: %w", taskPayload.SessionID, err)
			}
			messages = sess.Messages
		} else {
			if principal.IsZero() {
				principal = identity.LegacyPrincipal()
			}
			sess, err = rt.mgr.NewSessionWithPrincipal(principal)
			if err != nil {
				rt.recordSetupOutcome(ctx, principal, userInput, "create_session_failed", time.Since(start))
				return daemon.TaskResult{}, fmt.Errorf("create session: %w", err)
			}
		}

		defer rt.maybePurgeSessionWorkspace(sess)

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

func (rt *executionRuntime) prepareDaemonAgenticEnforcement(ctx context.Context, payload daemon.TaskPayload) (context.Context, error) {
	mode := strings.ToLower(strings.TrimSpace(payload.AgenticEnforcement))
	if mode == "" {
		return ctx, nil
	}
	if mode != config.AgenticEnforcementModeGateway {
		return ctx, fmt.Errorf("unsupported agentic enforcement mode %q", mode)
	}
	if !agenticGatewayConfigPermitted(rt.app.Config) {
		return ctx, fmt.Errorf("agentic gateway enforcement requested but config maximum is %q", agenticEnforcementConfigMode(rt.app.Config))
	}
	taskID, ok := daemon.AgenticTaskIDFromContext(ctx)
	if !ok || taskID == 0 {
		return ctx, fmt.Errorf("agentic gateway enforcement requested but agentic task id is required")
	}
	return withAgenticRuntimeEnforcement(ctx, mode), nil
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
