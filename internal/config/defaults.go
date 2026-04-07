package config

import (
	"os"
	"path/filepath"
)

func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".elnath")

	return &Config{
		DataDir:  filepath.Join(base, "data"),
		WikiDir:  filepath.Join(base, "wiki"),
		LogLevel: "info",

		Anthropic: ProviderConfig{
			Model:   "claude-sonnet-4-20250514",
			Timeout: 120,
		},
		OpenAI: ProviderConfig{
			Model:   "gpt-4o",
			Timeout: 120,
		},

		Permission: PermissionConfig{
			Mode: "default",
		},
		Daemon: DaemonConfig{
			SocketPath: filepath.Join(base, "daemon.sock"),
			MaxWorkers: 3,
		},
		Research: ResearchConfig{
			MaxRounds:  5,
			CostCapUSD: 5.0,
		},
	}
}
