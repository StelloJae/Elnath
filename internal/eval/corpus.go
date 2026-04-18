package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// v2 bench shape constraints — see plan phase 1.
const (
	v2TrainingMin = 12
	v2TrainingMax = 15
	v2HeldOutMin  = 5
	v2HeldOutMax  = 8
	// v2DistributionEpsilon tolerates floating-point rounding when summing
	// IntentDistribution entries (e.g. 0.45 + 0.35 + 0.20 == 1.0 ± 1e-9).
	v2DistributionEpsilon = 1e-6
)

func validTrack(track Track) bool {
	switch track {
	case TrackBrownfieldFeature, TrackBugfix, TrackGreenfield, TrackResearch:
		return true
	default:
		return false
	}
}

func validLanguage(language Language) bool {
	switch language {
	case LanguageGo, LanguageTypeScript:
		return true
	default:
		return false
	}
}

// LoadCorpus reads and validates a benchmark corpus file.
func LoadCorpus(path string) (*Corpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load corpus: %w", err)
	}

	var corpus Corpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		return nil, fmt.Errorf("load corpus: parse json: %w", err)
	}

	if err := corpus.Validate(); err != nil {
		return nil, err
	}
	return &corpus, nil
}

// Validate checks corpus structure deterministically. It dispatches to
// version-specific rules so v2 corpora (which relax `Repo`/`SourceURL`/
// `RepoRef`/`AcceptanceCriteria` and require `Intent` + set membership)
// do not inherit v1 field mandates and vice versa.
func (c *Corpus) Validate() error {
	if c == nil {
		return fmt.Errorf("validate corpus: corpus is nil")
	}
	if c.Version == "" {
		return fmt.Errorf("validate corpus: version is required")
	}
	if len(c.Tasks) == 0 {
		return fmt.Errorf("validate corpus: at least one task is required")
	}

	if c.Version == "v2" {
		return c.validateV2()
	}
	return c.validateV1()
}

func (c *Corpus) validateV1() error {
	seen := make(map[string]struct{}, len(c.Tasks))
	for i, task := range c.Tasks {
		if task.ID == "" {
			return fmt.Errorf("validate corpus: tasks[%d] id is required", i)
		}
		if _, ok := seen[task.ID]; ok {
			return fmt.Errorf("validate corpus: duplicate task id %q", task.ID)
		}
		seen[task.ID] = struct{}{}
		if task.Title == "" {
			return fmt.Errorf("validate corpus: task %q title is required", task.ID)
		}
		if task.RepoClass == "" {
			return fmt.Errorf("validate corpus: task %q repo_class is required", task.ID)
		}
		if task.BenchmarkFamily == "" {
			return fmt.Errorf("validate corpus: task %q benchmark_family is required", task.ID)
		}
		if task.Prompt == "" {
			return fmt.Errorf("validate corpus: task %q prompt is required", task.ID)
		}
		if task.Repo == "" && task.SourceURL == "" {
			return fmt.Errorf("validate corpus: task %q requires repo or source_url", task.ID)
		}
		if task.Repo != "" && task.RepoRef == "" {
			return fmt.Errorf("validate corpus: task %q requires repo_ref when repo is set", task.ID)
		}
		if len(task.AcceptanceCriteria) == 0 {
			return fmt.Errorf("validate corpus: task %q requires at least one acceptance criterion", task.ID)
		}
		if !validTrack(task.Track) {
			return fmt.Errorf("validate corpus: task %q has invalid track %q", task.ID, task.Track)
		}
		if !validLanguage(task.Language) {
			return fmt.Errorf("validate corpus: task %q has invalid language %q", task.ID, task.Language)
		}
	}
	return nil
}

func (c *Corpus) validateV2() error {
	// Per-task checks: id, title, prompt, intent, valid track+language.
	// Repo / SourceURL / RepoRef / AcceptanceCriteria are optional for v2
	// because stub execution does not use real repos.
	seen := make(map[string]struct{}, len(c.Tasks))
	taskIntent := make(map[string]string, len(c.Tasks))
	for i, task := range c.Tasks {
		if task.ID == "" {
			return fmt.Errorf("validate v2 corpus: tasks[%d] id is required", i)
		}
		if _, ok := seen[task.ID]; ok {
			return fmt.Errorf("validate v2 corpus: duplicate task id %q", task.ID)
		}
		seen[task.ID] = struct{}{}
		if task.Title == "" {
			return fmt.Errorf("validate v2 corpus: task %q title is required", task.ID)
		}
		if task.Prompt == "" {
			return fmt.Errorf("validate v2 corpus: task %q prompt is required", task.ID)
		}
		if task.Intent == "" {
			return fmt.Errorf("validate v2 corpus: task %q intent is required", task.ID)
		}
		if !validTrack(task.Track) {
			return fmt.Errorf("validate v2 corpus: task %q has invalid track %q", task.ID, task.Track)
		}
		if !validLanguage(task.Language) {
			return fmt.Errorf("validate v2 corpus: task %q has invalid language %q", task.ID, task.Language)
		}
		taskIntent[task.ID] = task.Intent
	}

	// Set membership + disjointness.
	if len(c.TrainingSet) < v2TrainingMin || len(c.TrainingSet) > v2TrainingMax {
		return fmt.Errorf("validate v2 corpus: training_set size %d out of bounds [%d,%d]",
			len(c.TrainingSet), v2TrainingMin, v2TrainingMax)
	}
	if len(c.HeldOutSet) < v2HeldOutMin || len(c.HeldOutSet) > v2HeldOutMax {
		return fmt.Errorf("validate v2 corpus: held_out_set size %d out of bounds [%d,%d]",
			len(c.HeldOutSet), v2HeldOutMin, v2HeldOutMax)
	}
	training := make(map[string]struct{}, len(c.TrainingSet))
	for _, id := range c.TrainingSet {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("validate v2 corpus: training_set references unknown task %q", id)
		}
		training[id] = struct{}{}
	}
	heldOut := make(map[string]struct{}, len(c.HeldOutSet))
	for _, id := range c.HeldOutSet {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("validate v2 corpus: held_out_set references unknown task %q", id)
		}
		if _, ok := training[id]; ok {
			return fmt.Errorf("validate v2 corpus: task %q appears in both training_set and held_out_set", id)
		}
		heldOut[id] = struct{}{}
	}

	// IntentDistribution coverage.
	if len(c.IntentDistribution) == 0 {
		return fmt.Errorf("validate v2 corpus: intent_distribution is required")
	}
	var sum float64
	for intent, share := range c.IntentDistribution {
		if intent == "" {
			return fmt.Errorf("validate v2 corpus: intent_distribution contains empty intent key")
		}
		if share < 0 {
			return fmt.Errorf("validate v2 corpus: intent_distribution[%q] must be non-negative", intent)
		}
		sum += share
	}
	if math.Abs(sum-1.0) > v2DistributionEpsilon {
		return fmt.Errorf("validate v2 corpus: intent_distribution sums to %f, expected 1.0", sum)
	}
	for id, intent := range taskIntent {
		if _, ok := c.IntentDistribution[intent]; !ok {
			return fmt.Errorf("validate v2 corpus: task %q intent %q not declared in intent_distribution", id, intent)
		}
	}
	return nil
}
