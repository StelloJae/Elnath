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
