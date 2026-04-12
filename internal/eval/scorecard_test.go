package eval

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func writeScorecardFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scorecard.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write scorecard: %v", err)
	}
	return path
}

func TestLoadScorecardAndSummary(t *testing.T) {
	path := writeScorecardFile(t, `{
  "version":"v1",
  "system":"elnath",
  "baseline":"claude+omx",
  "results":[
    {"task_id":"BF-001","track":"brownfield_feature","language":"go","success":true,"intervention_count":1,"intervention_needed":true,"intervention_class":"necessary","verification_passed":true,"failure_family":"repo_context_miss","recovery_attempted":true,"recovery_succeeded":true,"duration_seconds":10},
    {"task_id":"BF-002","track":"brownfield_feature","language":"go","success":false,"intervention_count":0,"intervention_needed":false,"verification_passed":false,"failure_family":"weak_verification_path","recovery_attempted":true,"recovery_succeeded":false,"duration_seconds":12},
    {"task_id":"BUG-001","track":"bugfix","language":"typescript","success":true,"intervention_count":2,"intervention_needed":true,"intervention_class":"late","verification_passed":true,"failure_family":"bad_interruption_timing","recovery_attempted":true,"recovery_succeeded":true,"duration_seconds":8}
  ]
}`)

	scorecard, err := LoadScorecard(path)
	if err != nil {
		t.Fatalf("LoadScorecard: %v", err)
	}
	summary := scorecard.Summary()
	if summary.Total != 3 || summary.Successes != 2 {
		t.Fatalf("unexpected summary totals: %+v", summary)
	}
	if summary.ByTrack[TrackBrownfieldFeature].Total != 2 {
		t.Fatalf("unexpected brownfield summary: %+v", summary.ByTrack[TrackBrownfieldFeature])
	}
	if summary.ByTrack[TrackBugfix].SuccessRate != 1.0 {
		t.Fatalf("unexpected bugfix success rate: %+v", summary.ByTrack[TrackBugfix])
	}
	if summary.VerificationPassRate == 0 || summary.RecoverySuccessRate == 0 {
		t.Fatalf("expected verification/recovery rates, got %+v", summary)
	}
	if summary.FailureFamilies["repo_context_miss"] != 1 {
		t.Fatalf("expected failure family counts, got %+v", summary.FailureFamilies)
	}
}

