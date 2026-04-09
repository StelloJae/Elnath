package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/onboarding"
)

func cmdSetup(_ context.Context, _ []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}

	// Load existing config for locale and rerun defaults.
	locale := onboarding.En
	var rerunOpts []onboarding.Option
	rerunOpts = append(rerunOpts, onboarding.WithRerunMode())
	if existing, err := config.Load(cfgPath); err == nil {
		if existing.Locale != "" {
			locale = onboarding.Locale(existing.Locale)
		}
		rerunOpts = append(rerunOpts, onboarding.WithExistingConfig(onboarding.ExistingConfig{
			Locale:         onboarding.Locale(existing.Locale),
			APIKey:         existing.Anthropic.APIKey,
			PermissionMode: existing.Permission.Mode,
			DataDir:        existing.DataDir,
			WikiDir:        existing.WikiDir,
		}))
	}

	// Back up existing config if present.
	if _, err := os.Stat(cfgPath); err == nil {
		backupPath := cfgPath + ".bak." + time.Now().Format("20060102-150405")
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("read existing config for backup: %w", err)
		}
		if err := os.WriteFile(backupPath, data, 0o600); err != nil {
			return fmt.Errorf("write config backup: %w", err)
		}
		fmt.Printf(onboarding.T(locale, "setup.backup")+"\n", backupPath)
	}

	result, err := onboarding.Run(cfgPath, version, rerunOpts...)
	if err != nil {
		return fmt.Errorf("setup wizard: %w", err)
	}

	cfgResult := onboardingResultToConfig(result)
	return config.WriteFromResult(cfgPath, cfgResult)
}
