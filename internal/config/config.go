package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir  string `yaml:"data_dir"`
	WikiDir  string `yaml:"wiki_dir"`
	Locale   string `yaml:"locale"`
	LogLevel string `yaml:"log_level"`
	Provider string `yaml:"provider"`

	Anthropic ProviderConfig `yaml:"anthropic"`
	OpenAI    ProviderConfig `yaml:"openai"`
	// OpenAIResponses is for OpenAI Responses-compatible providers. This keeps
	// transport choice independent from model names, so users can route GPT,
	// Kimi, MiniMax, or other compatible models through /responses.
	OpenAIResponses ProviderConfig `yaml:"openai_responses"`
	Ollama          OllamaConfig   `yaml:"ollama"`

	MaxContextTokens  int     `yaml:"max_context_tokens"`
	CompressThreshold float64 `yaml:"compress_threshold"`

	// FallbackModel is the model used when a provider's Model field is
	// empty or a downstream path needs a sane default (see
	// cmd/elnath/commands.go resolveFallbackModel). Defaults to gpt-5.5;
	// overridden by ELNATH_FALLBACK_MODEL env var or yaml fallback_model.
	FallbackModel string `yaml:"fallback_model"`

	Permission     PermissionConfig     `yaml:"permission"`
	Tools          ToolsConfig          `yaml:"tools"`
	Skills         SkillsConfig         `yaml:"skills"`
	Sandbox        SandboxConfig        `yaml:"sandbox"`
	Principal      PrincipalConfig      `yaml:"principal"`
	Daemon         DaemonConfig         `yaml:"daemon"`
	FaultInjection FaultInjectionConfig `yaml:"fault_injection"`
	Agentic        AgenticConfig        `yaml:"agentic"`
	Telegram       TelegramConfig       `yaml:"telegram"`
	Research       ResearchConfig       `yaml:"research"`
	LLMExtraction  LLMExtractionConfig  `yaml:"llm_extraction"`
	Reasoning      ReasoningConfig      `yaml:"reasoning"`
	MagicDocs      MagicDocsConfig      `yaml:"magic_docs"`
	Ambient        AmbientConfig        `yaml:"ambient"`
	SelfHealing    SelfHealingConfig    `yaml:"self_healing"`
	Projects       []ProjectRef         `yaml:"projects"`
	MCPServers     []MCPServerConfig    `yaml:"mcp_servers"`
	Hooks          []HookConfig         `yaml:"hooks"`
}

// ReasoningConfig controls request-level reasoning effort. It is separate
// from provider defaults so a runtime can choose per-task effort when the
// active provider supports it.
type ReasoningConfig struct {
	EffortMode string `yaml:"effort_mode"` // manual or auto
	Effort     string `yaml:"effort"`      // none/minimal/low/medium/high/xhigh
}

// SelfHealingConfig controls the Phase 0 observe-only reflection infrastructure.
// See docs/superpowers/specs/2026-04-20-self-healing-observe-only-phase0-design.md.
type SelfHealingConfig struct {
	Enabled            bool   `yaml:"enabled"`              // default true
	ObserveOnly        bool   `yaml:"observe_only"`         // default true (Phase 0)
	MaxTurns           int    `yaml:"max_turns"`            // transcript cap, default 20
	TimeoutSeconds     int    `yaml:"timeout_seconds"`      // per-reflection LLM call cap, default 15
	CompletionRetryMax int    `yaml:"completion_retry_max"` // bounded correction retries, default 1 when not observe-only
	Model              string `yaml:"model"`                // blank → reuse main provider model
	Path               string `yaml:"path"`                 // blank → <data_dir>/self_heal_attempts.jsonl
	MaxConcurrent      int    `yaml:"max_concurrent"`       // default 2
	QueueSize          int    `yaml:"queue_size"`           // default 10
}

