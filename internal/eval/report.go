package eval

import "fmt"

// Diff compares two scorecards at overall and per-track level.
func Diff(current, baseline *Scorecard) (DiffSummary, error) {
	if current == nil || baseline == nil {
		return DiffSummary{}, fmt.Errorf("diff scorecards: scorecard is nil")
	}
	if err := current.Validate(); err != nil {
		return DiffSummary{}, err
	}
	if err := baseline.Validate(); err != nil {
		return DiffSummary{}, err
	}
	if err := ValidateComparableTaskRuns(current, baseline); err != nil {
		return DiffSummary{}, err
	}

	currentSummary := current.Summary()
	baselineSummary := baseline.Summary()

	diff := DiffSummary{
		Current:               currentSummary,
		Baseline:              baselineSummary,
		SuccessRateDelta:      currentSummary.SuccessRate - baselineSummary.SuccessRate,
		RegressionRateDelta:   currentSummary.RegressionRate - baselineSummary.RegressionRate,
		VerificationPassDelta: currentSummary.VerificationPassRate - baselineSummary.VerificationPassRate,
		RecoverySuccessDelta:  currentSummary.RecoverySuccessRate - baselineSummary.RecoverySuccessRate,
		ByTrack:               make(map[Track]TrackDelta),
	}

	for _, track := range []Track{TrackBrownfieldFeature, TrackBugfix, TrackGreenfield} {
		diff.ByTrack[track] = TrackDelta{
			Current:               currentSummary.ByTrack[track],
			Baseline:              baselineSummary.ByTrack[track],
			SuccessRateDelta:      currentSummary.ByTrack[track].SuccessRate - baselineSummary.ByTrack[track].SuccessRate,
			RegressionRateDelta:   currentSummary.ByTrack[track].RegressionRate - baselineSummary.ByTrack[track].RegressionRate,
			VerificationPassDelta: currentSummary.ByTrack[track].VerificationPassRate - baselineSummary.ByTrack[track].VerificationPassRate,
			RecoverySuccessDelta:  currentSummary.ByTrack[track].RecoverySuccessRate - baselineSummary.ByTrack[track].RecoverySuccessRate,
		}
	}

	return diff, nil
}
