package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCorpusFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	return path
}

func TestLoadCorpus(t *testing.T) {
	path := writeCorpusFile(t, `{
  "version":"v1",
  "tasks":[
    {"id":"BF-001","title":"Fix auth bug","track":"bugfix","language":"go","repo_class":"cli_dev_tool","benchmark_family":"brownfield_primary","prompt":"Fix the bug","repo":"https://github.com/example/go-repo","repo_ref":"deadbeef","acceptance_criteria":["tests pass"]},
    {"id":"BR-001","title":"Add endpoint","track":"brownfield_feature","language":"typescript","repo_class":"service_backend","benchmark_family":"brownfield_secondary","prompt":"Add the feature","source_url":"https://github.com/example/ts-repo/issues/1","acceptance_criteria":["feature works"]}
  ]
}`)

	corpus, err := LoadCorpus(path)
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if corpus.Version != "v1" || len(corpus.Tasks) != 2 {
		t.Fatalf("unexpected corpus: %+v", corpus)
	}
}

func TestCorpusValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		corpus Corpus
	}{
		{
			name:   "missing version",
			corpus: Corpus{Tasks: []Task{{ID: "A", Title: "task", Track: TrackBugfix, Language: LanguageGo, Prompt: "do"}}},
		},
		{
			name:   "missing task id",
			corpus: Corpus{Version: "v1", Tasks: []Task{{Title: "task", Track: TrackBugfix, Language: LanguageGo, Prompt: "do"}}},
		},
		{
			name:   "duplicate task id",
			corpus: Corpus{Version: "v1", Tasks: []Task{{ID: "A", Title: "t1", Track: TrackBugfix, Language: LanguageGo, Prompt: "do"}, {ID: "A", Title: "t2", Track: TrackBugfix, Language: LanguageGo, Prompt: "do"}}},
		},
		{
			name:   "invalid track",
			corpus: Corpus{Version: "v1", Tasks: []Task{{ID: "A", Title: "task", Track: "oops", Language: LanguageGo, RepoClass: "cli_dev_tool", BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://github.com/example/repo", RepoRef: "deadbeef", AcceptanceCriteria: []string{"tests pass"}}}},
		},
		{
			name:   "invalid language",
			corpus: Corpus{Version: "v1", Tasks: []Task{{ID: "A", Title: "task", Track: TrackBugfix, Language: "python", RepoClass: "cli_dev_tool", BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://github.com/example/repo", RepoRef: "deadbeef", AcceptanceCriteria: []string{"tests pass"}}}},
		},
		{
			name:   "missing repo class",
			corpus: Corpus{Version: "v1", Tasks: []Task{{ID: "A", Title: "task", Track: TrackBugfix, Language: LanguageGo, BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://github.com/example/repo", RepoRef: "deadbeef", AcceptanceCriteria: []string{"tests pass"}}}},
		},
		{
			name:   "missing benchmark family",
			corpus: Corpus{Version: "v1", Tasks: []Task{{ID: "A", Title: "task", Track: TrackBugfix, Language: LanguageGo, RepoClass: "cli_dev_tool", Prompt: "do", Repo: "https://github.com/example/repo", RepoRef: "deadbeef", AcceptanceCriteria: []string{"tests pass"}}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.corpus.Validate(); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestCorpusValidateAllowsResearchTrack(t *testing.T) {
	corpus := Corpus{
		Version: "v1",
		Tasks: []Task{{
			ID:                 "R-1",
			Title:              "Investigate flaky benchmark",
			Track:              TrackResearch,
			Language:           LanguageGo,
			RepoClass:          "cli_dev_tool",
			BenchmarkFamily:    "research_primary",
			Prompt:             "Investigate and summarize the issue",
			Repo:               "https://github.com/example/repo",
			RepoRef:            "deadbeef",
			AcceptanceCriteria: []string{"summary is written"},
		}},
	}

	if err := corpus.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
