package eval

import (
	"encoding/json"
	"fmt"
	"os"
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

// Validate checks corpus structure deterministically.
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
