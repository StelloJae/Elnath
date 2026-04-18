package eval

import (
	"os"
	"path/filepath"
	"strings"
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

// validV2Corpus returns a minimal valid v2 corpus (12 training + 5 held-out
// = 17 tasks, 3 intents summing to 1.0). Tests derive failure-mode
// variations by mutating the returned corpus.
func validV2Corpus() *Corpus {
	intents := []string{"question", "complex_task", "bugfix"}
	workflows := []string{"single", "team", "ralph"}
	tasks := make([]Task, 0, 17)
	training := make([]string, 0, 12)
	heldOut := make([]string, 0, 5)
	for i := 0; i < 17; i++ {
		id := "V2-" + string(rune('A'+i))
		intent := intents[i%len(intents)]
		workflow := workflows[i%len(workflows)]
		tasks = append(tasks, Task{
			ID:               id,
			Title:            "v2 task " + id,
			Track:            TrackBugfix,
			Language:         LanguageGo,
			Prompt:           "stub prompt",
			Intent:           intent,
			ExpectedWorkflow: workflow,
		})
		if i < 12 {
			training = append(training, id)
		} else {
			heldOut = append(heldOut, id)
		}
	}
	return &Corpus{
		Version: "v2",
		Tasks:   tasks,
		IntentDistribution: map[string]float64{
			"question":     0.45,
			"complex_task": 0.35,
			"bugfix":       0.20,
		},
		TrainingSet: training,
		HeldOutSet:  heldOut,
	}
}

func TestCorpusValidateV2Valid(t *testing.T) {
	if err := validV2Corpus().Validate(); err != nil {
		t.Fatalf("valid v2 corpus rejected: %v", err)
	}
}

func TestCorpusValidateV2ErrorCases(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Corpus)
		wantSub string
	}{
		{
			name:    "missing intent",
			mutate:  func(c *Corpus) { c.Tasks[0].Intent = "" },
			wantSub: "intent is required",
		},
		{
			name: "overlapping sets",
			mutate: func(c *Corpus) {
				c.HeldOutSet = append(c.HeldOutSet, c.TrainingSet[0])
			},
			wantSub: "both training_set and held_out_set",
		},
		{
			name: "training set too small",
			mutate: func(c *Corpus) {
				c.TrainingSet = c.TrainingSet[:5]
			},
			wantSub: "training_set size",
		},
		{
			name: "held_out set too small",
			mutate: func(c *Corpus) {
				c.HeldOutSet = c.HeldOutSet[:2]
			},
			wantSub: "held_out_set size",
		},
		{
			name:    "distribution empty",
			mutate:  func(c *Corpus) { c.IntentDistribution = nil },
			wantSub: "intent_distribution is required",
		},
		{
			name:    "distribution sum mismatch",
			mutate:  func(c *Corpus) { c.IntentDistribution["question"] = 0.90 },
			wantSub: "intent_distribution sums to",
		},
		{
			name: "intent not in distribution",
			mutate: func(c *Corpus) {
				c.Tasks[0].Intent = "wiki_query"
			},
			wantSub: "not declared in intent_distribution",
		},
		{
			name: "training references unknown task",
			mutate: func(c *Corpus) {
				c.TrainingSet[0] = "NONEXISTENT"
			},
			wantSub: "training_set references unknown",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			corpus := validV2Corpus()
			tc.mutate(corpus)
			err := corpus.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestCorpusValidateV2RelaxesV1Fields confirms the plan's relaxation: v2
// tasks do NOT need Repo, SourceURL, RepoRef, or AcceptanceCriteria.
// RepoClass and BenchmarkFamily are also optional for v2.
func TestCorpusValidateV2RelaxesV1Fields(t *testing.T) {
	corpus := validV2Corpus()
	for i := range corpus.Tasks {
		corpus.Tasks[i].Repo = ""
		corpus.Tasks[i].SourceURL = ""
		corpus.Tasks[i].RepoRef = ""
		corpus.Tasks[i].AcceptanceCriteria = nil
		corpus.Tasks[i].RepoClass = ""
		corpus.Tasks[i].BenchmarkFamily = ""
	}
	if err := corpus.Validate(); err != nil {
		t.Fatalf("v2 corpus without v1 mandatory fields rejected: %v", err)
	}
}

// TestCorpusValidateV1RegressionPublicCorpus guards the brownfield
// constraint: real v1 corpus files must continue to pass Validate() without
// modification after v2 version dispatch is introduced.
func TestCorpusValidateV1RegressionPublicCorpus(t *testing.T) {
	corpus, err := LoadCorpus("../../benchmarks/public-corpus.v1.json")
	if err != nil {
		t.Fatalf("LoadCorpus(public-corpus.v1.json) = %v; v1 path must remain unchanged", err)
	}
	if corpus.Version != "v1" {
		t.Fatalf("public corpus version = %q, want v1", corpus.Version)
	}
	if len(corpus.Tasks) == 0 {
		t.Fatal("public corpus has zero tasks; test setup broken")
	}
}
