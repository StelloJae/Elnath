package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- DefaultConfig ---

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if cfg.DataDir == "" {
		t.Error("DataDir should not be empty")
	}
	if cfg.WikiDir == "" {
		t.Error("WikiDir should not be empty")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel %q, got %q", "info", cfg.LogLevel)
	}
	if cfg.Anthropic.Model == "" {
		t.Error("Anthropic.Model should not be empty")
	}
	if cfg.Anthropic.Timeout <= 0 {
		t.Error("Anthropic.Timeout should be positive")
	}
	if cfg.Permission.Mode != "default" {
		t.Errorf("expected Permission.Mode %q, got %q", "default", cfg.Permission.Mode)
	}
	if cfg.Daemon.MaxWorkers <= 0 {
		t.Error("Daemon.MaxWorkers should be positive")
	}
	if cfg.Research.MaxRounds <= 0 {
		t.Error("Research.MaxRounds should be positive")
	}
	if cfg.LLMExtraction.MinMessages != 5 {
		t.Errorf("expected LLMExtraction.MinMessages %d, got %d", 5, cfg.LLMExtraction.MinMessages)
	}
	if cfg.LLMExtraction.Model != "claude-haiku-4-5" {
		t.Errorf("expected LLMExtraction.Model %q, got %q", "claude-haiku-4-5", cfg.LLMExtraction.Model)
	}
	if cfg.LLMExtraction.Enabled {
		t.Error("LLMExtraction.Enabled should default to false")
	}
	if cfg.FallbackModel != "gpt-5.5" {
		t.Errorf("expected FallbackModel default %q, got %q", "gpt-5.5", cfg.FallbackModel)
	}
}

// --- DefaultConfigPath ---

func TestDefaultConfigPath(t *testing.T) {
	p := DefaultConfigPath()
	if p == "" {
		t.Fatal("DefaultConfigPath returned empty string")
	}
	if !strings.HasSuffix(p, filepath.Join(".elnath", "config.yaml")) {
		t.Errorf("unexpected config path %q", p)
	}
}

// --- Load ---

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	yaml := "data_dir: " + dir + "\nwiki_dir: " + wikiDir + "\nlog_level: debug\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.DataDir != dir {
		t.Errorf("expected DataDir %q, got %q", dir, cfg.DataDir)
	}
	if cfg.WikiDir != wikiDir {
		t.Errorf("expected WikiDir %q, got %q", wikiDir, cfg.WikiDir)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel %q, got %q", "debug", cfg.LogLevel)
	}
}

func TestLoad_PrincipalConfig(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	yaml := "data_dir: " + dir + "\nwiki_dir: " + wikiDir + "\nprincipal:\n  user_id: stello\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Principal.UserID != "stello" {
		t.Fatalf("Principal.UserID = %q, want stello", cfg.Principal.UserID)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(":\tinvalid: yaml: {["), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoad_MissingFile_ReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nonexistent.yaml")

	// Default config has data_dir and wiki_dir set but wiki_dir won't exist as a dir,
	// and validate checks wiki_dir only when it IS reachable via os.Stat.
	// With a missing file, Load returns defaults without error.
	cfg, err := Load(cfgPath)
	if err != nil {
		// validate may reject default wiki_dir if it doesn't exist as a file (it doesn't),
		// but validate only errors if wiki_dir IS reachable and NOT a directory.
		// A missing wiki_dir passes validate — verify that's the case here.
		t.Fatalf("Load with missing file returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoad_EmptyPath_UsesDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoad_EnvOverridesApplied(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	yaml := "data_dir: " + dir + "\nwiki_dir: " + wikiDir + "\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ELNATH_ANTHROPIC_API_KEY", "env-anthro-key")
	t.Setenv("ELNATH_OPENAI_API_KEY", "env-oai-key")
	t.Setenv("ELNATH_LOG_LEVEL", "warn")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Anthropic.APIKey != "env-anthro-key" {
		t.Errorf("expected Anthropic.APIKey %q, got %q", "env-anthro-key", cfg.Anthropic.APIKey)
	}
	if cfg.OpenAI.APIKey != "env-oai-key" {
		t.Errorf("expected OpenAI.APIKey %q, got %q", "env-oai-key", cfg.OpenAI.APIKey)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("expected LogLevel %q, got %q", "warn", cfg.LogLevel)
	}
}

func TestLoad_LLMExtractionDefaultsWithoutBlock(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	yaml := "data_dir: " + dir + "\nwiki_dir: " + wikiDir + "\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.LLMExtraction.MinMessages != 5 {
		t.Fatalf("LLMExtraction.MinMessages = %d, want 5", cfg.LLMExtraction.MinMessages)
	}
	if cfg.LLMExtraction.Model != "claude-haiku-4-5" {
		t.Fatalf("LLMExtraction.Model = %q, want default", cfg.LLMExtraction.Model)
	}
	if cfg.LLMExtraction.Enabled {
		t.Fatal("LLMExtraction.Enabled = true, want false")
	}
}

func TestLoad_LLMExtractionPartialBlockKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	yaml := "data_dir: " + dir + "\nwiki_dir: " + wikiDir + "\nllm_extraction:\n  enabled: true\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.LLMExtraction.Enabled {
		t.Fatal("LLMExtraction.Enabled = false, want true")
	}
	if cfg.LLMExtraction.MinMessages != 5 {
		t.Fatalf("LLMExtraction.MinMessages = %d, want 5", cfg.LLMExtraction.MinMessages)
	}
	if cfg.LLMExtraction.Model != "claude-haiku-4-5" {
		t.Fatalf("LLMExtraction.Model = %q, want default", cfg.LLMExtraction.Model)
	}
}

