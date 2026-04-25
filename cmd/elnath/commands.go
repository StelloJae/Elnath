package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/llm/promptcache"
	"github.com/stello/elnath/internal/mcp"
	"github.com/stello/elnath/internal/onboarding"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/userfacingerr"
	"github.com/stello/elnath/internal/wiki"
)

type commandRunner func(ctx context.Context, args []string) error

func commandRegistry() map[string]commandRunner {
	return map[string]commandRunner{
		"version":     cmdVersion,
		"help":        cmdHelp,
		"chaos":       cmdChaos,
		"run":         cmdRun,
		"setup":       cmdSetup,
		"errors":      cmdErrors,
		"daemon":      cmdDaemon,
		"portability": cmdPortability,
		"research":    cmdResearch,
		"telegram":    cmdTelegram,
		"wiki":        cmdWiki,
		"search":      cmdSearch,
		"eval":        cmdEval,
		"task":        cmdTask,
		"lessons":     cmdLessons,
		"skill":       cmdSkill,
		"profile":     cmdProfile,
		"explain":     cmdExplain,
		"debug":       cmdDebug,
		// Hidden internal exec mode for the v41 / B3b-4-S0 Linux
		// netns bridge spike. Not user-facing; the integration test
		// at internal/tools/netproxy_bridge_spike_linux_test.go is
		// the only caller. Omitted from `elnath help` on purpose.
		"netproxy-bridge-spike": cmdNetproxyBridgeSpike,
	}
}

func executeCommand(ctx context.Context, name string, args []string) error {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		return printCommandHelp(name)
	}
	registry := commandRegistry()
	cmd, ok := registry[name]
	if !ok {
		fmt.Fprintln(os.Stderr, fmt.Sprintf(onboarding.T(loadLocale(), "cli.unknown_command"), name))
		return cmdHelp(ctx, args)
	}
	return cmd(ctx, args)
}

