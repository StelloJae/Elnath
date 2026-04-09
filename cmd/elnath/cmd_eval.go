package main

import (
	"context"
	"fmt"

	"github.com/stello/elnath/internal/eval"
)

func cmdEval(_ context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Println(`Usage: elnath eval <subcommand> <file>

Subcommands:
  validate <corpus.json>     Validate a benchmark corpus file
  summarize <scorecard.json> Summarize a benchmark scorecard
  diff <current.json> <baseline.json> Compare two scorecards
  report <corpus.json> <current.json> <baseline.json> <output.md> Write a markdown benchmark report
  gate-month2 <corpus.json> <current.json> <baseline.json> Evaluate Month-2 brownfield proof gate
  rules <corpus.json> <scorecard.json> Check anti-vanity benchmark rules
  run-baseline <plan.json>   Execute a baseline runner plan and write a scorecard
  run-current <plan.json>    Execute a current-system runner plan and write a scorecard
  scaffold-baseline <output.json>     Write a baseline runner scaffold
  scaffold-current <output.json>      Write a current-system runner scaffold`)
		return nil
	}

	switch args[0] {
	case "validate":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath eval validate <corpus.json>")
		}
		corpus, err := eval.LoadCorpus(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Corpus OK: version=%s tasks=%d\n", corpus.Version, len(corpus.Tasks))
		return nil
	case "summarize":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath eval summarize <scorecard.json>")
		}
		scorecard, err := eval.LoadScorecard(args[1])
		if err != nil {
			return err
		}
		summary := scorecard.Summary()
		fmt.Printf("System: %s\n", scorecard.System)
		if scorecard.Baseline != "" {
			fmt.Printf("Baseline: %s\n", scorecard.Baseline)
		}
		fmt.Printf("Overall: total=%d success=%d success_rate=%.2f intervention_rate=%.2f\n",
			summary.Total, summary.Successes, summary.SuccessRate, summary.InterventionRate)
		fmt.Printf("Verification: pass_rate=%.2f recovery_success_rate=%.2f\n",
			summary.VerificationPassRate, summary.RecoverySuccessRate)
		for _, track := range []eval.Track{eval.TrackBrownfieldFeature, eval.TrackBugfix, eval.TrackGreenfield} {
			trackSummary, ok := summary.ByTrack[track]
			if !ok || trackSummary.Total == 0 {
				continue
			}
			fmt.Printf("Track %s: total=%d success=%d success_rate=%.2f intervention_rate=%.2f verification_pass_rate=%.2f recovery_success_rate=%.2f\n",
				track, trackSummary.Total, trackSummary.Successes, trackSummary.SuccessRate, trackSummary.InterventionRate, trackSummary.VerificationPassRate, trackSummary.RecoverySuccessRate)
		}
		if len(summary.FailureFamilies) > 0 {
			fmt.Println("Failure families:")
			for family, count := range summary.FailureFamilies {
				fmt.Printf("  %s=%d\n", family, count)
			}
		}
		return nil
	case "diff":
		if len(args) < 3 {
			return fmt.Errorf("usage: elnath eval diff <current.json> <baseline.json>")
		}
		current, err := eval.LoadScorecard(args[1])
		if err != nil {
			return err
		}
		baseline, err := eval.LoadScorecard(args[2])
		if err != nil {
			return err
		}
		diff, err := eval.Diff(current, baseline)
		if err != nil {
			return err
		}
		fmt.Printf("Overall delta: success_rate_delta=%.2f verification_pass_delta=%.2f recovery_success_delta=%.2f\n",
			diff.SuccessRateDelta, diff.VerificationPassDelta, diff.RecoverySuccessDelta)
		for _, track := range []eval.Track{eval.TrackBrownfieldFeature, eval.TrackBugfix, eval.TrackGreenfield} {
			trackDiff := diff.ByTrack[track]
			if trackDiff.Current.Total == 0 && trackDiff.Baseline.Total == 0 {
				continue
			}
			fmt.Printf("Track %s delta: success_rate_delta=%.2f verification_pass_delta=%.2f recovery_success_delta=%.2f\n",
				track, trackDiff.SuccessRateDelta, trackDiff.VerificationPassDelta, trackDiff.RecoverySuccessDelta)
		}
		return nil
	case "report":
		if len(args) < 5 {
			return fmt.Errorf("usage: elnath eval report <corpus.json> <current.json> <baseline.json> <output.md>")
		}
		corpus, err := eval.LoadCorpus(args[1])
		if err != nil {
			return err
		}
		current, err := eval.LoadScorecard(args[2])
		if err != nil {
			return err
		}
		baseline, err := eval.LoadScorecard(args[3])
		if err != nil {
			return err
		}
		if err := eval.WriteMarkdownReport(args[4], corpus, current, baseline); err != nil {
			return err
		}
		fmt.Printf("Benchmark report written: %s\n", args[4])
		return nil
	case "gate-month2":
		if len(args) < 4 {
			return fmt.Errorf("usage: elnath eval gate-month2 <corpus.json> <current.json> <baseline.json>")
		}
		corpus, err := eval.LoadCorpus(args[1])
		if err != nil {
			return err
		}
		current, err := eval.LoadScorecard(args[2])
		if err != nil {
			return err
		}
		baseline, err := eval.LoadScorecard(args[3])
		if err != nil {
			return err
		}
		gate, err := eval.EvaluateMonth2Gate(corpus, current, baseline)
		if err != nil {
			return err
		}
		if gate.Pass {
			fmt.Println("Month 2 gate: PASS")
		} else {
			fmt.Println("Month 2 gate: FAIL")
		}
		for _, reason := range gate.Reasons {
			fmt.Printf("reason: %s\n", reason)
		}
		for _, warning := range gate.Warnings {
			fmt.Printf("warning: %s\n", warning)
		}
		if !gate.Pass {
			return fmt.Errorf("month 2 gate failed")
		}
		return nil
	case "rules":
		if len(args) < 3 {
			return fmt.Errorf("usage: elnath eval rules <corpus.json> <scorecard.json>")
		}
		corpus, err := eval.LoadCorpus(args[1])
		if err != nil {
			return err
		}
		scorecard, err := eval.LoadScorecard(args[2])
		if err != nil {
			return err
		}
		violations := eval.CheckAntiVanityRules(corpus, scorecard)
		if len(violations) == 0 {
			fmt.Println("Anti-vanity rules OK")
			return nil
		}
		for _, violation := range violations {
			fmt.Printf("[%s] %s: %s\n", violation.Severity, violation.Rule, violation.Message)
		}
		return fmt.Errorf("anti-vanity rules failed: %d violation(s)", len(violations))
	case "run-baseline":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath eval run-baseline <plan.json>")
		}
		plan, err := eval.LoadBaselineRunPlan(args[1])
		if err != nil {
			return err
		}
		scorecard, err := eval.RunBaselinePlan(plan)
		if err != nil {
			return err
		}
		fmt.Printf("Baseline run complete: baseline=%s results=%d output=%s\n", scorecard.Baseline, len(scorecard.Results), plan.OutputPath)
		return nil
	case "run-current":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath eval run-current <plan.json>")
		}
		plan, err := eval.LoadBaselineRunPlan(args[1])
		if err != nil {
			return err
		}
		scorecard, err := eval.RunBaselinePlan(plan)
		if err != nil {
			return err
		}
		fmt.Printf("Current run complete: system=%s results=%d output=%s\n", scorecard.System, len(scorecard.Results), plan.OutputPath)
		return nil
	case "scaffold-baseline":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath eval scaffold-baseline <output.json>")
		}
		plan := eval.NewBaselineRunPlan("benchmarks/public-corpus.v1.json")
		if err := eval.WriteBaselineRunPlan(args[1], plan); err != nil {
			return err
		}
		fmt.Printf("Baseline scaffold written: %s\n", args[1])
		return nil
	case "scaffold-current":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath eval scaffold-current <output.json>")
		}
		plan := eval.NewCurrentRunPlan("benchmarks/public-corpus.v1.json")
		if err := eval.WriteBaselineRunPlan(args[1], plan); err != nil {
			return err
		}
		fmt.Printf("Current scaffold written: %s\n", args[1])
		return nil
	default:
		return fmt.Errorf("unknown eval subcommand: %s", args[0])
	}
}
