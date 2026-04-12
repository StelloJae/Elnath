package eval

import "fmt"

// GateResult is the result of a month gate check.
type GateResult struct {
	Pass     bool
	Reasons  []string
	Warnings []string
}

// EvaluateMonth2Gate checks whether Month 2 evidence is strong enough to continue.
func EvaluateMonth2Gate(corpus *Corpus, current, baseline *Scorecard) (GateResult, error) {
	if corpus == nil || current == nil || baseline == nil {
		return GateResult{}, fmt.Errorf("evaluate month2 gate: corpus/current/baseline must be non-nil")
	}
	if err := corpus.Validate(); err != nil {
		return GateResult{}, err
	}
	if err := current.Validate(); err != nil {
		return GateResult{}, err
	}
	if err := baseline.Validate(); err != nil {
		return GateResult{}, err
	}
	if err := ValidateScorecardCoverage(corpus, current, current.RepeatedRuns); err != nil {
		return GateResult{}, err
	}
	if err := ValidateScorecardCoverage(corpus, baseline, baseline.RepeatedRuns); err != nil {
		return GateResult{}, err
	}

	diff, err := Diff(current, baseline)
	if err != nil {
		return GateResult{}, err
	}

	result := GateResult{Pass: true}
	if diff.ByTrack[TrackBrownfieldFeature].Current.Total == 0 {
		result.Pass = false
		result.Reasons = append(result.Reasons, "no brownfield feature results present")
	}
	if diff.ByTrack[TrackBrownfieldFeature].SuccessRateDelta <= 0 {
		result.Pass = false
		result.Reasons = append(result.Reasons, "brownfield success rate did not improve over baseline")
	}
	if diff.Current.VerificationPassRate < baseline.Summary().VerificationPassRate {
		result.Pass = false
		result.Reasons = append(result.Reasons, "verification pass rate regressed versus baseline")
	}
	expectedHoldout, currentHoldout := holdoutCoverage(corpus, current)
	_, baselineHoldout := holdoutCoverage(corpus, baseline)
	if expectedHoldout == 0 {
		result.Pass = false
		result.Reasons = append(result.Reasons, "holdout slice is missing")
	} else {
		if currentHoldout < expectedHoldout {
			result.Pass = false
			result.Reasons = append(result.Reasons, "current scorecard is missing holdout results")
		}
		if baselineHoldout < expectedHoldout {
			result.Pass = false
			result.Reasons = append(result.Reasons, "baseline scorecard is missing holdout results")
		}
	}
	if len(current.Summary().FailureFamilies) == 0 {
		result.Warnings = append(result.Warnings, "failure family data is empty")
	}
	return result, nil
}

// ThresholdCheck captures one H1 threshold evaluation.
type ThresholdCheck struct {
	Name      string
	Pass      bool
	Current   float64
	Baseline  float64
	Threshold float64
}

// H1Result is the result of the MC3 H1 pass rule.
type H1Result struct {
	Pass             bool
	ThresholdResults map[string]ThresholdCheck
	HardGatePass     bool
	SoftGateCount    int
	SoftGatePass     bool
}

// Month3GateResult is the result of the MC4 Month 3 gate.
type Month3GateResult struct {
	Pass            bool
	H1Results       []H1Result
	AverageH1Result H1Result
	AverageH1Pass   bool
	StabilityPass   bool
	Reasons         []string
}

// EvaluateH1PassRule applies the MC3 canonical threshold checks.
func EvaluateH1PassRule(current, baseline *Summary) H1Result {
	result := H1Result{ThresholdResults: make(map[string]ThresholdCheck, 5)}
	if current == nil || baseline == nil {
		return result
	}

	result.ThresholdResults["T_brownfield"] = thresholdTrackAtLeast(
		"T_brownfield",
		current.ByTrack[TrackBrownfieldFeature],
		baseline.ByTrack[TrackBrownfieldFeature],
	)
	result.ThresholdResults["T_bugfix"] = thresholdTrackAtLeast(
		"T_bugfix",
		current.ByTrack[TrackBugfix],
		baseline.ByTrack[TrackBugfix],
	)
	result.ThresholdResults["T_intervent"] = thresholdAtMost(
		"T_intervent",
		current.InterventionMean,
		baseline.InterventionMean,
		baseline.InterventionMean*0.80,
	)
	result.ThresholdResults["T_regression"] = thresholdAtMost(
		"T_regression",
		current.RegressionRate,
		baseline.RegressionRate,
		baseline.RegressionRate,
	)
	result.ThresholdResults["T_time"] = thresholdAtMost(
		"T_time",
		current.SuccessDurationMean,
		baseline.SuccessDurationMean,
		baseline.SuccessDurationMean*1.20,
	)

	result.HardGatePass = result.ThresholdResults["T_brownfield"].Pass && result.ThresholdResults["T_bugfix"].Pass
	for _, key := range []string{"T_intervent", "T_regression", "T_time"} {
		if result.ThresholdResults[key].Pass {
			result.SoftGateCount++
		}
	}
	result.SoftGatePass = result.SoftGateCount >= 2
	result.Pass = result.HardGatePass && result.SoftGatePass
	return result
}

