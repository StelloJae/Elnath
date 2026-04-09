package eval

import "testing"

func TestCheckAntiVanityRules(t *testing.T) {
	corpus := &Corpus{
		Version: "v1",
		Tasks: []Task{
			{
				ID:                 "A",
				Title:              "Bugfix task",
				Track:              TrackBugfix,
				Language:           LanguageGo,
				RepoClass:          "cli_dev_tool",
				BenchmarkFamily:    "brownfield_primary",
				Prompt:             "Fix the regression",
				Repo:               "https://github.com/example/repo",
				AcceptanceCriteria: []string{"tests pass"},
			},
		},
	}
	scorecard := &Scorecard{
		Version:           "v1",
		System:            "elnath",
		Context:           "launch",
		RepeatedRuns:      0,
		InterventionNotes: false,
		Results: []RunResult{
			{TaskID: "A", Track: TrackBugfix, Language: LanguageGo, Success: true, InterventionNeeded: true, InterventionClass: "avoidable"},
		},
	}

	violations := CheckAntiVanityRules(corpus, scorecard)
	if len(violations) < 2 {
		t.Fatalf("expected multiple violations, got %+v", violations)
	}
}
