package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/stello/elnath/internal/eval"
)

func cmdEval(_ context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Println(`Usage: elnath eval <subcommand> <file>

Subcommands:
  validate <corpus.json>     Validate a benchmark corpus file
  benchmark <plan.json>      Run a benchmark plan and print MC2 metrics
  summarize <scorecard.json> Summarize a benchmark scorecard
  diff <current.json> <baseline.json> Compare two scorecards
  report <corpus.json> <current.json> <baseline.json> <output.md> Write a markdown benchmark report
  gate-month2 <corpus.json> <current.json> <baseline.json> Evaluate Month-2 brownfield proof gate
  month3-gate <current1.json> <current2.json> <current3.json> <baseline.json> Evaluate Month-3 gate
  rules <corpus.json> <scorecard.json> Check anti-vanity benchmark rules
  run-baseline <plan.json>   Execute a baseline runner plan and write a scorecard
  run-current <plan.json>    Execute a current-system runner plan and write a scorecard
  scaffold-baseline <output.json>     Write a baseline runner scaffold
  scaffold-current <output.json>      Write a current-system runner scaffold
  run-v2 <corpus.v2.json> <output_dir> Phase 7.3 self-improvement benchmark (stub execution)`)
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
	case "benchmark":
		if len(args) < 2 {
			return fmt.Errorf("usage: elnath eval benchmark <plan.json>")
		}
		plan, err := eval.LoadBaselineRunPlan(args[1])
		if err != nil {
			return err
		}
		scorecard, err := eval.RunBaselinePlan(plan)
		if err != nil {
			return err
		}
		if err := scorecard.Validate(); err != nil {
			return fmt.Errorf("benchmark: scorecard validate: %w", err)
		}
		summary := scorecard.Summary()
		fmt.Printf("Benchmark complete: system=%s baseline=%s tasks=%d\n", scorecard.System, scorecard.Baseline, summary.Total)
		fmt.Printf("MC2 metrics:\n")
		fmt.Printf("  success_rate           = %.4f\n", summary.SuccessRate)
		fmt.Printf("  success_and_verified_rate = %.4f\n", summary.SuccessAndVerifiedRate)
		fmt.Printf("  intervention_rate      = %.4f\n", summary.InterventionRate)
		fmt.Printf("  intervention_mean      = %.4f\n", summary.InterventionMean)
		fmt.Printf("  verification_pass_rate = %.4f\n", summary.VerificationPassRate)
		fmt.Printf("  recovery_success_rate  = %.4f\n", summary.RecoverySuccessRate)
		fmt.Printf("  success_duration_mean  = %.4f\n", summary.SuccessDurationMean)
		fmt.Printf("  regression_rate        = %.4f\n", summary.RegressionRate)
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
		fmt.Printf("Overall: total=%d success=%d success_rate=%.2f success_and_verified_rate=%.2f intervention_rate=%.2f intervention_mean=%.2f\n",
			summary.Total, summary.Successes, summary.SuccessRate, summary.SuccessAndVerifiedRate, summary.InterventionRate, summary.InterventionMean)
		fmt.Printf("Verification: pass_rate=%.2f recovery_success_rate=%.2f regression_rate=%.4f success_duration_mean=%.2f\n",
			summary.VerificationPassRate, summary.RecoverySuccessRate, summary.RegressionRate, summary.SuccessDurationMean)
		for _, track := range []eval.Track{eval.TrackBrownfieldFeature, eval.TrackBugfix, eval.TrackGreenfield, eval.TrackResearch} {
			trackSummary, ok := summary.ByTrack[track]
			if !ok || trackSummary.Total == 0 {
				continue
			}
			fmt.Printf("Track %s: total=%d success=%d success_rate=%.2f success_and_verified_rate=%.2f intervention_rate=%.2f intervention_mean=%.2f verification_pass_rate=%.2f recovery_success_rate=%.2f regression_rate=%.4f success_duration_mean=%.2f\n",
				track, trackSummary.Total, trackSummary.Successes, trackSummary.SuccessRate, trackSummary.SuccessAndVerifiedRate, trackSummary.InterventionRate, trackSummary.InterventionMean, trackSummary.VerificationPassRate, trackSummary.RecoverySuccessRate, trackSummary.RegressionRate, trackSummary.SuccessDurationMean)
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
		fmt.Printf("Overall delta: success_rate_delta=%.2f success_and_verified_rate_delta=%.2f verification_pass_delta=%.2f recovery_success_delta=%.2f intervention_mean_delta=%.2f success_duration_mean_delta=%.2f regression_rate_delta=%.4f\n",
			diff.SuccessRateDelta, diff.SuccessAndVerifiedRateDelta, diff.VerificationPassDelta, diff.RecoverySuccessDelta, diff.InterventionMeanDelta, diff.SuccessDurationMeanDelta, diff.RegressionRateDelta)
		for _, track := range []eval.Track{eval.TrackBrownfieldFeature, eval.TrackBugfix, eval.TrackGreenfield, eval.TrackResearch} {
			trackDiff := diff.ByTrack[track]
			if trackDiff.Current.Total == 0 && trackDiff.Baseline.Total == 0 {
				continue
			}
			fmt.Printf("Track %s delta: success_rate_delta=%.2f success_and_verified_rate_delta=%.2f verification_pass_delta=%.2f recovery_success_delta=%.2f intervention_mean_delta=%.2f success_duration_mean_delta=%.2f regression_rate_delta=%.4f\n",
				track, trackDiff.SuccessRateDelta, trackDiff.SuccessAndVerifiedRateDelta, trackDiff.VerificationPassDelta, trackDiff.RecoverySuccessDelta, trackDiff.InterventionMeanDelta, trackDiff.SuccessDurationMeanDelta, trackDiff.RegressionRateDelta)
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
	case "month3-gate":
		if len(args) < 5 {
			return fmt.Errorf("usage: elnath eval month3-gate <current1.json> <current2.json> <current3.json> <baseline.json>")
		}
		scorecards := make([]*eval.Scorecard, 0, len(args)-2)
		for _, path := range args[1 : len(args)-1] {
			scorecard, err := eval.LoadScorecard(path)
			if err != nil {
				return err
			}
			scorecards = append(scorecards, scorecard)
		}
		baseline, err := eval.LoadScorecard(args[len(args)-1])
		if err != nil {
			return err
		}
		gate, err := eval.EvaluateMonth3Gate(scorecards, baseline)
		if err != nil {
			return err
		}
		fmt.Printf("Month 3 gate: %s (margin: %.0f%%)\n", passFail(gate.Pass), gate.HardGateMargin*100)
		for i, h1 := range gate.H1Results {
			printH1Result(fmt.Sprintf("Run %d H1", i+1), h1)
		}
		printH1Result("Average H1 (no margin)", gate.AverageH1Result)
		printH1Result(fmt.Sprintf("Aggregate H1 (%.0f%% margin)", gate.HardGateMargin*100), gate.MarginH1Result)
		fmt.Printf("Stability (diagnostic): %s\n", passFail(gate.StabilityPass))
		for _, reason := range gate.Reasons {
			fmt.Printf("reason: %s\n", reason)
		}
		if !gate.Pass {
			return fmt.Errorf("month 3 gate failed")
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
	case "run-v2":
		if len(args) < 3 {
			return fmt.Errorf("usage: elnath eval run-v2 <corpus.v2.json> <output_dir>")
		}
		return cmdEvalRunV2(args[1], args[2])
	default:
		return fmt.Errorf("unknown eval subcommand: %s", args[0])
	}
}

// cmdEvalRunV2 runs one Phase 7.3 benchmark cycle: loads the v2 corpus,
// executes 10 training+held-out runs, and writes a timeseries JSON + a
// human-readable Markdown report under the given output directory. On
// STRONG_PASS/PASS the function returns nil (exit 0); on FAIL it returns
// an error so the CLI exits non-zero.
func cmdEvalRunV2(corpusPath, outputDir string) error {
	corpus, err := eval.LoadCorpus(corpusPath)
	if err != nil {
		return err
	}
	if corpus.Version != "v2" {
		return fmt.Errorf("run-v2: corpus version %q is not v2", corpus.Version)
	}

	series, runErr := eval.RunV2(eval.V2RunOptions{
		Corpus:    corpus,
		OutputDir: outputDir,
	})
	if runErr != nil && series == nil {
		return runErr
	}

	// Persist timeseries as JSON alongside the report for downstream
	// tooling (trend analysis, CI step, etc).
	if err := eval.WriteV2TimeSeries(filepath.Join(outputDir, "timeseries.json"), series); err != nil {
		return fmt.Errorf("run-v2: write timeseries: %w", err)
	}

	reportPath := filepath.Join(outputDir, "report.md")
	if _, err := eval.RenderV2Report(series, eval.V2ReportOptions{
		Corpus:     corpus,
		OutputPath: reportPath,
	}); err != nil {
		return fmt.Errorf("run-v2: render report: %w", err)
	}

	fmt.Printf("Phase 7.3 v2 cycle complete\n")
	fmt.Printf("  verdict:       %s\n", series.Verdict)
	fmt.Printf("  spearman:      %.4f (is_constant=%t)\n", series.SpearmanCoeff, series.IsConstant)
	fmt.Printf("  first3 avg:    %.4f\n", series.First3Avg)
	fmt.Printf("  last3 avg:     %.4f\n", series.Last3Avg)
	fmt.Printf("  report:        %s\n", reportPath)

	if runErr != nil {
		return runErr
	}
	if series.Verdict == eval.V2VerdictFail {
		return fmt.Errorf("run-v2: FAIL (see report)")
	}
	return nil
}

func printH1Result(label string, result eval.H1Result) {
	fmt.Printf("%s: %s\n", label, passFail(result.Pass))
	for _, key := range []string{"T_brownfield", "T_bugfix", "T_intervent", "T_regression", "T_time"} {
		check := result.ThresholdResults[key]
		fmt.Printf("  %s: %s current=%.4f baseline=%.4f threshold=%.4f\n", key, passFail(check.Pass), check.Current, check.Baseline, check.Threshold)
	}
	fmt.Printf("  hard_gate_pass=%t soft_gate_count=%d soft_gate_pass=%t\n", result.HardGatePass, result.SoftGateCount, result.SoftGatePass)
}

func passFail(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}
