package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadScorecard reads and validates a scorecard file.
func LoadScorecard(path string) (*Scorecard, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load scorecard: %w", err)
	}

	var scorecard Scorecard
	if err := json.Unmarshal(data, &scorecard); err != nil {
		return nil, fmt.Errorf("load scorecard: parse json: %w", err)
	}

	if err := scorecard.Validate(); err != nil {
		return nil, err
	}
	return &scorecard, nil
}

// Validate checks scorecard structure.
func (s *Scorecard) Validate() error {
	if s == nil {
		return fmt.Errorf("validate scorecard: scorecard is nil")
	}
	if s.Version == "" {
		return fmt.Errorf("validate scorecard: version is required")
	}
	if s.System == "" {
		return fmt.Errorf("validate scorecard: system is required")
	}
	if len(s.Results) == 0 {
		return fmt.Errorf("validate scorecard: at least one result is required")
	}
	for i, result := range s.Results {
		if result.TaskID == "" {
			return fmt.Errorf("validate scorecard: results[%d] task_id is required", i)
		}
		if !validTrack(result.Track) {
			return fmt.Errorf("validate scorecard: result %q has invalid track %q", result.TaskID, result.Track)
		}
		if !validLanguage(result.Language) {
			return fmt.Errorf("validate scorecard: result %q has invalid language %q", result.TaskID, result.Language)
		}
		if result.InterventionCount < 0 {
			return fmt.Errorf("validate scorecard: result %q has negative intervention_count", result.TaskID)
		}
		if result.InterventionNeeded && result.InterventionClass == "" {
			return fmt.Errorf("validate scorecard: result %q requires intervention_class when intervention_needed is true", result.TaskID)
		}
		if result.RecoverySucceeded && !result.RecoveryAttempted {
			return fmt.Errorf("validate scorecard: result %q cannot have recovery_succeeded without recovery_attempted", result.TaskID)
		}
		if result.DurationSeconds < 0 {
			return fmt.Errorf("validate scorecard: result %q has negative duration_seconds", result.TaskID)
		}
	}
	return nil
}

// Summary aggregates scorecard metrics overall and by track.
func (s *Scorecard) Summary() Summary {
	summary := Summary{
		ByTrack:         make(map[Track]TrackSummary),
		FailureFamilies: make(map[string]int),
	}
	if s == nil {
		return summary
	}

	trackSuccessAndVerified := make(map[Track]int)
	trackInterventionTotals := make(map[Track]int)
	trackSuccessDurationTotals := make(map[Track]float64)
	trackSuccessDurationCounts := make(map[Track]int)
	successAndVerified := 0
	interventionTotal := 0
	successDurationTotal := 0.0
	successDurationCount := 0

	for _, result := range s.Results {
		summary.Total++
		interventionTotal += result.InterventionCount
		trackInterventionTotals[result.Track] += result.InterventionCount
		if result.Success {
			summary.Successes++
			successDurationTotal += result.DurationSeconds
			successDurationCount++
			trackSuccessDurationTotals[result.Track] += result.DurationSeconds
			trackSuccessDurationCounts[result.Track]++
		}
		if result.Success && result.VerificationPassed {
			successAndVerified++
			trackSuccessAndVerified[result.Track]++
		}
		if result.RegressionTriggered {
			summary.RegressionsTriggered++
		}
		if result.InterventionNeeded {
			summary.Interventions++
		}
		if result.VerificationPassed {
			summary.VerificationPasses++
		}
		if result.RecoveryAttempted {
			summary.RecoveryAttempts++
		}
		if result.RecoverySucceeded {
			summary.RecoverySuccesses++
		}
		if result.FailureFamily != "" {
			summary.FailureFamilies[result.FailureFamily]++
		}

		trackSummary := summary.ByTrack[result.Track]
		if trackSummary.FailureFamilies == nil {
			trackSummary.FailureFamilies = make(map[string]int)
		}
		trackSummary.Total++
		if result.Success {
			trackSummary.Successes++
		}
		if result.RegressionTriggered {
			trackSummary.RegressionsTriggered++
		}
		if result.InterventionNeeded {
			trackSummary.Interventions++
		}
		if result.VerificationPassed {
			trackSummary.VerificationPasses++
		}
		if result.RecoveryAttempted {
			trackSummary.RecoveryAttempts++
		}
		if result.RecoverySucceeded {
			trackSummary.RecoverySuccesses++
		}
		if result.FailureFamily != "" {
			trackSummary.FailureFamilies[result.FailureFamily]++
		}
		summary.ByTrack[result.Track] = trackSummary
	}

	if summary.Total > 0 {
		summary.SuccessRate = float64(summary.Successes) / float64(summary.Total)
		summary.SuccessAndVerifiedRate = float64(successAndVerified) / float64(summary.Total)
		if summary.Successes > 0 {
			summary.RegressionRate = float64(summary.RegressionsTriggered) / float64(summary.Successes)
		}
		summary.InterventionRate = float64(summary.Interventions) / float64(summary.Total)
		summary.InterventionMean = float64(interventionTotal) / float64(summary.Total)
		summary.VerificationPassRate = float64(summary.VerificationPasses) / float64(summary.Total)
		if summary.RecoveryAttempts > 0 {
			summary.RecoverySuccessRate = float64(summary.RecoverySuccesses) / float64(summary.RecoveryAttempts)
		}
	}
	if successDurationCount > 0 {
		summary.SuccessDurationMean = successDurationTotal / float64(successDurationCount)
	}
	for track, trackSummary := range summary.ByTrack {
		if trackSummary.Total > 0 {
			trackSummary.SuccessRate = float64(trackSummary.Successes) / float64(trackSummary.Total)
			trackSummary.SuccessAndVerifiedRate = float64(trackSuccessAndVerified[track]) / float64(trackSummary.Total)
			if trackSummary.Successes > 0 {
				trackSummary.RegressionRate = float64(trackSummary.RegressionsTriggered) / float64(trackSummary.Successes)
			}
			trackSummary.InterventionRate = float64(trackSummary.Interventions) / float64(trackSummary.Total)
			trackSummary.InterventionMean = float64(trackInterventionTotals[track]) / float64(trackSummary.Total)
			trackSummary.VerificationPassRate = float64(trackSummary.VerificationPasses) / float64(trackSummary.Total)
			if trackSummary.RecoveryAttempts > 0 {
				trackSummary.RecoverySuccessRate = float64(trackSummary.RecoverySuccesses) / float64(trackSummary.RecoveryAttempts)
			}
		}
		if trackSuccessDurationCounts[track] > 0 {
			trackSummary.SuccessDurationMean = trackSuccessDurationTotals[track] / float64(trackSuccessDurationCounts[track])
		}
		summary.ByTrack[track] = trackSummary
	}

	return summary
}
