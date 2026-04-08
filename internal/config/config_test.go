package config

import (
	"os"
	"path/filepath"
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

// --- applyEnvOverrides ---

func TestApplyEnvOverrides(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		envVal  string
		check   func(*Config) string
		want    string
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