func TestScorecardValidateErrors(t *testing.T) {
	cases := []struct {
		name      string
		scorecard Scorecard
	}{
		{name: "missing version", scorecard: Scorecard{System: "elnath", Results: []RunResult{{TaskID: "A", Track: TrackBugfix, Language: LanguageGo}}}},
		{name: "missing system", scorecard: Scorecard{Version: "v1", Results: []RunResult{{TaskID: "A", Track: TrackBugfix, Language: LanguageGo}}}},
		{name: "missing task id", scorecard: Scorecard{Version: "v1", System: "elnath", Results: []RunResult{{Track: TrackBugfix, Language: LanguageGo}}}},
		{name: "invalid track", scorecard: Scorecard{Version: "v1", System: "elnath", Results: []RunResult{{TaskID: "A", Track: "oops", Language: LanguageGo}}}},
		{name: "invalid language", scorecard: Scorecard{Version: "v1", System: "elnath", Results: []RunResult{{TaskID: "A", Track: TrackBugfix, Language: "python"}}}},
		{name: "negative intervention count", scorecard: Scorecard{Version: "v1", System: "elnath", Results: []RunResult{{TaskID: "A", Track: TrackBugfix, Language: LanguageGo, InterventionCount: -1}}}},
		{name: "missing intervention class", scorecard: Scorecard{Version: "v1", System: "elnath", Results: []RunResult{{TaskID: "A", Track: TrackBugfix, Language: LanguageGo, InterventionNeeded: true}}}},
		{name: "recovery succeeded without attempt", scorecard: Scorecard{Version: "v1", System: "elnath", Results: []RunResult{{TaskID: "A", Track: TrackBugfix, Language: LanguageGo, RecoverySucceeded: true}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.scorecard.Validate(); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestSummaryComputesRegressionRate(t *testing.T) {
	results := make([]RunResult, 0, 10)
	for i := 0; i < 2; i++ {
		results = append(results, RunResult{TaskID: string(rune('A' + i)), Track: TrackBugfix, Language: LanguageGo, Success: true, RegressionTriggered: true})
	}
	for i := 2; i < 10; i++ {
		results = append(results, RunResult{TaskID: string(rune('A' + i)), Track: TrackBugfix, Language: LanguageGo, Success: true})
	}

	summary := (&Scorecard{Version: "v1", System: "elnath", Results: results}).Summary()
	if summary.RegressionsTriggered != 2 {
		t.Fatalf("RegressionsTriggered = %d, want 2", summary.RegressionsTriggered)
	}
	if summary.RegressionRate != 0.2 {
		t.Fatalf("RegressionRate = %v, want 0.2", summary.RegressionRate)
	}
}

func TestSummaryRegressionRateZeroSuccesses(t *testing.T) {
	summary := (&Scorecard{Version: "v1", System: "elnath", Results: []RunResult{
		{TaskID: "A", Track: TrackBugfix, Language: LanguageGo, Success: false, RegressionTriggered: true},
		{TaskID: "B", Track: TrackBugfix, Language: LanguageGo, Success: false},
	}}).Summary()
	if summary.RegressionRate != 0 {
		t.Fatalf("RegressionRate = %v, want 0", summary.RegressionRate)
	}
	if math.IsNaN(summary.RegressionRate) {
		t.Fatal("RegressionRate must not be NaN")
	}
}

func TestSummaryRegressionRateExcludesFailedTasks(t *testing.T) {
	summary := (&Scorecard{Version: "v1", System: "elnath", Results: []RunResult{
		{TaskID: "A", Track: TrackBugfix, Language: LanguageGo, Success: true, RegressionTriggered: true},
		{TaskID: "B", Track: TrackBugfix, Language: LanguageGo, Success: false, RegressionTriggered: true},
		{TaskID: "C", Track: TrackBugfix, Language: LanguageGo, Success: true},
		{TaskID: "D", Track: TrackBugfix, Language: LanguageGo, Success: true},
		{TaskID: "E", Track: TrackBugfix, Language: LanguageGo, Success: true},
	}}).Summary()
	if summary.RegressionsTriggered != 2 {
		t.Fatalf("RegressionsTriggered = %d, want 2", summary.RegressionsTriggered)
	}
	if summary.Successes != 4 {
		t.Fatalf("Successes = %d, want 4", summary.Successes)
	}
	if summary.RegressionRate != 0.5 {
		t.Fatalf("RegressionRate = %v, want 0.5", summary.RegressionRate)
	}
}

func TestTrackSummaryRegressionRate(t *testing.T) {
	summary := (&Scorecard{Version: "v1", System: "elnath", Results: []RunResult{
		{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, RegressionTriggered: true},
		{TaskID: "B", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
		{TaskID: "C", Track: TrackBugfix, Language: LanguageGo, Success: true, RegressionTriggered: true},
		{TaskID: "D", Track: TrackBugfix, Language: LanguageGo, Success: true},
		{TaskID: "E", Track: TrackBugfix, Language: LanguageGo, Success: false, RegressionTriggered: true},
	}}).Summary()
	if got := summary.ByTrack[TrackBrownfieldFeature].RegressionRate; got != 0.5 {
		t.Fatalf("brownfield RegressionRate = %v, want 0.5", got)
	}
	if got := summary.ByTrack[TrackBugfix].RegressionRate; got != 1 {
		t.Fatalf("bugfix RegressionRate = %v, want 1", got)
	}
}

func TestValidateBackwardCompatRegressionTriggeredAbsent(t *testing.T) {
	path := writeScorecardFile(t, `{
  "version":"v1",
  "system":"elnath",
  "results":[
    {"task_id":"BUG-001","track":"bugfix","language":"go","success":true,"intervention_count":0,"intervention_needed":false,"duration_seconds":1}
  ]
}`)

	scorecard, err := LoadScorecard(path)
	if err != nil {
		t.Fatalf("LoadScorecard: %v", err)
	}
	if err := scorecard.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if scorecard.Results[0].RegressionTriggered {
		t.Fatal("RegressionTriggered = true, want default false")
	}
}
