package eval

import "testing"

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