// --- applyEnvOverrides ---

func TestApplyEnvOverrides(t *testing.T) {
	tests := []struct {
		name   string
		envKey string
		envVal string
		check  func(*Config) string
		want   string
	}{
		{
			name:   "ELNATH_DATA_DIR",
			envKey: "ELNATH_DATA_DIR",
			envVal: "/tmp/data",
			check:  func(c *Config) string { return c.DataDir },
			want:   "/tmp/data",
		},
		{
			name:   "ELNATH_WIKI_DIR",
			envKey: "ELNATH_WIKI_DIR",
			envVal: "/tmp/wiki",
			check:  func(c *Config) string { return c.WikiDir },
			want:   "/tmp/wiki",
		},
		{
			name:   "ELNATH_LOG_LEVEL",
			envKey: "ELNATH_LOG_LEVEL",
			envVal: "debug",
			check:  func(c *Config) string { return c.LogLevel },
			want:   "debug",
		},
		{
			name:   "ELNATH_ANTHROPIC_API_KEY",
			envKey: "ELNATH_ANTHROPIC_API_KEY",
			envVal: "anthro-key",
			check:  func(c *Config) string { return c.Anthropic.APIKey },
			want:   "anthro-key",
		},
		{
			name:   "ELNATH_OPENAI_API_KEY",
			envKey: "ELNATH_OPENAI_API_KEY",
			envVal: "oai-key",
			check:  func(c *Config) string { return c.OpenAI.APIKey },
			want:   "oai-key",
		},
		{
			name:   "ELNATH_OLLAMA_BASE_URL",
			envKey: "ELNATH_OLLAMA_BASE_URL",
			envVal: "http://localhost:11434",
			check:  func(c *Config) string { return c.Ollama.BaseURL },
			want:   "http://localhost:11434",
		},
		{
			name:   "ELNATH_OLLAMA_MODEL",
			envKey: "ELNATH_OLLAMA_MODEL",
			envVal: "llama3",
			check:  func(c *Config) string { return c.Ollama.Model },
			want:   "llama3",
		},
		{
			name:   "ELNATH_PERMISSION_MODE",
			envKey: "ELNATH_PERMISSION_MODE",
			envVal: "bypass",
			check:  func(c *Config) string { return c.Permission.Mode },
			want:   "bypass",
		},
		{
			name:   "ELNATH_TELEGRAM_ENABLED",
			envKey: "ELNATH_TELEGRAM_ENABLED",
			envVal: "true",
			check: func(c *Config) string {
				if c.Telegram.Enabled {
					return "true"
				}
				return "false"
			},
			want: "true",
		},
		{
			name:   "ELNATH_TELEGRAM_BOT_TOKEN",
			envKey: "ELNATH_TELEGRAM_BOT_TOKEN",
			envVal: "bot-token",
			check:  func(c *Config) string { return c.Telegram.BotToken },
			want:   "bot-token",
		},
		{
			name:   "ELNATH_TELEGRAM_CHAT_ID",
			envKey: "ELNATH_TELEGRAM_CHAT_ID",
			envVal: "12345",
			check:  func(c *Config) string { return c.Telegram.ChatID },
			want:   "12345",
		},
		{
			name:   "ELNATH_TELEGRAM_API_BASE_URL",
			envKey: "ELNATH_TELEGRAM_API_BASE_URL",
			envVal: "https://telegram.example.test",
			check:  func(c *Config) string { return c.Telegram.APIBaseURL },
			want:   "https://telegram.example.test",
		},
		{
			name:   "ELNATH_TELEGRAM_POLL_TIMEOUT_SECONDS",
			envKey: "ELNATH_TELEGRAM_POLL_TIMEOUT_SECONDS",
			envVal: "45",
			check: func(c *Config) string {
				return strconv.Itoa(c.Telegram.PollTimeoutSeconds)
			},
			want: "45",
		},
		{
			name:   "ELNATH_FALLBACK_MODEL",
			envKey: "ELNATH_FALLBACK_MODEL",
			envVal: "gpt-custom",
			check:  func(c *Config) string { return c.FallbackModel },
			want:   "gpt-custom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)
			cfg := DefaultConfig()
			applyEnvOverrides(cfg)
			if got := tc.check(cfg); got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestParseEnvBool(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "true", want: true},
		{value: "1", want: true},
		{value: "yes", want: true},
		{value: "on", want: true},
		{value: "false", want: false},
		{value: "0", want: false},
		{value: "", want: false},
	}

	for _, tc := range tests {
		if got := parseEnvBool(tc.value); got != tc.want {
			t.Errorf("parseEnvBool(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestApplyEnvOverrides_EmptyVarNoOverwrite(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogLevel = "info"
	// Ensure env var is unset.
	t.Setenv("ELNATH_LOG_LEVEL", "")
	applyEnvOverrides(cfg)
	if cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel unchanged %q, got %q", "info", cfg.LogLevel)
	}
}

// --- validate ---

func TestValidate(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "valid config",
			mutate:  func(c *Config) {},
			wantErr: "",
		},
		{
			name:    "empty data_dir",
			mutate:  func(c *Config) { c.DataDir = "" },
			wantErr: "data_dir is required",
		},
		{
			name:    "empty wiki_dir",
			mutate:  func(c *Config) { c.WikiDir = "" },
			wantErr: "wiki_dir is required",
		},
		{
			name:    "locale auto",
			mutate:  func(c *Config) { c.Locale = "auto" },
			wantErr: "",
		},
		{
			name:    "locale ja",
			mutate:  func(c *Config) { c.Locale = "ja" },
			wantErr: "",
		},
		{
			name:    "locale zh",
			mutate:  func(c *Config) { c.Locale = "zh" },
			wantErr: "",
		},
		{
			name:    "locale uppercase and padded",
			mutate:  func(c *Config) { c.Locale = " KO " },
			wantErr: "",
		},
		{
			name:    "unsupported locale",
			mutate:  func(c *Config) { c.Locale = "fr" },
			wantErr: "supported: en, ko, ja, zh, auto",
		},
		{
			name: "wiki_dir is a file not a dir",
			mutate: func(c *Config) {
				f := filepath.Join(dir, "not-a-dir.txt")
				_ = os.WriteFile(f, []byte("x"), 0o600)
				c.WikiDir = f
			},
			wantErr: "is not a directory",
		},
		{
			name:    "unknown permission mode",
			mutate:  func(c *Config) { c.Permission.Mode = "unknown" },
			wantErr: "unknown permission mode",
		},
		{
			name: "mcp_server missing command",
			mutate: func(c *Config) {
				c.MCPServers = []MCPServerConfig{{Name: "test", Command: ""}}
			},
			wantErr: "command is required",
		},
		{
			name: "hook missing both commands",
			mutate: func(c *Config) {
				c.Hooks = []HookConfig{{Matcher: "*"}}
			},
			wantErr: "at least one of pre_command or post_command is required",
		},
		{
			name: "hook with only pre_command is valid",
			mutate: func(c *Config) {
				c.Hooks = []HookConfig{{Matcher: "*", PreCommand: "echo pre"}}
			},
			wantErr: "",
		},
		{
			name: "hook with only post_command is valid",
			mutate: func(c *Config) {
				c.Hooks = []HookConfig{{Matcher: "*", PostCommand: "echo post"}}
			},
			wantErr: "",
		},
		{
			name:    "permission mode accept_edits",
			mutate:  func(c *Config) { c.Permission.Mode = "accept_edits" },
			wantErr: "",
		},
		{
			name:    "permission mode plan",
			mutate:  func(c *Config) { c.Permission.Mode = "plan" },
			wantErr: "",
		},
		{
			name:    "permission mode bypass",
			mutate:  func(c *Config) { c.Permission.Mode = "bypass" },
			wantErr: "",
		},
		{
			name: "telegram enabled missing bot token",
			mutate: func(c *Config) {
				c.Telegram.Enabled = true
				c.Telegram.ChatID = "123"
			},
			wantErr: "telegram.bot_token is required",
		},
		{
			name: "telegram enabled missing chat id",
			mutate: func(c *Config) {
				c.Telegram.Enabled = true
				c.Telegram.BotToken = "token"
			},
			wantErr: "telegram.chat_id is required",
		},
		{
			name: "telegram enabled valid",
			mutate: func(c *Config) {
				c.Telegram.Enabled = true
				c.Telegram.BotToken = "token"
				c.Telegram.ChatID = "123"
			},
			wantErr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				DataDir: dir,
				WikiDir: wikiDir,
				Permission: PermissionConfig{
					Mode: "default",
				},
			}
			tc.mutate(cfg)
			err := validate(cfg)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tc.wantErr)
				} else if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
			}
		})
	}
}

