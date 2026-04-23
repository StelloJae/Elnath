package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/wiki"
)

func cmdExplain(_ context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		return printExplainUsage()
	}

	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("explain: load config: %w", err)
	}

	outcomePath := filepath.Join(cfg.DataDir, "outcomes.jsonl")
	outcomeStore := learning.NewOutcomeStore(outcomePath)
	routingAdvisor := learning.NewRoutingAdvisor(outcomeStore)

	var wikiStore *wiki.Store
	if cfg.WikiDir != "" {
		if ws, err := wiki.NewStore(cfg.WikiDir); err == nil {
			wikiStore = ws
		}
	}

	switch args[0] {
	case "last":
		return explainLast(outcomeStore, wikiStore, routingAdvisor)
	case "history":
		n := 10
		if len(args) > 1 {
			parsed, err := strconv.Atoi(args[1])
			if err != nil || parsed <= 0 {
				return fmt.Errorf("explain: history: invalid count %q", args[1])
			}
			n = parsed
		}
		return explainHistory(outcomeStore, n)
	default:
		return fmt.Errorf("explain: unknown subcommand %q (try: elnath explain help)", args[0])
	}
}

func printExplainUsage() error {
	fmt.Fprintf(os.Stdout, `Usage: elnath explain <subcommand>

Subcommands:
  last           Show the most recent routing decision
  history [n]    Show recent n routing decisions (default 10)
  help           Show this help
`)
	return nil
}

func explainLast(outcomeStore *learning.OutcomeStore, wikiStore *wiki.Store, advisor *learning.RoutingAdvisor) error {
	records, err := outcomeStore.Recent(1)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	if len(records) == 0 {
		fmt.Fprintf(os.Stdout, "No routing decisions recorded yet.\n")
		return nil
	}

	r := records[0]

	pref, _ := wiki.LoadWorkflowPreference(wikiStore, r.ProjectID)

	stats, _ := advisor.ProjectStats(r.ProjectID, 30)

	resultMark := "x"
	if r.Success {
		resultMark = "✓"
	}

	fmt.Fprintf(os.Stdout, "Last routing decision (%s)\n\n", r.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"))

	if r.InputSnippet != "" {
		snippet := r.InputSnippet
		if len([]rune(snippet)) == 100 {
			snippet += "..."
		}
		fmt.Fprintf(os.Stdout, "  Input:     %q\n", snippet)
	}
	fmt.Fprintf(os.Stdout, "  Intent:    %s\n", r.Intent)
	fmt.Fprintf(os.Stdout, "  Workflow:  %s\n", r.Workflow)
	fmt.Fprintf(os.Stdout, "  Result:    %s %s (%d iterations, %.1fs)\n\n",
		resultMark, finishLabel(r.Success, r.FinishReason), r.Iterations, r.Duration)

	fmt.Fprintf(os.Stdout, "  Why this workflow?\n")
	if r.PreferenceUsed && pref != nil {
		if pw := pref.PreferredWorkflow(r.Intent); pw != "" {
			fmt.Fprintf(os.Stdout, "    • Preference: %s → %s\n", r.Intent, pw)
		} else {
			fmt.Fprintf(os.Stdout, "    • Preference applied for intent %s\n", r.Intent)
		}
	}
	fmt.Fprintf(os.Stdout, "    • Context: existing_code=%v, estimated_files=%d\n\n",
		r.ExistingCode, r.EstimatedFiles)

	if len(stats) > 0 {
		fmt.Fprintf(os.Stdout, "  Project %q routing stats (last 30):\n", r.ProjectID)
		intents := make([]string, 0, len(stats))
		for intent := range stats {
			intents = append(intents, intent)
		}
		sort.Strings(intents)
		for _, intent := range intents {
			wfStats := stats[intent]
			wfNames := make([]string, 0, len(wfStats))
			for wf := range wfStats {
				wfNames = append(wfNames, wf)
			}
			sort.Slice(wfNames, func(i, j int) bool {
				si, sj := wfStats[wfNames[i]], wfStats[wfNames[j]]
				ri := float64(si.Success) / float64(si.Total)
				rj := float64(sj.Success) / float64(sj.Total)
				return ri > rj
			})
			parts := make([]string, 0, len(wfNames))
			for _, wf := range wfNames {
				s := wfStats[wf]
				pct := int(100 * float64(s.Success) / float64(s.Total))
				parts = append(parts, fmt.Sprintf("%s %d%% (%d/%d)", wf, pct, s.Success, s.Total))
			}
			fmt.Fprintf(os.Stdout, "    %-14s %s\n", intent+":", strings.Join(parts, ", "))
		}
	}

	return nil
}

func explainHistory(outcomeStore *learning.OutcomeStore, n int) error {
	records, err := outcomeStore.Recent(n)
	if err != nil {
		return fmt.Errorf("explain: %w", err)
	}
	if len(records) == 0 {
		fmt.Fprintf(os.Stdout, "No routing decisions recorded yet.\n")
		return nil
	}

	fmt.Fprintf(os.Stdout, "Recent routing decisions:\n\n")
	fmt.Fprintf(os.Stdout, "  %2s  %-20s  %-14s  %-10s  %-8s  %s\n",
		"#", "Time", "Intent", "Workflow", "Result", "Duration")

	for i, r := range records {
		result := "success"
		if !r.Success {
			result = "failure"
		}
		fmt.Fprintf(os.Stdout, "  %2d  %-20s  %-14s  %-10s  %-8s  %.1fs\n",
			i+1,
			r.Timestamp.UTC().Format("2006-01-02 15:04:05"),
			r.Intent,
			r.Workflow,
			result,
			r.Duration,
		)
	}
	return nil
}

func finishLabel(success bool, reason string) string {
	if success {
		if reason == "unverified_inline" {
			return "success (unverified inline)"
		}
		return "success"
	}
	if reason != "" {
		return "failure (" + reason + ")"
	}
	return "failure"
}
