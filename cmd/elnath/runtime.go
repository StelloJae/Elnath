package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	app       *core.App
	provider  llm.Provider
	mgr       *conversation.Manager
	router    *orchestrator.Router
	reg       *tools.Registry
	wfCfg     orchestrator.WorkflowConfig
	wikiIdx   *wiki.Index
	wikiStore *wiki.Store
	gitSync   *wiki.GitSync
	workDir   string
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
	systemPrompt string,
	perm *agent.Permission,
	workDir string,
	protectedPaths []string,
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
		workDir:   effectiveWorkDir,
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

	cfg := rt.wfCfg
	if rt.wikiIdx != nil {
		if ragCtx := wiki.BuildRAGContext(ctx, rt.wikiIdx, userInput, 3); ragCtx != "" {
			cfg.SystemPrompt += "\n\n" + ragCtx
		}
	}
	if routeCtx.ExistingCode {
		cfg.SystemPrompt += "\n\nBrownfield execution guidance:\n- Inspect existing files, tests, and nearby patterns before editing.\n- Keep scope bounded to the smallest correct change.\n- Prefer repo-native verification commands and reuse existing abstractions.\n- Ask the user only when missing information would materially change the outcome or the decision is costly to reverse."
		if hints := likelyRepoFiles(rt.workDir, userInput, 8); len(hints) > 0 {
			cfg.SystemPrompt += "\n- Likely relevant files:\n  - " + strings.Join(hints, "\n  - ")
		}
	}
	if routeCtx.VerificationHint {
		cfg.SystemPrompt += "\n- This task explicitly emphasizes verification or regression safety; prioritize proving the change with tests or repo-native checks."
	}

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
			sess, err = rt.mgr.NewSession()
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

func likelyRepoFiles(root, prompt string, limit int) []string {
	if root == "" || limit <= 0 {
		return nil
	}
	keywords := keywordHints(prompt)
	if len(keywords) == 0 {
		return nil
	}

	type candidate struct {
		path  string
		score int
	}
	var candidates []candidate
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == ".github" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		lower := strings.ToLower(rel)
		score := 0
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				score += 2
			}
		}
		if strings.HasPrefix(lower, "test/") || strings.HasPrefix(lower, "examples/") {
			score -= 2
		}
		if strings.Contains(lower, "/fixtures/") {
			score -= 2
		}
		if strings.Contains(lower, "/runtime/") || strings.Contains(lower, "/worker") || strings.Contains(lower, "/workers/") {
			score += 2
		}
		if strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".js") {
			score++
		}
		if score < 2 {
			contentScore := scoreFileContents(path, keywords)
			score += contentScore
		}
		if score > 0 {
			candidates = append(candidates, candidate{path: rel, score: score})
		}
		return nil
	})

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.path)
	}
	return out
}

func scoreFileContents(path string, keywords []string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if len(data) > 8192 {
		data = data[:8192]
	}
	lower := strings.ToLower(string(data))
	score := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			score++
		}
	}
	return score
}

func keywordHints(prompt string) []string {
	stop := map[string]struct{}{
		"the": {}, "and": {}, "with": {}, "into": {}, "without": {}, "existing": {}, "repository": {},
		"codebase": {}, "task": {}, "this": {}, "that": {}, "must": {}, "should": {}, "make": {},
		"smallest": {}, "correct": {}, "change": {}, "verify": {}, "verification": {}, "tests": {},
		"test": {}, "feature": {}, "brownfield": {}, "track": {}, "language": {}, "repo": {},
		"extend": {}, "current": {}, "behavior": {}, "regressing": {}, "emit": {}, "flow": {},
		"service": {},
	}
	fields := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	seen := map[string]struct{}{}
	var out []string
	for _, field := range fields {
		if len(field) < 4 {
			continue
		}
		if _, ok := stop[field]; ok {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
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
