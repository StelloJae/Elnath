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
		Locale:   "en",
		LogLevel: "info",

		Anthropic: ProviderConfig{
			Model:   "claude-sonnet-4-6",
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
			SocketPath:         filepath.Join(base, "daemon.sock"),
			MaxWorkers:         3,
			MaxRecoveries:      3,
			InactivityTimeout:  600,
			WallClockTimeout:   1800,
			WorkDir:            filepath.Join(base, "workspace"),
			WorkspaceRetention: "immediate",
		},
		FaultInjection: FaultInjectionConfig{Enabled: false},
		Research: ResearchConfig{
			MaxRounds:  5,
			CostCapUSD: 5.0,
		},
		LLMExtraction: LLMExtractionConfig{
			Model:       "claude-haiku-4-5",
			MinMessages: 5,
		},
	}
}
