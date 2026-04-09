package eval

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

type reportClassSummary struct {
	Total       int
	Successes   int
	SuccessRate float64
}

// WriteMarkdownReport writes a human-readable comparison report.
func WriteMarkdownReport(path string, corpus *Corpus, current, baseline *Scorecard) error {
	report, err := BuildMarkdownReport(corpus, current, baseline)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(report), 0o644)
}

// BuildMarkdownReport renders a markdown benchmark report.
func BuildMarkdownReport(corpus *Corpus, current, baseline *Scorecard) (string, error) {
	if corpus == nil || current == nil || baseline == nil {
		return "", fmt.Errorf("build markdown report: corpus/current/baseline must be non-nil")
	}
	if err := corpus.Validate(); err != nil {
		return "", err
	}
	if err := current.Validate(); err != nil {
		return "", err
	}
	if err := baseline.Validate(); err != nil {
		return "", err
	}
	if err := ValidateScorecardCoverage(corpus, current, current.RepeatedRuns); err != nil {
		return "", fmt.Errorf("build markdown report: current coverage: %w", err)
	}
	if err := ValidateScorecardCoverage(corpus, baseline, baseline.RepeatedRuns); err != nil {
		return "", fmt.Errorf("build markdown report: baseline coverage: %w", err)
	}

	diff, err := Diff(current, baseline)
	if err != nil {
		return "", err
	}
	classSummary := summarizeByRepoClass(corpus, current, baseline)
	interventionCounts := interventionClassCounts(current)

	var b strings.Builder
	fmt.Fprintf(&b, "# Benchmark Cycle Report\n\n")
	fmt.Fprintf(&b, "- Current: %s\n", current.System)
	fmt.Fprintf(&b, "- Baseline: %s\n\n", baseline.System)
	fmt.Fprintf(&b, "## Overall Delta\n\n")
	fmt.Fprintf(&b, "- Success rate delta: %.2f\n", diff.SuccessRateDelta)
	fmt.Fprintf(&b, "- Verification pass delta: %.2f\n", diff.VerificationPassDelta)
	fmt.Fprintf(&b, "- Recovery success delta: %.2f\n\n", diff.RecoverySuccessDelta)

	fmt.Fprintf(&b, "## Track Deltas\n\n")
	for _, track := range []Track{TrackBrownfieldFeature, TrackBugfix, TrackGreenfield} {
		trackDiff := diff.ByTrack[track]
		if trackDiff.Current.Total == 0 && trackDiff.Baseline.Total == 0 {
			continue
		}
		fmt.Fprintf(&b, "- %s: success %.2f, verification %.2f, recovery %.2f\n",
			track, trackDiff.SuccessRateDelta, trackDiff.VerificationPassDelta, trackDiff.RecoverySuccessDelta)
	}

	fmt.Fprintf(&b, "\n## Repo Class Summary\n\n")
	var classes []string
	for class := range classSummary {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		currentClass := classSummary[class]["current"]
		baselineClass := classSummary[class]["baseline"]
		fmt.Fprintf(&b, "- %s: current %.2f (%d/%d), baseline %.2f (%d/%d)\n",
			class,
			currentClass.SuccessRate, currentClass.Successes, currentClass.Total,
			baselineClass.SuccessRate, baselineClass.Successes, baselineClass.Total,
		)
	}

	if len(interventionCounts) > 0 {
		fmt.Fprintf(&b, "\n## Intervention Classes\n\n")
		var keys []string
		for key := range interventionCounts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "- %s: %d\n", key, interventionCounts[key])
		}
	}

	if len(current.Summary().FailureFamilies) > 0 {
		fmt.Fprintf(&b, "\n## Failure Families (Current)\n\n")
		var families []string
		for family := range current.Summary().FailureFamilies {
			families = append(families, family)
		}
		sort.Strings(families)
		for _, family := range families {
			fmt.Fprintf(&b, "- %s: %d\n", family, current.Summary().FailureFamilies[family])
		}
	}

	return b.String(), nil
}

func summarizeByRepoClass(corpus *Corpus, current, baseline *Scorecard) map[string]map[string]reportClassSummary {
	taskClass := make(map[string]string, len(corpus.Tasks))
	for _, task := range corpus.Tasks {
		taskClass[task.ID] = task.RepoClass
	}
	out := make(map[string]map[string]reportClassSummary)
	update := func(label string, result RunResult) {
		class := taskClass[result.TaskID]
		if class == "" {
			class = "unknown"
		}
		if out[class] == nil {
			out[class] = map[string]reportClassSummary{}
		}
		s := out[class][label]
		s.Total++
		if result.Success {
			s.Successes++
		}
		if s.Total > 0 {
			s.SuccessRate = float64(s.Successes) / float64(s.Total)
		}
		out[class][label] = s
	}
	for _, result := range current.Results {
		update("current", result)
	}
	for _, result := range baseline.Results {
		update("baseline", result)
	}
	return out
}

func interventionClassCounts(scorecard *Scorecard) map[string]int {
	counts := make(map[string]int)
	for _, result := range scorecard.Results {
		if result.InterventionClass != "" {
			counts[result.InterventionClass]++
		}
	}
	return counts
}
