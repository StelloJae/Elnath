package eval

import (
	"math"
	"testing"
)

func TestDiff(t *testing.T) {
	current := &Scorecard{
		Version: "v1",
		System:  "elnath",
		Results: []RunResult{
			{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true, RecoveryAttempted: true, RecoverySucceeded: true},
			{TaskID: "B", Track: TrackBugfix, Language: LanguageTypeScript, Success: true, VerificationPassed: true, RecoveryAttempted: true, RecoverySucceeded: true},
		},
	}
	baseline := &Scorecard{
		Version: "v1",
		System:  "baseline",
		Results: []RunResult{
			{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: false, RecoveryAttempted: true, RecoverySucceeded: false},
			{TaskID: "B", Track: TrackBugfix, Language: LanguageTypeScript, Success: true, VerificationPassed: true, RecoveryAttempted: true, RecoverySucceeded: false},
		},
	}

	diff, err := Diff(current, baseline)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff.SuccessRateDelta <= 0 {
		t.Fatalf("expected positive delta, got %+v", diff)
	}
	if diff.ByTrack[TrackBrownfieldFeature].SuccessRateDelta <= 0 {
		t.Fatalf("expected positive brownfield delta, got %+v", diff.ByTrack[TrackBrownfieldFeature])
	}
	if diff.VerificationPassDelta <= 0 || diff.RecoverySuccessDelta <= 0 {
		t.Fatalf("expected positive verification/recovery deltas, got %+v", diff)
	}
}

func TestDiffRegressionRateDelta(t *testing.T) {
	currentResults := make([]RunResult, 0, 10)
	baselineResults := make([]RunResult, 0, 10)
	for i := 0; i < 10; i++ {
		taskID := string(rune('A' + i))
		currentResults = append(currentResults, RunResult{TaskID: taskID, Track: TrackBugfix, Language: LanguageGo, Success: true, RegressionTriggered: i < 3})
		baselineResults = append(baselineResults, RunResult{TaskID: taskID, Track: TrackBugfix, Language: LanguageGo, Success: true, RegressionTriggered: i < 1})
	}

	diff, err := Diff(
		&Scorecard{Version: "v1", System: "elnath", Results: currentResults},
		&Scorecard{Version: "v1", System: "baseline", Results: baselineResults},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if math.Abs(diff.RegressionRateDelta-0.2) > 1e-9 {
		t.Fatalf("RegressionRateDelta = %v, want 0.2", diff.RegressionRateDelta)
	}
}

func TestTrackDeltaRegressionRate(t *testing.T) {
	current := &Scorecard{Version: "v1", System: "elnath", Results: []RunResult{
		{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, RegressionTriggered: true},
		{TaskID: "B", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
		{TaskID: "C", Track: TrackBugfix, Language: LanguageGo, Success: true},
		{TaskID: "D", Track: TrackBugfix, Language: LanguageGo, Success: true},
	}}
	baseline := &Scorecard{Version: "v1", System: "baseline", Results: []RunResult{
		{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
		{TaskID: "B", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
		{TaskID: "C", Track: TrackBugfix, Language: LanguageGo, Success: true},
		{TaskID: "D", Track: TrackBugfix, Language: LanguageGo, Success: true},
	}}

	diff, err := Diff(current, baseline)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if got := diff.ByTrack[TrackBrownfieldFeature].RegressionRateDelta; got != 0.5 {
		t.Fatalf("brownfield RegressionRateDelta = %v, want 0.5", got)
	}
	if got := diff.ByTrack[TrackBugfix].RegressionRateDelta; got != 0 {
		t.Fatalf("bugfix RegressionRateDelta = %v, want 0", got)
	}
}

func TestDiffIncludesMonth3Deltas(t *testing.T) {
	current := &Scorecard{Version: "v1", System: "elnath", Results: []RunResult{
		{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 1, DurationSeconds: 10},
		{TaskID: "B", Track: TrackBugfix, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 3, DurationSeconds: 50},
	}}
	baseline := &Scorecard{Version: "v1", System: "baseline", Results: []RunResult{
		{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: false, InterventionCount: 2, DurationSeconds: 30},
		{TaskID: "B", Track: TrackBugfix, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 1, DurationSeconds: 60},
	}}

	diff, err := Diff(current, baseline)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if math.Abs(diff.SuccessAndVerifiedRateDelta-0.5) > 1e-9 {
		t.Fatalf("SuccessAndVerifiedRateDelta = %v, want 0.5", diff.SuccessAndVerifiedRateDelta)
	}
	if math.Abs(diff.InterventionMeanDelta-0.5) > 1e-9 {
		t.Fatalf("InterventionMeanDelta = %v, want 0.5", diff.InterventionMeanDelta)
	}
	if math.Abs(diff.SuccessDurationMeanDelta+20) > 1e-9 {
		t.Fatalf("SuccessDurationMeanDelta = %v, want -20", diff.SuccessDurationMeanDelta)
	}
	if math.Abs(diff.ByTrack[TrackBrownfieldFeature].SuccessAndVerifiedRateDelta-1) > 1e-9 {
		t.Fatalf("brownfield SuccessAndVerifiedRateDelta = %v, want 1", diff.ByTrack[TrackBrownfieldFeature].SuccessAndVerifiedRateDelta)
	}
}