func printCommandHelp(name string) error {
	if name == "errors" {
		return printErrorsHelp()
	}
	text := onboarding.TOptional(loadLocale(), "cmd."+name+".help")
	if text == "" {
		return cmdHelp(nil, nil)
	}
	fmt.Println(text)
	return nil
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
// Cached via sync.Once to avoid re-parsing config on every help/error call.
var (
	cachedLocale     onboarding.Locale
	cachedLocaleOnce sync.Once
)

func loadLocale() onboarding.Locale {
	cachedLocaleOnce.Do(func() {
		cachedLocale = onboarding.En
		cfgPath := extractConfigFlag(os.Args)
		if cfgPath == "" {
			cfgPath = config.DefaultConfigPath()
		}
		if cfg, err := config.Load(cfgPath); err == nil && cfg.Locale != "" {
			cachedLocale = onboarding.Locale(cfg.Locale)
		}
	})
	return cachedLocale
}

// ---- helpers ----

// summarizeToolUses sums per-tool aggregates into single-turn totals
// (calls, errors) for surfacing in FormatUsageSummary. Defined here so
// both runtime.go execution paths share the same arithmetic.
func summarizeToolUses(stats []agent.ToolStat) (calls, errors int) {
	for _, s := range stats {
		calls += s.Calls
		errors += s.Errors
	}
	return
}

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
		if cfg.DataDir != "" {
			// Prompt-cache FileSink lands each turn's BreakReport at
			// <data-dir>/prompt-cache/<session-id>.jsonl so `elnath debug
			// prompt-cache --session=<id>` has real data to show. Active
			// only when the agent threads a non-empty SessionID through
			// ChatRequest (orchestrator.agentOptions wires this today).
			opts = append(opts, llm.WithAnthropicPromptCacheSink(promptcache.NewFileSink(cfg.DataDir)))
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

	// Codex OAuth provider (preferred — auto-refreshes tokens).
	if llm.CodexOAuthAvailable() {
		codexModel := loadCodexModel()
		reg.Register("codex", llm.NewCodexOAuthProvider(codexModel))
		if model == "" {
			model = codexModel
		}
	} else {
		// Fallback: use access_token as static API key (no refresh).
		codexToken, codexModel, codexAccountID := loadCodexAuth()
		if codexToken != "" {
			reg.Register("openai-responses", llm.NewResponsesProvider(codexToken, codexModel, codexAccountID))
			if model == "" {
				model = codexModel
			}
		}
	}

	if cfg.OpenAI.APIKey != "" {
		var opts []llm.OpenAIOption
		if cfg.OpenAI.BaseURL != "" {
			opts = append(opts, llm.WithOpenAIBaseURL(cfg.OpenAI.BaseURL))
		}
		m := cfg.OpenAI.Model
		if m == "" {
			m = resolveFallbackModel(cfg)
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
		inner := fmt.Errorf("no LLM provider configured: set ELNATH_ANTHROPIC_API_KEY or ELNATH_OPENAI_API_KEY")
		return nil, "", userfacingerr.Wrap(userfacingerr.ELN001, inner, "build provider")
	}

	canonical := llm.ResolveModel(model)
	detectedProvider := llm.DetectProvider(canonical)

	// Codex provider preferred over plain OpenAI for the same model names.
	if detectedProvider == "openai" {
		if p, err := reg.Get("codex"); err == nil {
			return p, canonical, nil
		}
		if p, err := reg.Get("openai-responses"); err == nil {
			return p, canonical, nil
		}
	}

	p, resolvedModel, err := reg.ForModel(model)
	if err != nil {
		return nil, "", err
	}
	return p, resolvedModel, nil
}

// defaultFallbackModel is the hardcoded fallback when neither the
// provider's Model field nor cfg.FallbackModel is set. Primary model
// per partner directive (2026-04-24) is gpt-5.5.
const defaultFallbackModel = "gpt-5.5"

// resolveFallbackModel returns the effective fallback model. Priority:
// cfg.FallbackModel → defaultFallbackModel. ELNATH_FALLBACK_MODEL is
// handled upstream by config.applyEnvOverrides, which populates
// cfg.FallbackModel before this call.
func resolveFallbackModel(cfg *config.Config) string {
	if cfg != nil && cfg.FallbackModel != "" {
		return cfg.FallbackModel
	}
	return defaultFallbackModel
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
		model = defaultFallbackModel
	}
	return auth.Tokens.AccessToken, model, accountID
}

// loadCodexModel reads the model from ~/.codex/config.toml, defaulting to o4-mini.
func loadCodexModel() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "o4-mini"
	}
	cfgData, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return "o4-mini"
	}
	for _, line := range strings.Split(string(cfgData), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "model") && !strings.HasPrefix(line, "model_") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), "\"")
			}
		}
	}
	return "o4-mini"
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
	if reg == nil || wikiDir == "" || wikiDB == nil {
		return nil, nil
	}
	idx, err := wiki.NewIndex(wikiDB)
	if err != nil {
		return nil, nil
	}
	store, err := wiki.NewStore(wikiDir, wiki.WithIndex(idx))
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

func buildToolRegistry(guard *tools.PathGuard, provider llm.Provider) *tools.Registry {
	reg := tools.NewRegistry()
	tracker := tools.NewReadTracker()
	reg.Register(tools.NewBashTool(guard))
	reg.Register(tools.NewReadTool(guard, tracker))
	reg.Register(tools.NewWriteTool(guard, tracker))
	reg.Register(tools.NewEditTool(guard, tracker))
	reg.Register(tools.NewGlobTool(guard))
	reg.Register(tools.NewGrepTool(guard, tracker))
	reg.Register(tools.NewGitTool(guard))
	reg.Register(tools.NewWebFetchTool(tools.WithSecondaryCaller(llm.NewSecondaryModelCaller(provider))))
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

// estimateFiles guesses how many files the user's request might touch
// based on simple heuristics (file-path-like tokens).
func estimateFiles(input string) int {
	count := 0
	for _, word := range strings.Fields(input) {
		if strings.Contains(word, "/") || strings.Contains(word, ".go") ||
			strings.Contains(word, ".ts") || strings.Contains(word, ".py") ||
			strings.Contains(word, ".js") || strings.Contains(word, ".yaml") {
			count++
		}
	}
	if count == 0 {
		count = 1 // default: assume single file
	}
	return count
}

// parseWorkflowPrefix detects anchored escape-hatch prefixes like
// "[ralph] ...", "[team] ...", "[single] ..." at the start of the prompt.
// Returns the chosen workflow (or "" if no prefix) and the cleaned prompt
// with the prefix stripped. Only prompt-start anchoring is supported —
// mid-sentence "[team]" does not match.
func parseWorkflowPrefix(input string) (workflow, cleaned string) {
	trimmed := strings.TrimLeft(input, " \t")
	prefixes := []struct {
		tag  string
		name string
	}{
		{"[ralph]", "ralph"},
		{"[team]", "team"},
		{"[single]", "single"},
	}
	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p.tag) {
			cleaned := strings.TrimLeft(strings.TrimPrefix(trimmed, p.tag), " \t")
			return p.name, cleaned
		}
	}
	return "", input
}

