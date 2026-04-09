package eval

import "testing"

func TestValidateScorecardCoverage(t *testing.T) {
	corpus := &Corpus{
		Version: "v1",
		Tasks: []Task{
			{ID: "A", Title: "a", Track: TrackBrownfieldFeature, Language: LanguageGo, RepoClass: "cli_dev_tool", BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://example.com/a", RepoRef: "deadbeef", AcceptanceCriteria: []string{"ok"}},
			{ID: "B", Title: "b", Track: TrackBugfix, Language: LanguageTypeScript, RepoClass: "service_backend", BenchmarkFamily: "brownfield_holdout", Holdout: true, Prompt: "do", Repo: "https://example.com/b", RepoRef: "feedface", AcceptanceCriteria: []string{"ok"}},
		},
	}
	scorecard := &Scorecard{
		Version:      "v1",
		System:       "elnath",
		RepeatedRuns: 2,
		Results: []RunResult{
			{TaskID: "A", Run: 1, Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
			{TaskID: "B", Run: 1, Track: TrackBugfix, Language: LanguageTypeScript, Success: true},
			{TaskID: "A", Run: 2, Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
			{TaskID: "B", Run: 2, Track: TrackBugfix, Language: LanguageTypeScript, Success: false, FailureFamily: "verification_failed"},
		},
	}

	if err := ValidateScorecardCoverage(corpus, scorecard, 2); err != nil {
		t.Fatalf("ValidateScorecardCoverage: %v", err)
	}
}

func TestValidateScorecardCoverageFailsOnMissingTaskRun(t *testing.T) {
	corpus := &Corpus{
		Version: "v1",
		Tasks: []Task{
			{ID: "A", Title: "a", Track: TrackBrownfieldFeature, Language: LanguageGo, RepoClass: "cli_dev_tool", BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://example.com/a", RepoRef: "deadbeef", AcceptanceCriteria: []string{"ok"}},
			{ID: "B", Title: "b", Track: TrackBugfix, Language: LanguageTypeScript, RepoClass: "service_backend", BenchmarkFamily: "brownfield_holdout", Holdout: true, Prompt: "do", Repo: "https://example.com/b", RepoRef: "feedface", AcceptanceCriteria: []string{"ok"}},
		},
	}
	scorecard := &Scorecard{
		Version:      "v1",
		System:       "elnath",
		RepeatedRuns: 2,
		Results: []RunResult{
			{TaskID: "A", Run: 1, Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
			{TaskID: "B", Run: 1, Track: TrackBugfix, Language: LanguageTypeScript, Success: true},
			{TaskID: "A", Run: 2, Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
		},
	}

	if err := ValidateScorecardCoverage(corpus, scorecard, 2); err == nil {
		t.Fatal("expected coverage validation failure")
	}
}

func TestValidateComparableTaskRunsFailsOnMismatch(t *testing.T) {
	current := &Scorecard{
		Version:      "v1",
		System:       "elnath",
		RepeatedRuns: 1,
		Results: []RunResult{
			{TaskID: "A", Run: 1, Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true},
			{TaskID: "B", Run: 1, Track: TrackBugfix, Language: LanguageTypeScript, Success: true},
		},
	}
	baseline := &Scorecard{
		Version:      "v1",
		System:       "baseline",
		RepeatedRuns: 1,
		Results: []RunResult{
			{TaskID: "A", Run: 1, Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false},
		},
	}

	if err := ValidateComparableTaskRuns(current, baseline); err == nil {
		t.Fatal("expected comparable-task-run validation failure")
	}
}