// --- WriteFromResult ---

func TestWriteFromResult_BasicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	result := &OnboardingResult{
		APIKey:         "sk-test-123",
		DataDir:        filepath.Join(dir, "data"),
		WikiDir:        filepath.Join(dir, "wiki"),
		PermissionMode: "accept_edits",
	}
	if err := WriteFromResult(cfgPath, result); err != nil {
		t.Fatalf("WriteFromResult: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Permission.Mode != "accept_edits" {
		t.Errorf("expected permission mode %q, got %q", "accept_edits", cfg.Permission.Mode)
	}
	if cfg.Anthropic.APIKey != "sk-test-123" {
		t.Errorf("expected api key %q, got %q", "sk-test-123", cfg.Anthropic.APIKey)
	}
}

func TestWriteFromResult_WithMCPServers(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	result := &OnboardingResult{
		APIKey:         "sk-test",
		DataDir:        filepath.Join(dir, "data"),
		WikiDir:        filepath.Join(dir, "wiki"),
		PermissionMode: "default",
		MCPServers: []MCPServerConfig{
			{Name: "GitHub", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"}},
			{Name: "Filesystem", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"}},
		},
	}
	if err := WriteFromResult(cfgPath, result); err != nil {
		t.Fatalf("WriteFromResult: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load round-trip: %v", err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("expected 2 MCP servers, got %d", len(cfg.MCPServers))
	}
	if cfg.MCPServers[0].Name != "GitHub" {
		t.Errorf("expected first server name %q, got %q", "GitHub", cfg.MCPServers[0].Name)
	}
	if cfg.MCPServers[0].Command != "npx" {
		t.Errorf("expected command %q, got %q", "npx", cfg.MCPServers[0].Command)
	}
	if len(cfg.MCPServers[0].Args) != 2 {
		t.Errorf("expected 2 args, got %d", len(cfg.MCPServers[0].Args))
	}
}

func TestWriteFromResult_DefaultPermission(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	result := &OnboardingResult{
		APIKey:  "sk-test",
		DataDir: filepath.Join(dir, "data"),
		WikiDir: filepath.Join(dir, "wiki"),
	}
	if err := WriteFromResult(cfgPath, result); err != nil {
		t.Fatalf("WriteFromResult: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Permission.Mode != "default" {
		t.Errorf("expected default permission, got %q", cfg.Permission.Mode)
	}
}

func TestWriteFromResult_PreservesExistingPrincipal(t *testing.T) {
	dir := t.TempDir()
	wikiDir := filepath.Join(dir, "wiki-existing")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	existing := "data_dir: " + filepath.Join(dir, "data-existing") + "\nwiki_dir: " + wikiDir + "\nprincipal:\n  user_id: stello\n"
	if err := os.WriteFile(cfgPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	result := &OnboardingResult{
		APIKey:         "sk-test",
		DataDir:        filepath.Join(dir, "data"),
		WikiDir:        filepath.Join(dir, "wiki"),
		PermissionMode: "default",
	}
	if err := WriteFromResult(cfgPath, result); err != nil {
		t.Fatalf("WriteFromResult: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Principal.UserID != "stello" {
		t.Fatalf("Principal.UserID = %q, want stello", cfg.Principal.UserID)
	}
}
