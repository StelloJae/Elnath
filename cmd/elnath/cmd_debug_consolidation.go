package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/ambient"
	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/learning"
)

// debugConsolidation handles `elnath debug consolidation <subcommand>`.
// Currently only `run [--force]` is implemented; `show` and `history` will
// follow once the flywheel has real data to report against.
func debugConsolidation(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return debugConsolidationShow()
	}
	switch args[0] {
	case "show":
		return debugConsolidationShow()
	case "run":
		return debugConsolidationRun(ctx, args[1:])
	case "help", "-h", "--help":
		return debugConsolidationUsage()
	default:
		return fmt.Errorf("debug consolidation: unknown subcommand %q (try: elnath debug consolidation help)", args[0])
	}
}

func debugConsolidationUsage() error {
	fmt.Fprintf(os.Stdout, `Usage: elnath debug consolidation [subcommand]

Subcommands:
  show (default) Report last run, gate status, active lesson count, and
                 existing synthesis pages.
  run [--force]  Execute one consolidation run. --force bypasses the time
                 and session gates but still respects an active lock.
  help           Show this help

State file: <data_dir>/consolidation_state.json
Lock file:  <data_dir>/.consolidate-lock
`)
	return nil
}

func debugConsolidationShow() error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("debug consolidation show: load config: %w", err)
	}

	statePath := filepath.Join(cfg.DataDir, "consolidation_state.json")
	state, err := learning.LoadConsolidationState(statePath)
	if err != nil {
		return fmt.Errorf("debug consolidation show: load state: %w", err)
	}

	lockPath := filepath.Join(cfg.DataDir, ".consolidate-lock")
	gate := learning.NewGate(lockPath,
		learning.WithMinInterval(24*time.Hour),
		learning.WithMinSessions(0),
		learning.WithHolderStale(60*time.Minute),
	)
	now := time.Now()
	lastAt, _ := gate.LastConsolidatedAt()
	ready, reason := gate.ShouldRun(now)

	lessonsPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
	lessonStore := learning.NewStore(lessonsPath)
	allLessons, _ := lessonStore.List()
	activeCount := 0
	supersededCount := 0
	for _, l := range allLessons {
		if l.SupersededBy == "" {
			activeCount++
		} else {
			supersededCount++
		}
	}

	synthGlob := filepath.Join(cfg.WikiDir, "synthesis", "*", "*.md")
	synthesisPages, _ := filepath.Glob(synthGlob)

	nextFire := ambient.NextDailyRun(now, ambient.TimeOfDay{Hour: 4, Minute: 0})

	fmt.Printf("Consolidation Status\n\n")
	fmt.Printf("  Data dir:          %s\n", cfg.DataDir)
	fmt.Printf("  Wiki dir:          %s\n", cfg.WikiDir)
	fmt.Printf("\n")
	fmt.Printf("  Gate:              %s\n", gateStatus(ready, reason))
	if !lastAt.IsZero() {
		fmt.Printf("  Last mtime:        %s (%s ago)\n", lastAt.Format(time.RFC3339), now.Sub(lastAt).Truncate(time.Second))
	} else {
		fmt.Printf("  Last mtime:        (never)\n")
	}
	fmt.Printf("  Next daily fire:   %s (in %s)\n", now.Add(nextFire).Format(time.RFC3339), nextFire.Truncate(time.Second))
	fmt.Printf("\n")
	fmt.Printf("  Lessons active:    %d\n", activeCount)
	fmt.Printf("  Lessons superseded:%d\n", supersededCount)
	fmt.Printf("  Synthesis pages:   %d\n", len(synthesisPages))
	fmt.Printf("\n")
	fmt.Printf("  Run count:         %d\n", state.RunCount)
	fmt.Printf("  Success count:     %d\n", state.SuccessCount)
	if !state.LastRunAt.IsZero() {
		fmt.Printf("  Last run:          %s\n", state.LastRunAt.Format(time.RFC3339))
	}
	if !state.LastSuccessAt.IsZero() {
		fmt.Printf("  Last success:      %s\n", state.LastSuccessAt.Format(time.RFC3339))
	}
	if state.LastError != "" {
		fmt.Printf("  Last error:        %s\n", state.LastError)
	}
	fmt.Printf("  Last run produced: %d syntheses, %d lessons superseded (from %d active)\n",
		state.LastSynthCount, state.LastSuperseded, state.LastActiveCount)
	return nil
}

func gateStatus(ready bool, reason string) string {
	if ready {
		return "READY (" + reason + ")"
	}
	return "BLOCKED (" + reason + ")"
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
	if deps.wikiDB != nil {
		defer deps.wikiDB.Close()
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