// TODO(phase-8-1b): replace phrase-matching with LLM-based intent classifier (Haiku).
// Phase 8.1a Fix 1 (GPT G2): phrase-aware matching. newWorkPhrases override
// VerificationHint only — ExistingCode is preserved as semantic truth so that
// "Add --json flag to existing CLI" remains ExistingCode=true while still
// routing to single via the raised team threshold.
// Phase 8.2 Fix 5: all phrase lookups run against orchestrator.NormalizeForPhraseMatch
// output so markdown-wrapped identifiers (backticked paths, names) still
// participate in the match.
func buildRoutingContext(input string) *orchestrator.RoutingContext {
	wf, cleaned := parseWorkflowPrefix(input)
	normalized := orchestrator.NormalizeForPhraseMatch(cleaned)

	existingCodeCues := []string{
		"existing", "current", "repo", "repository", "module", "handler", "middleware",
		"refactor", "fix", "bug",
		"runtime", "service", "worker", "command",
		// Note: "cli" removed — too short, false-positive on "ClientSession" etc.
		// Phase 8.1b LLM classifier will replace substring matching with
		// regex/word-boundary rules per GPT lap #8.
	}

	verificationPhrases := []string{
		"run tests", "run the test", "run the tests", "go test",
		"pytest", "npm test", "cargo test", "make test",
		"ensure tests pass", "ensure regression-free", "ensure regression",
		"verify this", "verify that", "regression check",
		"make sure tests pass", "make sure it passes",
		"check coverage", "check the regression", "run lint",
		"ci passes", "build succeeds", "go build",
		"tests still pass",
	}

	newWorkPhrases := []string{
		"write a test", "write a unit test", "write tests", "write unit tests",
		"write a reusable",
		"add a test", "add tests", "add a unit test",
		"add a --", "add a flag",
		"create a test", "create a unit test",
		"create a ", "generate a ", "scaffold",
		"include a test", "include a unit test", "include unit tests",
		"implement a", "author a", "author ci.yml", "author .github",
		"draft a", "draft a yaml", "draft .github",
		"new dockerfile", "new workflow", "set up workflow",
		"from scratch", "build a ", "build new", "make a ", "set up",
	}

	ctx := &orchestrator.RoutingContext{
		EstimatedFiles:   estimateFiles(cleaned),
		ExplicitWorkflow: wf,
	}
	for _, cue := range existingCodeCues {
		if strings.Contains(normalized, cue) {
			ctx.ExistingCode = true
			break
		}
	}
	for _, phrase := range verificationPhrases {
		if strings.Contains(normalized, phrase) {
			ctx.VerificationHint = true
			break
		}
	}
	// GPT G2: newWorkPhrases suppress VerificationHint only.
	// ExistingCode is NOT flipped — semantic truth is preserved so that
	// legitimate existing-code small tasks stay ExistingCode=true and ride
	// the raised team threshold (EstimatedFiles >= 4) to single.
	for _, phrase := range newWorkPhrases {
		if strings.Contains(normalized, phrase) {
			ctx.VerificationHint = false
			break
		}
	}
	if ctx.ExistingCode && ctx.VerificationHint && ctx.EstimatedFiles < 2 {
		ctx.EstimatedFiles = 2
	}
	return ctx
}

// cliPrompter asks the user for interactive permission approval.
type cliPrompter struct{}

func (p *cliPrompter) Prompt(_ context.Context, toolName string, _ json.RawMessage) (bool, error) {
	fmt.Printf("\nAllow tool %q? [y/N] ", toolName)
	var resp string
	fmt.Scanln(&resp)
	return strings.ToLower(strings.TrimSpace(resp)) == "y", nil
}
