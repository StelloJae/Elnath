package eval

import "testing"

func TestEvaluateMonth2Gate(t *testing.T) {
	corpus := &Corpus{
		Version: "v1",
		Tasks: []Task{
			{ID: "A", Title: "a", Track: TrackBrownfieldFeature, Language: LanguageGo, RepoClass: "cli_dev_tool", BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://example.com/a", RepoRef: "deadbeef", AcceptanceCriteria: []string{"ok"}},
			{ID: "B", Title: "b", Track: TrackBugfix, Language: LanguageTypeScript, RepoClass: "service_backend", BenchmarkFamily: "brownfield_holdout", Holdout: true, Prompt: "do", Repo: "https://example.com/b", RepoRef: "feedface", AcceptanceCriteria: []string{"ok"}},
		},
	}
	current := &Scorecard{
		Version: "v1",
		System:  "elnath",
		Results: []RunResult{
			{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true, FailureFamily: "repo_context_miss"},
			{TaskID: "B", Track: TrackBugfix, Language: LanguageTypeScript, Success: false, VerificationPassed: true, FailureFamily: "weak_verification_path"},
		},
	}
	baseline := &Scorecard{
		Version: "v1",
		System:  "baseline",
		Results: []RunResult{
			{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: false},
			{TaskID: "B", Track: TrackBugfix, Language: LanguageTypeScript, Success: false, VerificationPassed: false},
		},
	}

	gate, err := EvaluateMonth2Gate(corpus, current, baseline)
	if err != nil {
		t.Fatalf("EvaluateMonth2Gate: %v", err)
	}
	if !gate.Pass {
		t.Fatalf("expected gate pass, got %+v", gate)
	}
}

func TestEvaluateMonth2GateFailsWithoutHoldout(t *testing.T) {
	corpus := &Corpus{
		Version: "v1",
		Tasks: []Task{
			{ID: "A", Title: "a", Track: TrackBrownfieldFeature, Language: LanguageGo, RepoClass: "cli_dev_tool", BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://example.com/a", RepoRef: "deadbeef", AcceptanceCriteria: []string{"ok"}},
		},
	}
	current := &Scorecard{
		Version: "v1",
		System:  "elnath",
		Results: []RunResult{{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true}},
	}
	baseline := &Scorecard{
		Version: "v1",
		System:  "baseline",
		Results: []RunResult{{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: true}},
	}

	gate, err := EvaluateMonth2Gate(corpus, current, baseline)
	if err != nil {
		t.Fatalf("EvaluateMonth2Gate: %v", err)
	}
	if gate.Pass {
		t.Fatalf("expected gate fail, got %+v", gate)
	}
}

func TestEvaluateMonth2GateFailsWhenBaselineMissesHoldoutCoverage(t *testing.T) {
	corpus := &Corpus{
		Version: "v1",
		Tasks: []Task{
			{ID: "A", Title: "a", Track: TrackBrownfieldFeature, Language: LanguageGo, RepoClass: "cli_dev_tool", BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://example.com/a", RepoRef: "deadbeef", AcceptanceCriteria: []string{"ok"}},
			{ID: "B", Title: "b", Track: TrackBugfix, Language: LanguageTypeScript, RepoClass: "service_backend", BenchmarkFamily: "brownfield_holdout", Holdout: true, Prompt: "do", Repo: "https://example.com/b", RepoRef: "feedface", AcceptanceCriteria: []string{"ok"}},
		},
	}
	current := &Scorecard{
		Version: "v1",
		System:  "elnath",
		Results: []RunResult{
			{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true},
			{TaskID: "B", Track: TrackBugfix, Language: LanguageTypeScript, Success: true, VerificationPassed: true},
		},
	}
	baseline := &Scorecard{
		Version: "v1",
		System:  "baseline",
		Results: []RunResult{
			{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: false},
		},
	}

	gate, err := EvaluateMonth2Gate(corpus, current, baseline)
	if err == nil {
		t.Fatalf("expected coverage validation failure or gate failure, got %+v", gate)
	}
}

func TestEvaluateH1PassRulePassesWithImprovedSummary(t *testing.T) {
	baseline := month3BaselineScorecard().Summary()
	current := month3PassingScorecard("run-1").Summary()

	result := EvaluateH1PassRule(&current, &baseline)
	if !result.Pass {
		t.Fatalf("expected H1 pass, got %+v", result)
	}
	if !result.HardGatePass {
		t.Fatalf("expected hard gate pass, got %+v", result)
	}
	if !result.SoftGatePass || result.SoftGateCount != 3 {
		t.Fatalf("expected all soft gates to pass, got %+v", result)
	}
	for _, key := range []string{"T_brownfield", "T_bugfix", "T_intervent", "T_regression", "T_time"} {
		if !result.ThresholdResults[key].Pass {
			t.Fatalf("expected %s to pass, got %+v", key, result.ThresholdResults[key])
		}
	}
}

func TestEvaluateH1PassRuleFailsWhenSoftGatesMiss(t *testing.T) {
	baseline := month3BaselineScorecard().Summary()
	current := month3SoftFailScorecard("run-1").Summary()

	result := EvaluateH1PassRule(&current, &baseline)
	if result.Pass {
		t.Fatalf("expected H1 fail, got %+v", result)
	}
	if !result.HardGatePass {
		t.Fatalf("expected hard gate pass, got %+v", result)
	}
	if result.SoftGatePass || result.SoftGateCount != 0 {
		t.Fatalf("expected soft gate fail with count 0, got %+v", result)
	}
}

func TestEvaluateH1PassRuleFailsHardGateImmediately(t *testing.T) {
	baseline := month3BaselineScorecard().Summary()
	current := month3HardFailScorecard("run-1").Summary()

	result := EvaluateH1PassRule(&current, &baseline)
	if result.Pass {
		t.Fatalf("expected H1 fail, got %+v", result)
	}
	if result.HardGatePass {
		t.Fatalf("expected hard gate fail, got %+v", result)
	}
	if !result.ThresholdResults["T_bugfix"].Pass {
		t.Fatalf("expected bugfix threshold to pass, got %+v", result.ThresholdResults["T_bugfix"])
	}
	if result.ThresholdResults["T_brownfield"].Pass {
		t.Fatalf("expected brownfield threshold to fail, got %+v", result.ThresholdResults["T_brownfield"])
	}
	if result.SoftGateCount != 3 {
		t.Fatalf("expected soft gate count 3 despite hard gate fail, got %+v", result)
	}
}

func TestEvaluateH1PassRuleFailsWithoutRequiredTracks(t *testing.T) {
	baseline := (&Scorecard{Version: "v1", System: "baseline", Results: []RunResult{
		{TaskID: "BUG-1", Track: TrackBugfix, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 2, DurationSeconds: 10},
		{TaskID: "BUG-2", Track: TrackBugfix, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 2, DurationSeconds: 20},
	}}).Summary()
	current := (&Scorecard{Version: "v1", System: "run-1", Results: []RunResult{
		{TaskID: "BUG-1", Track: TrackBugfix, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 1, DurationSeconds: 8},
		{TaskID: "BUG-2", Track: TrackBugfix, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 1, DurationSeconds: 8},
	}}).Summary()

	result := EvaluateH1PassRule(&current, &baseline)
	if result.Pass {
		t.Fatalf("expected H1 fail, got %+v", result)
	}
	if result.HardGatePass {
		t.Fatalf("expected hard gate fail when brownfield track is missing, got %+v", result)
	}
	if result.ThresholdResults["T_brownfield"].Pass {
		t.Fatalf("expected brownfield threshold to fail when track is missing, got %+v", result.ThresholdResults["T_brownfield"])
	}
}

func TestEvaluateMonth3GatePassesWithStableRuns(t *testing.T) {
	baseline := month3BaselineScorecard()
	result, err := EvaluateMonth3Gate([]*Scorecard{
		month3PassingScorecard("run-1"),
		month3PassingScorecard("run-2"),
		month3PassingScorecard("run-3"),
	}, baseline)
	if err != nil {
		t.Fatalf("EvaluateMonth3Gate: %v", err)
	}
	if !result.Pass || !result.AverageH1Pass || !result.StabilityPass {
		t.Fatalf("expected month 3 gate pass, got %+v", result)
	}
	if len(result.H1Results) != 3 {
		t.Fatalf("H1Results len = %d, want 3", len(result.H1Results))
	}
	for i, h1 := range result.H1Results {
		if !h1.Pass {
			t.Fatalf("run %d H1 pass = false, want true", i+1)
		}
	}
}

func TestEvaluateMonth3GateFailsWhenRunsAreUnstable(t *testing.T) {
	baseline := month3BaselineScorecard()
	result, err := EvaluateMonth3Gate([]*Scorecard{
		month3PassingScorecard("run-1"),
		month3SoftFailScorecard("run-2"),
		month3SoftFailScorecard("run-3"),
	}, baseline)
	if err != nil {
		t.Fatalf("EvaluateMonth3Gate: %v", err)
	}
	if result.Pass {
		t.Fatalf("expected month 3 gate fail, got %+v", result)
	}
	if result.StabilityPass {
		t.Fatalf("expected stability fail, got %+v", result)
	}
	if len(result.H1Results) != 3 {
		t.Fatalf("H1Results len = %d, want 3", len(result.H1Results))
	}
	if !result.H1Results[0].Pass || result.H1Results[1].Pass || result.H1Results[2].Pass {
		t.Fatalf("expected pass/fail/fail sequence, got %+v", result.H1Results)
	}
}

func month3BaselineScorecard() *Scorecard {
	return &Scorecard{Version: "v1", System: "baseline", Results: []RunResult{
		{TaskID: "BF-1", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 2, DurationSeconds: 10, RegressionTriggered: true},
		{TaskID: "BF-2", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 2, DurationSeconds: 20},
		{TaskID: "BUG-1", Track: TrackBugfix, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 2, DurationSeconds: 10},
		{TaskID: "BUG-2", Track: TrackBugfix, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 2, DurationSeconds: 20},
	}}
}

func month3PassingScorecard(system string) *Scorecard {
	return &Scorecard{Version: "v1", System: system, Results: []RunResult{
		{TaskID: "BF-1", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 1, DurationSeconds: 8},
		{TaskID: "BF-2", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 1, DurationSeconds: 8},
		{TaskID: "BUG-1", Track: TrackBugfix, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 1, DurationSeconds: 10},
		{TaskID: "BUG-2", Track: TrackBugfix, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 1, DurationSeconds: 12},
	}}
}

func month3SoftFailScorecard(system string) *Scorecard {
	return &Scorecard{Version: "v1", System: system, Results: []RunResult{
		{TaskID: "BF-1", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 1, DurationSeconds: 15, RegressionTriggered: true},
		{TaskID: "BF-2", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 1, DurationSeconds: 15},
		{TaskID: "BUG-1", Track: TrackBugfix, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 2, DurationSeconds: 15, RegressionTriggered: true},
		{TaskID: "BUG-2", Track: TrackBugfix, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 4, DurationSeconds: 15},
	}}
}

func month3HardFailScorecard(system string) *Scorecard {
	return &Scorecard{Version: "v1", System: system, Results: []RunResult{
		{TaskID: "BF-1", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 0, DurationSeconds: 8},
		{TaskID: "BF-2", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: false, InterventionCount: 0, DurationSeconds: 8},
		{TaskID: "BUG-1", Track: TrackBugfix, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 0, DurationSeconds: 8},
		{TaskID: "BUG-2", Track: TrackBugfix, Language: LanguageGo, Success: true, VerificationPassed: true, InterventionCount: 0, DurationSeconds: 8},
	}}
}
