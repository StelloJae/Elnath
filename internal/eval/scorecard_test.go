package eval

import (
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
