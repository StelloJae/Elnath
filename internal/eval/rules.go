package eval

import (
	"fmt"
	"strings"
)

// CheckAntiVanityRules applies the Month-1 anti-vanity benchmark rules.
func CheckAntiVanityRules(corpus *Corpus, scorecard *Scorecard) []RuleViolation {
	var violations []RuleViolation

	if corpus == nil {
		return append(violations, RuleViolation{
			Rule:     "corpus_required",
			Severity: "error",
			Message:  "anti-vanity checks require a corpus",
		})
	}
	if scorecard == nil {
		return append(violations, RuleViolation{
			Rule:     "scorecard_required",
			Severity: "error",
			Message:  "anti-vanity checks require a scorecard",
		})
	}

	taskIndex := make(map[string]Task, len(corpus.Tasks))
	for _, task := range corpus.Tasks {
		taskIndex[task.ID] = task
		if task.Repo == "" && task.SourceURL == "" {
			violations = append(violations, RuleViolation{
				Rule:     "toy_task_source",
				Severity: "error",
				Message:  fmt.Sprintf("task %s lacks repo/source grounding", task.ID),
			})
		}
		if len(task.AcceptanceCriteria) == 0 {
			violations = append(violations, RuleViolation{
				Rule:     "acceptance_criteria_required",
				Severity: "error",
				Message:  fmt.Sprintf("task %s lacks acceptance criteria", task.ID),
			})
		}
		if task.RepoClass == "" {
			violations = append(violations, RuleViolation{
				Rule:     "repo_class_required",
				Severity: "error",
				Message:  fmt.Sprintf("task %s lacks repo_class", task.ID),
			})
		}
		if task.BenchmarkFamily == "" {
			violations = append(violations, RuleViolation{
				Rule:     "benchmark_family_required",
				Severity: "error",
				Message:  fmt.Sprintf("task %s lacks benchmark_family", task.ID),
			})
		}
	}

	for _, result := range scorecard.Results {
		task, ok := taskIndex[result.TaskID]
		if !ok {
			violations = append(violations, RuleViolation{
				Rule:     "unknown_task",
				Severity: "error",
				Message:  fmt.Sprintf("scorecard result %s is not present in corpus", result.TaskID),
			})
			continue
		}
		if task.Track != result.Track || task.Language != result.Language {
			violations = append(violations, RuleViolation{
				Rule:     "task_mismatch",
				Severity: "error",
				Message:  fmt.Sprintf("scorecard result %s track/language mismatch corpus definition", result.TaskID),
			})
		}
		if result.Success && result.InterventionNeeded && !scorecard.InterventionNotes {
			violations = append(violations, RuleViolation{
				Rule:     "hidden_human_rescue",
				Severity: "error",
				Message:  fmt.Sprintf("successful task %s required intervention but scorecard does not declare intervention notes", result.TaskID),
			})
		}
		if result.InterventionNeeded {
			switch result.InterventionClass {
			case "necessary", "avoidable", "late":
			default:
				violations = append(violations, RuleViolation{
					Rule:     "invalid_intervention_class",
					Severity: "error",
					Message:  fmt.Sprintf("result %s has invalid intervention_class %q", result.TaskID, result.InterventionClass),
				})
			}
		}
	}

	if scorecard.Context == "launch" && scorecard.RepeatedRuns < 2 {
		violations = append(violations, RuleViolation{
			Rule:     "one_shot_launch_claim",
			Severity: "error",
			Message:  "launch-context scorecards require repeated_runs >= 2",
		})
	}
	if scorecard.Context == "benchmark" && strings.TrimSpace(scorecard.RuntimePolicy) == "" {
		violations = append(violations, RuleViolation{
			Rule:     "runtime_policy_required",
			Severity: "error",
			Message:  "benchmark-context scorecards must record runtime_policy so sandbox/approval choices are disclosed",
		})
	}
	if scorecard.RepeatedRuns < 1 {
		violations = append(violations, RuleViolation{
			Rule:     "repeated_runs_required",
			Severity: "error",
			Message:  "scorecard must record repeated_runs >= 1",
		})
	}

	return violations
}
