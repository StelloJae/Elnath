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
