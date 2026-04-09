package eval

import (
	"strings"
	"testing"
)

func TestBuildMarkdownReport(t *testing.T) {
	corpus := &Corpus{
		Version: "v1",
		Tasks: []Task{
			{ID: "A", Title: "task A", Track: TrackBrownfieldFeature, Language: LanguageGo, RepoClass: "cli_dev_tool", BenchmarkFamily: "brownfield_primary", Prompt: "do", Repo: "https://example.com/a", RepoRef: "deadbeef", AcceptanceCriteria: []string{"ok"}},
			{ID: "B", Title: "task B", Track: TrackBugfix, Language: LanguageTypeScript, RepoClass: "service_backend", BenchmarkFamily: "brownfield_holdout", Prompt: "do", Repo: "https://example.com/b", RepoRef: "feedface", AcceptanceCriteria: []string{"ok"}},
		},
	}
	current := &Scorecard{
		Version:       "v1",
		System:        "elnath",
		Context:       "benchmark",
		RuntimePolicy: "sandbox=workspace-write, approvals=never",
		Results: []RunResult{
			{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: true, InterventionClass: "necessary", InterventionNeeded: true, VerificationPassed: true, FailureFamily: "repo_context_miss", RecoveryAttempted: true, RecoverySucceeded: true},
			{TaskID: "B", Track: TrackBugfix, Language: LanguageTypeScript, Success: false, VerificationPassed: false, FailureFamily: "weak_verification_path", RecoveryAttempted: true, RecoverySucceeded: false},
		},
	}
	baseline := &Scorecard{
		Version:       "v1",
		System:        "baseline",
		Context:       "benchmark",
		RuntimePolicy: "sandbox=workspace-write, approvals=on-request",
		Results: []RunResult{
			{TaskID: "A", Track: TrackBrownfieldFeature, Language: LanguageGo, Success: false, VerificationPassed: false, RecoveryAttempted: true, RecoverySucceeded: false},
			{TaskID: "B", Track: TrackBugfix, Language: LanguageTypeScript, Success: false, VerificationPassed: false, RecoveryAttempted: true, RecoverySucceeded: false},
		},
	}

	report, err := BuildMarkdownReport(corpus, current, baseline)
	if err != nil {
		t.Fatalf("BuildMarkdownReport: %v", err)
	}
	for _, needle := range []string{
		"# Benchmark Cycle Report",
		"Runtime Policy",
		"sandbox=workspace-write, approvals=never",
		"Success rate delta",
		"Repo Class Summary",
		"cli_dev_tool",
		"Intervention Classes",
		"Failure Families",
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("report missing %q:\n%s", needle, report)
		}
	}
}
