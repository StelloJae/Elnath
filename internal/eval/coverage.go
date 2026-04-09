package eval

import (
	"fmt"
)

func normalizedRepeats(n int) int {
	if n <= 0 {
		return 1
	}
	return n
}

func taskRunKey(taskID string, run int) string {
	return fmt.Sprintf("%s#%d", taskID, normalizedRepeats(run))
}

func expectedTaskRunSet(corpus *Corpus, repeats int) map[string]struct{} {
	set := make(map[string]struct{}, len(corpus.Tasks)*normalizedRepeats(repeats))
	for run := 1; run <= normalizedRepeats(repeats); run++ {
		for _, task := range corpus.Tasks {
			set[taskRunKey(task.ID, run)] = struct{}{}
		}
	}
	return set
}

func observedTaskRunCounts(scorecard *Scorecard) map[string]int {
	counts := make(map[string]int, len(scorecard.Results))
	for _, result := range scorecard.Results {
		counts[taskRunKey(result.TaskID, result.Run)]++
	}
	return counts
}

// ValidateScorecardCoverage ensures the scorecard exactly covers the expected corpus task/run set.
func ValidateScorecardCoverage(corpus *Corpus, scorecard *Scorecard, repeats int) error {
	if corpus == nil || scorecard == nil {
		return fmt.Errorf("validate scorecard coverage: corpus/scorecard must be non-nil")
	}
	if err := corpus.Validate(); err != nil {
		return err
	}
	if err := scorecard.Validate(); err != nil {
		return err
	}

	expected := expectedTaskRunSet(corpus, repeats)
	counts := observedTaskRunCounts(scorecard)
	if len(counts) != len(expected) {
		return fmt.Errorf("validate scorecard coverage: expected %d task/run results, got %d", len(expected), len(counts))
	}
	for key, count := range counts {
		if count != 1 {
			return fmt.Errorf("validate scorecard coverage: task/run %s appears %d times", key, count)
		}
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("validate scorecard coverage: unexpected task/run %s", key)
		}
	}
	for key := range expected {
		if _, ok := counts[key]; !ok {
			return fmt.Errorf("validate scorecard coverage: missing task/run %s", key)
		}
	}
	return nil
}

// ValidateComparableTaskRuns ensures two scorecards share the exact same task/run keys.
func ValidateComparableTaskRuns(current, baseline *Scorecard) error {
	if current == nil || baseline == nil {
		return fmt.Errorf("validate comparable task runs: scorecards must be non-nil")
	}
	if err := current.Validate(); err != nil {
		return err
	}
	if err := baseline.Validate(); err != nil {
		return err
	}
	left := observedTaskRunCounts(current)
	right := observedTaskRunCounts(baseline)
	if len(left) != len(right) {
		return fmt.Errorf("validate comparable task runs: current has %d task/runs, baseline has %d", len(left), len(right))
	}
	for key, count := range left {
		if count != 1 {
			return fmt.Errorf("validate comparable task runs: current task/run %s appears %d times", key, count)
		}
		if right[key] != 1 {
			return fmt.Errorf("validate comparable task runs: baseline task/run %s missing or duplicated", key)
		}
	}
	for key, count := range right {
		if count != 1 {
			return fmt.Errorf("validate comparable task runs: baseline task/run %s appears %d times", key, count)
		}
		if left[key] != 1 {
			return fmt.Errorf("validate comparable task runs: current task/run %s missing or duplicated", key)
		}
	}
	return nil
}

func holdoutCoverage(corpus *Corpus, scorecard *Scorecard) (expected int, covered int) {
	if corpus == nil || scorecard == nil {
		return 0, 0
	}
	holdoutIDs := make(map[string]struct{})
	for _, task := range corpus.Tasks {
		if task.Holdout {
			holdoutIDs[task.ID] = struct{}{}
		}
	}
	expected = len(holdoutIDs)
	if expected == 0 {
		return 0, 0
	}
	seen := make(map[string]struct{})
	for _, result := range scorecard.Results {
		if _, ok := holdoutIDs[result.TaskID]; ok {
			seen[result.TaskID] = struct{}{}
		}
	}
	return expected, len(seen)
}
