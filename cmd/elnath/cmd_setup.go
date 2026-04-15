package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/onboarding"
	"github.com/stello/elnath/internal/userfacingerr"
)

func cmdSetup(ctx context.Context, _ []string) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	if hasFlag(os.Args, "--quickstart") {
		return cmdSetupQuickstart(ctx, cfgPath)
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

func cmdSetupQuickstart(ctx context.Context, cfgPath string) error {
	started := time.Now()

	result, err := onboarding.RunQuickstart(cfgPath, version)
	if err != nil {
		return userfacingerr.Wrap(userfacingerr.ELN001, err, "setup quickstart")
	}

	cfgResult := onboardingResultToConfig(&result.Result)
	if err := config.WriteFromResult(cfgPath, cfgResult); err != nil {
		return userfacingerr.Wrap(userfacingerr.ELN060, err, "write config")
	}

	metric := onboarding.MetricRecord{
		SetupStartedAt: started,
		Steps: onboarding.MetricSteps{
			Provider:  result.ProviderDetected,
			APIKey:    result.APIKey != "",
			SmokeTest: result.SmokeTestPassed,
			DemoTask:  false,
		},
	}

	demoRan := false
	if promptYN("Try a demo task? [Y/n] ", true) {
		demoCfg := &config.Config{
			DataDir: cfgResult.DataDir,
			WikiDir: cfgResult.WikiDir,
			Locale:  cfgResult.Locale,
			Anthropic: config.ProviderConfig{
				APIKey: cfgResult.APIKey,
			},
			Permission: config.PermissionConfig{Mode: cfgResult.PermissionMode},
		}
		provider, model, provErr := buildProvider(demoCfg)
		if provErr != nil {
			fmt.Fprintf(os.Stderr, "Demo skipped (no provider): %v\n", provErr)
		} else if err := onboarding.RunDemoTask(ctx, provider, model); err != nil {
			fmt.Fprintf(os.Stderr, "Demo task failed (that's ok): %v\n", err)
		} else {
			demoRan = true
		}
	}
	metric.Steps.DemoTask = demoRan
	metric.SetupCompletedAt = time.Now()
	metric.DurationSec = int(metric.SetupCompletedAt.Sub(metric.SetupStartedAt).Seconds())
	_ = onboarding.WriteMetric(metric)

	if !demoRan {
		fmt.Println("\nSetup complete. Run 'elnath run' to start.")
	}
	return nil
}

func promptYN(prompt string, defaultYes bool) bool {
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		return defaultYes
	}

	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" {
		return defaultYes
	}
	return answer == "y" || answer == "yes"
}