// EvaluateMonth3Gate checks whether three benchmark runs clear the Month 3 gate.
func EvaluateMonth3Gate(scorecards []*Scorecard, baseline *Scorecard) (Month3GateResult, error) {
	if baseline == nil {
		return Month3GateResult{}, fmt.Errorf("evaluate month3 gate: baseline must be non-nil")
	}
	if len(scorecards) < 3 {
		return Month3GateResult{}, fmt.Errorf("evaluate month3 gate: at least 3 scorecards are required")
	}
	if err := baseline.Validate(); err != nil {
		return Month3GateResult{}, err
	}

	baselineSummary := baseline.Summary()
	runResults := make([]H1Result, 0, len(scorecards))
	summaries := make([]Summary, 0, len(scorecards))
	allRunsPass := true
	firstPass := false
	stabilityPass := true

	for i, scorecard := range scorecards {
		if scorecard == nil {
			return Month3GateResult{}, fmt.Errorf("evaluate month3 gate: scorecards[%d] is nil", i)
		}
		if err := scorecard.Validate(); err != nil {
			return Month3GateResult{}, err
		}
		if err := ValidateComparableTaskRuns(scorecard, baseline); err != nil {
			return Month3GateResult{}, err
		}

		summary := scorecard.Summary()
		h1 := EvaluateH1PassRule(&summary, &baselineSummary)
		runResults = append(runResults, h1)
		summaries = append(summaries, summary)
		allRunsPass = allRunsPass && h1.Pass
		if i == 0 {
			firstPass = h1.Pass
		} else if h1.Pass != firstPass {
			stabilityPass = false
		}
	}

	averageSummary := averageSummary(summaries)
	averageH1 := EvaluateH1PassRule(&averageSummary, &baselineSummary)
	result := Month3GateResult{
		H1Results:       runResults,
		AverageH1Result: averageH1,
		AverageH1Pass:   averageH1.Pass,
		StabilityPass:   stabilityPass,
	}
	if !allRunsPass {
		result.Reasons = append(result.Reasons, "not all individual H1 runs passed")
	}
	if !stabilityPass {
		result.Reasons = append(result.Reasons, "individual H1 run outcomes are unstable")
	}
	if !averageH1.Pass {
		result.Reasons = append(result.Reasons, "average H1 result did not pass")
	}
	result.Pass = allRunsPass && stabilityPass && averageH1.Pass
	return result, nil
}

func thresholdAtLeast(name string, current, baseline float64) ThresholdCheck {
	return ThresholdCheck{
		Name:      name,
		Pass:      current >= baseline,
		Current:   current,
		Baseline:  baseline,
		Threshold: baseline,
	}
}

func thresholdTrackAtLeast(name string, current, baseline TrackSummary) ThresholdCheck {
	check := thresholdAtLeast(name, current.SuccessAndVerifiedRate, baseline.SuccessAndVerifiedRate)
	check.Pass = current.Total > 0 && baseline.Total > 0 && check.Pass
	return check
}

func thresholdAtMost(name string, current, baseline, threshold float64) ThresholdCheck {
	return ThresholdCheck{
		Name:      name,
		Pass:      current <= threshold,
		Current:   current,
		Baseline:  baseline,
		Threshold: threshold,
	}
}

func averageSummary(summaries []Summary) Summary {
	average := Summary{ByTrack: make(map[Track]TrackSummary)}
	if len(summaries) == 0 {
		return average
	}

	count := float64(len(summaries))
	for _, summary := range summaries {
		average.SuccessRate += summary.SuccessRate
		average.SuccessAndVerifiedRate += summary.SuccessAndVerifiedRate
		average.InterventionRate += summary.InterventionRate
		average.InterventionMean += summary.InterventionMean
		average.VerificationPassRate += summary.VerificationPassRate
		average.RecoverySuccessRate += summary.RecoverySuccessRate
		average.RegressionRate += summary.RegressionRate
		average.SuccessDurationMean += summary.SuccessDurationMean
		for track, trackSummary := range summary.ByTrack {
			current := average.ByTrack[track]
			current.Total += trackSummary.Total
			current.SuccessRate += trackSummary.SuccessRate
			current.SuccessAndVerifiedRate += trackSummary.SuccessAndVerifiedRate
			current.InterventionRate += trackSummary.InterventionRate
			current.InterventionMean += trackSummary.InterventionMean
			current.VerificationPassRate += trackSummary.VerificationPassRate
			current.RecoverySuccessRate += trackSummary.RecoverySuccessRate
			current.RegressionRate += trackSummary.RegressionRate
			current.SuccessDurationMean += trackSummary.SuccessDurationMean
			average.ByTrack[track] = current
		}
	}

	average.SuccessRate /= count
	average.SuccessAndVerifiedRate /= count
	average.InterventionRate /= count
	average.InterventionMean /= count
	average.VerificationPassRate /= count
	average.RecoverySuccessRate /= count
	average.RegressionRate /= count
	average.SuccessDurationMean /= count
	for track, trackSummary := range average.ByTrack {
		trackSummary.SuccessRate /= count
		trackSummary.SuccessAndVerifiedRate /= count
		trackSummary.InterventionRate /= count
		trackSummary.InterventionMean /= count
		trackSummary.VerificationPassRate /= count
		trackSummary.RecoverySuccessRate /= count
		trackSummary.RegressionRate /= count
		trackSummary.SuccessDurationMean /= count
		average.ByTrack[track] = trackSummary
	}
	return average
}
