package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DataDir string `yaml:"data_dir"`
	WikiDir string `yaml:"wiki_dir"`
	LogLevel string `yaml:"log_level"`

	Anthropic ProviderConfig `yaml:"anthropic"`
	OpenAI    ProviderConfig `yaml:"openai"`
	Ollama    OllamaConfig   `yaml:"ollama"`

	Permission PermissionConfig `yaml:"permission"`
	Daemon     DaemonConfig     `yaml:"daemon"`
	Research   ResearchConfig   `yaml:"research"`
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

type DaemonConfig struct {
	SocketPath string `yaml:"socket_path"`
	MaxWorkers int    `yaml:"max_workers"`
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

	switch cfg.Permission.Mode {
	case "default", "accept_edits", "plan", "bypass":
	default:
		return fmt.Errorf("unknown permission mode: %q", cfg.Permission.Mode)
	}

	return nil
}
