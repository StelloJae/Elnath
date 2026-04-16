package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
)

func cmdDebug(_ context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		return printDebugUsage()
	}

	switch args[0] {
	case "info":
		return debugInfo()
	case "cost":
		return debugCost(args[1:])
	case "consolidation":
		return debugConsolidation(context.Background(), args[1:])
	default:
		return fmt.Errorf("debug: unknown subcommand %q (try: elnath debug help)", args[0])
	}
}

func printDebugUsage() error {
	fmt.Fprintf(os.Stdout, `Usage: elnath debug <subcommand>

Subcommands:
  info                    System diagnostics and data counts
  cost [--days N]         Cost summary (default: last 30 days)
  consolidation <action>  Lesson consolidation controls (run [--force], help)
  help                    Show this help
`)
	return nil
}

func debugInfo() error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("debug: load config: %w", err)
	}

	lessonsPath := filepath.Join(cfg.DataDir, "lessons.jsonl")
	lessonStore := learning.NewStore(lessonsPath)
	lessons, err := lessonStore.List()
	if err != nil {
		return fmt.Errorf("debug: list lessons: %w", err)
	}

	outcomePath := filepath.Join(cfg.DataDir, "outcomes.jsonl")
	outcomeStore := learning.NewOutcomeStore(outcomePath)
	outcomes, err := outcomeStore.Recent(0)
	if err != nil {
		return fmt.Errorf("debug: list outcomes: %w", err)
	}

	sessionGlob := filepath.Join(cfg.DataDir, "sessions", "*.jsonl")
	sessionFiles, err := filepath.Glob(sessionGlob)
	if err != nil {
		return fmt.Errorf("debug: glob sessions: %w", err)
	}

	provider := "anthropic"
	model := "claude-sonnet-4-6"
	if cfg.Anthropic.Model != "" {
		model = llm.ResolveModel(cfg.Anthropic.Model)
	}

	fmt.Fprintf(os.Stdout, "Elnath Debug Info\n\n")
	fmt.Fprintf(os.Stdout, "  Version:     %s\n", version)
	fmt.Fprintf(os.Stdout, "  Config:      %s\n", cfgPath)
	fmt.Fprintf(os.Stdout, "  Data dir:    %s\n", cfg.DataDir)
	fmt.Fprintf(os.Stdout, "  Wiki dir:    %s\n", cfg.WikiDir)
	fmt.Fprintf(os.Stdout, "  Provider:    %s\n", provider)
	fmt.Fprintf(os.Stdout, "  Model:       %s\n", model)
	fmt.Fprintf(os.Stdout, "\n")
	fmt.Fprintf(os.Stdout, "  Lessons:     %d stored\n", len(lessons))
	fmt.Fprintf(os.Stdout, "  Outcomes:    %d recorded\n", len(outcomes))
	fmt.Fprintf(os.Stdout, "  Sessions:    %d\n", len(sessionFiles))
	return nil
}

func debugCost(args []string) error {
	days := 30
	for i, arg := range args {
		if arg == "--days" && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("debug: cost: invalid --days value %q", args[i+1])
			}
			days = n
		}
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("debug: load config: %w", err)
	}

	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("debug: open db: %w", err)
	}
	defer db.Close()

	tracker, err := llm.NewUsageTracker(db.Main)
	if err != nil {
		return fmt.Errorf("debug: usage tracker: %w", err)
	}

	ctx := context.Background()
	allRecords, err := tracker.RecentRecords(ctx, 10000)
	if err != nil {
		return fmt.Errorf("debug: recent records: %w", err)
	}

	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	var filtered []llm.UsageRecord
	for _, r := range allRecords {
		if r.Timestamp.After(cutoff) {
			filtered = append(filtered, r)
		}
	}

	type modelStat struct {
		cost  float64
		calls int
	}
	byModel := make(map[string]*modelStat)
	var totalCost float64
	for _, r := range filtered {
		totalCost += r.CostUSD
		if byModel[r.Model] == nil {
			byModel[r.Model] = &modelStat{}
		}
		byModel[r.Model].cost += r.CostUSD
		byModel[r.Model].calls++
	}

	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool {
		return byModel[models[i]].cost > byModel[models[j]].cost
	})

	fmt.Fprintf(os.Stdout, "Cost Summary (last %d days)\n\n", days)
	fmt.Fprintf(os.Stdout, "  Total:    $%.2f\n", totalCost)
	fmt.Fprintf(os.Stdout, "  Records:  %d\n", len(filtered))

	if len(models) > 0 {
		fmt.Fprintf(os.Stdout, "\n  By Model:\n")
		for _, m := range models {
			s := byModel[m]
			fmt.Fprintf(os.Stdout, "    %-28s $%.2f (%d calls)\n", m, s.cost, s.calls)
		}
	}

	recent := filtered
	if len(recent) > 5 {
		recent = recent[:5]
	}
	if len(recent) > 0 {
		fmt.Fprintf(os.Stdout, "\n  Recent (last %d):\n", len(recent))
		for _, r := range recent {
			fmt.Fprintf(os.Stdout, "    %s  %-28s $%.2f  (%.1fK in / %.1fK out)\n",
				r.Timestamp.Local().Format("2006-01-02 15:04"),
				r.Model,
				r.CostUSD,
				float64(r.InputTokens)/1000,
				float64(r.OutputTokens)/1000,
			)
		}
	}

	return nil
}
