package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/scorecard"
)

// debugScorecard implements `elnath debug scorecard [--json]`.
//
// Default: compute the scorecard, append a JSON line to
// <data_dir>/scorecard/YYYY-MM-DD.jsonl, and print the Markdown report.
// With --json: print the JSON only; still appends to the daily file.
func debugScorecard(args []string) error {
	jsonOnly := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOnly = true
		case "-h", "--help", "help":
			fmt.Fprintln(os.Stdout, "Usage: elnath debug scorecard [--json]")
			return nil
		default:
			return fmt.Errorf("debug scorecard: unknown flag %q", a)
		}
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("debug scorecard: load config: %w", err)
	}

	paths := scorecard.SourcesPaths{
		OutcomesPath: filepath.Join(cfg.DataDir, "outcomes.jsonl"),
		LessonsPath:  filepath.Join(cfg.DataDir, "lessons.jsonl"),
		SynthesisDir: filepath.Join(cfg.WikiDir, "synthesis"),
		StatePath:    filepath.Join(cfg.DataDir, "consolidation_state.json"),
	}

	now := time.Now()
	report := scorecard.Compute(paths, now, version)

	outFile := scorecard.ScorecardFilePath(cfg.DataDir, now)
	if err := scorecard.AppendJSON(report, outFile); err != nil {
		return fmt.Errorf("debug scorecard: persist: %w", err)
	}

	if jsonOnly {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprint(os.Stdout, scorecard.RenderMarkdown(report))
	fmt.Fprintf(os.Stdout, "\n  Appended to: %s\n", outFile)
	return nil
}
