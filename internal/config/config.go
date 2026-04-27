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

	Anthropic ProviderConfig `yaml:"anthropic"`
	OpenAI    ProviderConfig `yaml:"openai"`
	Ollama    OllamaConfig   `yaml:"ollama"`

	MaxContextTokens  int     `yaml:"max_context_tokens"`
	CompressThreshold float64 `yaml:"compress_threshold"`

	// FallbackModel is the model used when a provider's Model field is
	// empty or a downstream path needs a sane default (see
	// cmd/elnath/commands.go resolveFallbackModel). Defaults to gpt-5.5;
	// overridden by ELNATH_FALLBACK_MODEL env var or yaml fallback_model.
	FallbackModel string `yaml:"fallback_model"`

	Permission     PermissionConfig     `yaml:"permission"`
	Sandbox        SandboxConfig        `yaml:"sandbox"`
	Principal      PrincipalConfig      `yaml:"principal"`
	Daemon         DaemonConfig         `yaml:"daemon"`
	FaultInjection FaultInjectionConfig `yaml:"fault_injection"`
	Telegram       TelegramConfig       `yaml:"telegram"`
	Research       ResearchConfig       `yaml:"research"`
	LLMExtraction  LLMExtractionConfig  `yaml:"llm_extraction"`
	MagicDocs      MagicDocsConfig      `yaml:"magic_docs"`
	Ambient        AmbientConfig        `yaml:"ambient"`
	SelfHealing    SelfHealingConfig    `yaml:"self_healing"`
	Projects       []ProjectRef         `yaml:"projects"`
	MCPServers     []MCPServerConfig    `yaml:"mcp_servers"`
	Hooks          []HookConfig         `yaml:"hooks"`
}

// SelfHealingConfig controls the Phase 0 observe-only reflection infrastructure.
// See docs/superpowers/specs/2026-04-20-self-healing-observe-only-phase0-design.md.
type SelfHealingConfig struct {
	Enabled        bool   `yaml:"enabled"`         // default true
	ObserveOnly    bool   `yaml:"observe_only"`    // default true (Phase 0)
	MaxTurns       int    `yaml:"max_turns"`       // transcript cap, default 20
	TimeoutSeconds int    `yaml:"timeout_seconds"` // per-reflection LLM call cap, default 15
	Model          string `yaml:"model"`           // blank → reuse main provider model
	Path           string `yaml:"path"`            // blank → <data_dir>/self_heal_attempts.jsonl
	MaxConcurrent  int    `yaml:"max_concurrent"`  // default 2
	QueueSize      int    `yaml:"queue_size"`      // default 10
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
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	Timeout int    `yaml:"timeout_seconds"`
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
	if v := os.Getenv("ELNATH_ANTHROPIC_API_KEY"); v != "" {
		cfg.Anthropic.APIKey = v
	}
	if v := os.Getenv("ELNATH_FALLBACK_MODEL"); v != "" {
		cfg.FallbackModel = v
	}
	if v := os.Getenv("ELNATH_OPENAI_API_KEY"); v != "" {
		cfg.OpenAI.APIKey = v
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

	switch cfg.Permission.Mode {
	case "default", "accept_edits", "plan", "bypass":
	default:
		return fmt.Errorf("unknown permission mode: %q", cfg.Permission.Mode)
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

	return nil
}
