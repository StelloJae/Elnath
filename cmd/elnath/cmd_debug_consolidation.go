package main

import (
	"context"
	"fmt"
	"os"

	"github.com/stello/elnath/internal/config"
)

// debugConsolidation handles `elnath debug consolidation <subcommand>`.
// Currently only `run [--force]` is implemented; `show` and `history` will
// follow once the flywheel has real data to report against.
func debugConsolidation(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return debugConsolidationUsage()
	}
	switch args[0] {
	case "run":
		return debugConsolidationRun(ctx, args[1:])
	case "help", "-h", "--help":
		return debugConsolidationUsage()
	default:
		return fmt.Errorf("debug consolidation: unknown subcommand %q (try: elnath debug consolidation help)", args[0])
	}
}

func debugConsolidationUsage() error {
	fmt.Fprintf(os.Stdout, `Usage: elnath debug consolidation <subcommand>

Subcommands:
  run [--force]  Execute one consolidation run. --force bypasses the time
                 and session gates but still respects an active lock.
  help           Show this help

State file: <data_dir>/consolidation_state.json
Lock file:  <data_dir>/.consolidate-lock
`)
	return nil
}

func debugConsolidationRun(ctx context.Context, args []string) error {
	force := false
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		return fmt.Errorf("debug consolidation run: unknown flag %q", a)
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("debug consolidation: load config: %w", err)
	}

	deps, err := buildConsolidationDepsFromCLI(cfg)
	if err != nil {
		return fmt.Errorf("debug consolidation: %w", err)
	}
	consolidator := newConsolidator(deps, force)

	fmt.Printf("Consolidation run (force=%v)\n", force)
	fmt.Printf("  Provider:    %s\n", deps.providerName)
	fmt.Printf("  Model:       %s\n", deps.model)
	fmt.Printf("  Wiki dir:    %s\n", cfg.WikiDir)
	fmt.Printf("  Data dir:    %s\n", cfg.DataDir)
	fmt.Println()

	result, err := consolidator.Run(ctx)
	if err != nil {
		return fmt.Errorf("consolidator run: %w", err)
	}

	if result.Skipped {
		fmt.Printf("Skipped: %s\n", result.SkipReason)
		return nil
	}
	if result.Error != nil {
		fmt.Printf("Failed: %v\n", result.Error)
		return result.Error
	}

	fmt.Printf("Success:\n")
	fmt.Printf("  Syntheses created:  %d\n", result.SynthesisCount)
	fmt.Printf("  Lessons superseded: %d\n", result.SupersededCount)
	fmt.Printf("\nWiki synthesis pages under: %s/synthesis/\n", cfg.WikiDir)
	return nil
}