// MCPServerConfig defines an external MCP server to connect to.
type MCPServerConfig struct {
	Name    string   `yaml:"name"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	Env     []string `yaml:"env"`
}

// HookConfig defines a shell command hook for tool execution lifecycle events.
type HookConfig struct {
	Matcher     string `yaml:"matcher"`      // glob pattern for tool names (e.g., "*", "bash")
	PreCommand  string `yaml:"pre_command"`  // shell command to run before tool execution
	PostCommand string `yaml:"post_command"` // shell command to run after tool execution
}

// ProjectRef points to another Elnath project whose wiki and conversation
// history can be searched via cross-project intelligence tools.
type ProjectRef struct {
	Name    string `yaml:"name"`
	WikiDir string `yaml:"wiki_dir"`
	DataDir string `yaml:"data_dir"`
}

type ProviderConfig struct {
	APIKey          string `yaml:"api_key"`
	BaseURL         string `yaml:"base_url"`
	Model           string `yaml:"model"`
	ReasoningEffort string `yaml:"reasoning_effort"`
	Timeout         int    `yaml:"timeout_seconds"`
}

type ToolsConfig struct {
	ExposureMode string `yaml:"exposure_mode"`
}

type SkillsConfig struct {
	// PluginCache controls whether Codex plugin-cache SKILL.md roots are loaded.
	// Default "enabled" preserves existing discovery; "disabled" keeps local
	// wiki/project/user skills while skipping ~/.codex/plugins/cache skills.
	PluginCache string `yaml:"plugin_cache"`
}

type PermissionConfig struct {
	Mode  string   `yaml:"mode"` // default, accept_edits, plan, bypass
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type SandboxConfig struct {
	Mode             string   `yaml:"mode"`
	NetworkAllowlist []string `yaml:"network_allowlist"`
	NetworkDenylist  []string `yaml:"network_denylist"`
}

type PrincipalConfig struct {
	UserID string `yaml:"user_id"`
}

type DaemonConfig struct {
	SocketPath         string   `yaml:"socket_path"`
	MaxWorkers         int      `yaml:"max_workers"`
	MaxRecoveries      int      `yaml:"max_recoveries"`
	InactivityTimeout  int      `yaml:"inactivity_timeout_seconds"`
	WallClockTimeout   int      `yaml:"wall_clock_timeout_seconds"`
	ScheduledTasksPath string   `yaml:"scheduled_tasks_path"`
	WorkDir            string   `yaml:"work_dir"`
	ProtectedPaths     []string `yaml:"protected_paths"`
	// WorkspaceRetention controls per-session workspace cleanup after a task
	// completes. "" or "immediate" deletes <work_dir>/sessions/<sid>/ as soon
	// as the task ends (default — prevents cross-session contamination and
	// disk creep). "keep" leaves the subdir in place so a follow-up task
	// resuming the same sessionID can still see prior tool artifacts.
	WorkspaceRetention string `yaml:"workspace_retention"`
}

type FaultInjectionConfig struct {
	Enabled   bool   `yaml:"enabled"`
	OutputDir string `yaml:"output_dir"`
}

const (
	ToolExposureModeStandard    = "standard"
	ToolExposureModeSearchFirst = "search_first"

	SkillPluginCacheModeEnabled  = "enabled"
	SkillPluginCacheModeDisabled = "disabled"

	AgenticEnforcementModeObserve = "observe"
	AgenticEnforcementModeGateway = "gateway"

	AgenticCompletionGateModeObserve      = "observe"
	AgenticCompletionGateModeVerification = "verification"
)

type AgenticConfig struct {
	Enforcement    AgenticEnforcementConfig    `yaml:"enforcement"`
	CompletionGate AgenticCompletionGateConfig `yaml:"completion_gate"`
}

type AgenticEnforcementConfig struct {
	Mode string `yaml:"mode"`
}

type AgenticCompletionGateConfig struct {
	Mode string `yaml:"mode"`
}

type TelegramConfig struct {
	Enabled            bool   `yaml:"enabled"`
	BotToken           string `yaml:"bot_token"`
	ChatID             string `yaml:"chat_id"`
	APIBaseURL         string `yaml:"api_base_url"`
	PollTimeoutSeconds int    `yaml:"poll_timeout_seconds"`
}

type OllamaConfig struct {
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	APIKey  string `yaml:"api_key"` // optional, Ollama doesn't require auth by default
}

type ResearchConfig struct {
	MaxRounds  int     `yaml:"max_rounds"`
	CostCapUSD float64 `yaml:"cost_cap_usd"`
}

type LLMExtractionConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Model       string `yaml:"model"`
	MinMessages int    `yaml:"min_messages"`
	// APIKey, when set, isolates lesson extraction onto a dedicated Anthropic
	// credential. Falls back to Anthropic.APIKey. When both are empty, the
	// main provider (Codex OAuth / OpenAI / etc.) is reused.
	APIKey string `yaml:"api_key"`
	// ClaudeCodeSignature prepends the Claude Code identity line to the
	// extraction system prompt. Last-resort workaround for Claude Code OAuth
	// scope rejecting tool-laden requests that lack that signature.
	ClaudeCodeSignature bool `yaml:"claude_code_signature"`
}

type MagicDocsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Model   string `yaml:"model"`
}

type AmbientConfig struct {
	Enabled       bool `yaml:"enabled"`
	MaxConcurrent int  `yaml:"max_concurrent"`
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				applyEnvOverrides(cfg)
				return cfg, nil
			}
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	applyEnvOverrides(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".elnath", "config.yaml")
}

func DefaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".elnath", "data")
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("ELNATH_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("ELNATH_WIKI_DIR"); v != "" {
		cfg.WikiDir = v
	}
	if v := os.Getenv("ELNATH_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("ELNATH_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("ELNATH_ANTHROPIC_API_KEY"); v != "" {
		cfg.Anthropic.APIKey = v
	}
	if v := os.Getenv("ELNATH_ANTHROPIC_BASE_URL"); v != "" {
		cfg.Anthropic.BaseURL = v
	}
	if v := os.Getenv("ELNATH_ANTHROPIC_MODEL"); v != "" {
		cfg.Anthropic.Model = v
	}
	if v := os.Getenv("ELNATH_ANTHROPIC_TIMEOUT_SECONDS"); v != "" {
		if timeout, err := strconv.Atoi(v); err == nil {
			cfg.Anthropic.Timeout = timeout
		}
	}
	if v := os.Getenv("ELNATH_FALLBACK_MODEL"); v != "" {
		cfg.FallbackModel = v
	}
	if v := os.Getenv("ELNATH_OPENAI_API_KEY"); v != "" {
		cfg.OpenAI.APIKey = v
	}
	if v := os.Getenv("ELNATH_OPENAI_BASE_URL"); v != "" {
		cfg.OpenAI.BaseURL = v
	}
	if v := os.Getenv("ELNATH_OPENAI_MODEL"); v != "" {
		cfg.OpenAI.Model = v
	}
	if v := os.Getenv("ELNATH_OPENAI_TIMEOUT_SECONDS"); v != "" {
		if timeout, err := strconv.Atoi(v); err == nil {
			cfg.OpenAI.Timeout = timeout
		}
	}
	if v := os.Getenv("ELNATH_OPENAI_RESPONSES_API_KEY"); v != "" {
		cfg.OpenAIResponses.APIKey = v
	}
	if v := os.Getenv("ELNATH_OPENAI_RESPONSES_BASE_URL"); v != "" {
		cfg.OpenAIResponses.BaseURL = v
	}
	if v := os.Getenv("ELNATH_OPENAI_RESPONSES_MODEL"); v != "" {
		cfg.OpenAIResponses.Model = v
	}
	if v := os.Getenv("ELNATH_OPENAI_RESPONSES_REASONING_EFFORT"); v != "" {
		cfg.OpenAIResponses.ReasoningEffort = v
	}
	if v := os.Getenv("ELNATH_OPENAI_RESPONSES_TIMEOUT_SECONDS"); v != "" {
		if timeout, err := strconv.Atoi(v); err == nil {
			cfg.OpenAIResponses.Timeout = timeout
		}
	}
	if v := os.Getenv("ELNATH_REASONING_EFFORT_MODE"); v != "" {
		cfg.Reasoning.EffortMode = v
	}
	if v := os.Getenv("ELNATH_REASONING_EFFORT"); v != "" {
		cfg.Reasoning.Effort = v
	}
	if v, ok := os.LookupEnv("ELNATH_SELF_HEALING_ENABLED"); ok {
		cfg.SelfHealing.Enabled = parseEnvBool(v)
	}
	if v, ok := os.LookupEnv("ELNATH_SELF_HEALING_OBSERVE_ONLY"); ok {
		cfg.SelfHealing.ObserveOnly = parseEnvBool(v)
	}
	if v := os.Getenv("ELNATH_SELF_HEALING_TIMEOUT_SECONDS"); v != "" {
		if timeout, err := strconv.Atoi(v); err == nil {
			cfg.SelfHealing.TimeoutSeconds = timeout
		}
	}
	if v := os.Getenv("ELNATH_SELF_HEALING_COMPLETION_RETRY_MAX"); v != "" {
		if maxRetries, err := strconv.Atoi(v); err == nil {
			cfg.SelfHealing.CompletionRetryMax = maxRetries
		}
	}
	if v := os.Getenv("ELNATH_OLLAMA_BASE_URL"); v != "" {
		cfg.Ollama.BaseURL = v
	}
	if v := os.Getenv("ELNATH_OLLAMA_MODEL"); v != "" {
		cfg.Ollama.Model = v
	}
	if v := os.Getenv("ELNATH_PERMISSION_MODE"); v != "" {
		cfg.Permission.Mode = v
	}
	if v := os.Getenv("ELNATH_TOOLS_EXPOSURE_MODE"); v != "" {
		cfg.Tools.ExposureMode = v
	}
	if v := os.Getenv("ELNATH_SKILLS_PLUGIN_CACHE"); v != "" {
		cfg.Skills.PluginCache = v
	}
	if v := os.Getenv("ELNATH_LOCALE"); v != "" {
		cfg.Locale = v
	}
	if v, ok := os.LookupEnv("ELNATH_TELEGRAM_ENABLED"); ok {
		cfg.Telegram.Enabled = parseEnvBool(v)
	}
	if v := os.Getenv("ELNATH_TELEGRAM_BOT_TOKEN"); v != "" {
		cfg.Telegram.BotToken = v
	}
	if v := os.Getenv("ELNATH_TELEGRAM_CHAT_ID"); v != "" {
		cfg.Telegram.ChatID = v
	}
	if v := os.Getenv("ELNATH_TELEGRAM_API_BASE_URL"); v != "" {
		cfg.Telegram.APIBaseURL = v
	}
	if v := os.Getenv("ELNATH_TELEGRAM_POLL_TIMEOUT_SECONDS"); v != "" {
		if timeout, err := strconv.Atoi(v); err == nil {
			cfg.Telegram.PollTimeoutSeconds = timeout
		}
	}
}

func parseEnvBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func validate(cfg *Config) error {
	if cfg.DataDir == "" {
		return fmt.Errorf("data_dir is required")
	}
	if cfg.WikiDir == "" {
		return fmt.Errorf("wiki_dir is required")
	}

	info, err := os.Stat(cfg.WikiDir)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("wiki_dir %q is not a directory", cfg.WikiDir)
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Locale)) {
	case "", "auto", "en", "ko", "ja", "zh":
	default:
		return fmt.Errorf("unsupported locale: %q (supported: en, ko, ja, zh, auto)", cfg.Locale)
	}

	if err := validateProviderReasoningEffort("anthropic", cfg.Anthropic.ReasoningEffort); err != nil {
		return err
	}
	if err := validateProviderReasoningEffort("openai", cfg.OpenAI.ReasoningEffort); err != nil {
		return err
	}
	if err := validateProviderReasoningEffort("openai_responses", cfg.OpenAIResponses.ReasoningEffort); err != nil {
		return err
	}
	if err := validateReasoningEffortMode(cfg.Reasoning.EffortMode); err != nil {
		return err
	}
	if err := validateProviderReasoningEffort("reasoning", cfg.Reasoning.Effort); err != nil {
		return err
	}
	if err := validateProviderName(cfg.Provider); err != nil {
		return err
	}
	if cfg.OpenAIResponses.APIKey == "" && (cfg.OpenAIResponses.BaseURL != "" || cfg.OpenAIResponses.Model != "") {
		return fmt.Errorf("openai_responses.api_key is required when openai_responses base_url or model is set")
	}

	switch cfg.Permission.Mode {
	case "default", "accept_edits", "plan", "bypass":
	default:
		return fmt.Errorf("unknown permission mode: %q", cfg.Permission.Mode)
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Tools.ExposureMode)) {
	case "", ToolExposureModeStandard, ToolExposureModeSearchFirst:
	default:
		return fmt.Errorf("unsupported tools.exposure_mode: %q (supported: standard, search_first)", cfg.Tools.ExposureMode)
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Skills.PluginCache)) {
	case "", SkillPluginCacheModeEnabled, SkillPluginCacheModeDisabled:
	default:
		return fmt.Errorf("unsupported skills.plugin_cache: %q (supported: enabled, disabled)", cfg.Skills.PluginCache)
	}
	if cfg.SelfHealing.CompletionRetryMax < 0 {
		return fmt.Errorf("self_healing.completion_retry_max must be >= 0")
	}
	if cfg.SelfHealing.CompletionRetryMax > 2 {
		return fmt.Errorf("self_healing.completion_retry_max must be <= 2")
	}

	for i, s := range cfg.MCPServers {
		if s.Command == "" {
			return fmt.Errorf("mcp_servers[%d]: command is required", i)
		}
	}

	for i, h := range cfg.Hooks {
		if h.PreCommand == "" && h.PostCommand == "" {
			return fmt.Errorf("hooks[%d]: at least one of pre_command or post_command is required", i)
		}
	}
	if cfg.Telegram.Enabled {
		if cfg.Telegram.BotToken == "" {
			return fmt.Errorf("telegram.bot_token is required when telegram.enabled=true")
		}
		if cfg.Telegram.ChatID == "" {
			return fmt.Errorf("telegram.chat_id is required when telegram.enabled=true")
		}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Agentic.Enforcement.Mode)) {
	case "", AgenticEnforcementModeObserve, AgenticEnforcementModeGateway:
	default:
		return fmt.Errorf("unsupported agentic.enforcement.mode: %q (supported: observe, gateway)", cfg.Agentic.Enforcement.Mode)
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Agentic.CompletionGate.Mode)) {
	case "", AgenticCompletionGateModeObserve, AgenticCompletionGateModeVerification:
	default:
		return fmt.Errorf("unsupported agentic.completion_gate.mode: %q (supported: observe, verification)", cfg.Agentic.CompletionGate.Mode)
	}

	return nil
}

func SkillsPluginCacheEnabled(cfg *Config) bool {
	if cfg == nil {
		return true
	}
	return strings.ToLower(strings.TrimSpace(cfg.Skills.PluginCache)) != SkillPluginCacheModeDisabled
}

func validateProviderReasoningEffort(providerName, effort string) error {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
		return nil
	default:
		return fmt.Errorf("%s.reasoning_effort %q is invalid (supported: none, minimal, low, medium, high, xhigh)", providerName, effort)
	}
}

func validateReasoningEffortMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "manual", "auto":
		return nil
	default:
		return fmt.Errorf("reasoning.effort_mode %q is invalid (supported: manual, auto)", mode)
	}
}

func NormalizeProviderName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "":
		return ""
	case "responses", "openai_responses", "openai-responses":
		return "openai-responses"
	case "anthropic", "openai", "codex", "ollama":
		return strings.ToLower(strings.TrimSpace(provider))
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func validateProviderName(provider string) error {
	switch NormalizeProviderName(provider) {
	case "", "anthropic", "openai", "openai-responses", "codex", "ollama":
		return nil
	default:
		return fmt.Errorf("provider %q is invalid (supported: anthropic, openai, openai_responses, codex, ollama)", provider)
	}
}
