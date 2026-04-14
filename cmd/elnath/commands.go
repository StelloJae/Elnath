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
	"github.com/stello/elnath/internal/mcp"
	"github.com/stello/elnath/internal/onboarding"
	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/tools"
	"github.com/stello/elnath/internal/wiki"
)

type commandRunner func(ctx context.Context, args []string) error

func commandRegistry() map[string]commandRunner {
	return map[string]commandRunner{
		"version":  cmdVersion,
		"help":     cmdHelp,
		"run":      cmdRun,
		"setup":    cmdSetup,
		"daemon":   cmdDaemon,
		"research": cmdResearch,
		"telegram": cmdTelegram,
		"wiki":     cmdWiki,
		"search":   cmdSearch,
		"eval":     cmdEval,
		"task":     cmdTask,
		"lessons":  cmdLessons,
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

func buildToolRegistry(guard *tools.PathGuard) *tools.Registry {
	reg := tools.NewRegistry()
	tracker := tools.NewReadTracker()
	reg.Register(tools.NewBashTool(guard))
	reg.Register(tools.NewReadTool(guard, tracker))
	reg.Register(tools.NewWriteTool(guard, tracker))
	reg.Register(tools.NewEditTool(guard, tracker))
	reg.Register(tools.NewGlobTool(guard))
	reg.Register(tools.NewGrepTool(guard, tracker))
	reg.Register(tools.NewGitTool(guard))
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

func buildRoutingContext(input string) *orchestrator.RoutingContext {
	lower := strings.ToLower(input)
	existingCodeCues := []string{
		"existing", "current", "repo", "repository", "module", "handler", "middleware",
		"regression", "refactor", "fix", "bug", "test", "tests", "coverage",
		"runtime", "service", "worker", "cli", "command",
	}
	verificationCues := []string{
		"test", "tests", "verify", "verification", "regression", "coverage", "lint", "build",
	}

	ctx := &orchestrator.RoutingContext{
		EstimatedFiles: estimateFiles(input),
	}
	for _, cue := range existingCodeCues {
		if strings.Contains(lower, cue) {
			ctx.ExistingCode = true
			break
		}
	}
	for _, cue := range verificationCues {
		if strings.Contains(lower, cue) {
			ctx.VerificationHint = true
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
