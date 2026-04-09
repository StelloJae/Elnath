package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunBaselinePlan executes a baseline wrapper against every task in the corpus.
func RunBaselinePlan(plan *BaselineRunPlan) (*Scorecard, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	for _, envKey := range plan.RequiredEnv {
		if os.Getenv(envKey) == "" {
			return nil, fmt.Errorf("run baseline plan: required env %s is not set", envKey)
		}
	}

	corpus, err := LoadCorpus(plan.CorpusPath)
	if err != nil {
		return nil, err
	}

	repeats := plan.RepeatedRuns
	if repeats <= 0 {
		repeats = 1
	}

	tempDir, err := os.MkdirTemp("", "elnath-baseline-run-*")
	if err != nil {
		return nil, fmt.Errorf("run baseline plan: tempdir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	scorecard := &Scorecard{
		Version:           plan.Version,
		System:            plan.System,
		Baseline:          plan.Baseline,
		Context:           plan.Context,
		RuntimePolicy:     plan.RuntimePolicy,
		RepeatedRuns:      repeats,
		InterventionNotes: plan.InterventionNotes,
		Results:           make([]RunResult, 0, len(corpus.Tasks)*repeats),
	}

	for run := 1; run <= repeats; run++ {
		for _, task := range corpus.Tasks {
			taskOutput := filepath.Join(tempDir, fmt.Sprintf("%s-run-%d.json", task.ID, run))
			command := renderCommandTemplate(plan.CommandTemplate, map[string]string{
				"corpus_path":           plan.CorpusPath,
				"task_id":               task.ID,
				"task_title":            task.Title,
				"task_prompt":           task.Prompt,
				"task_repo":             task.Repo,
				"task_repo_ref":         task.RepoRef,
				"task_source_url":       task.SourceURL,
				"task_track":            string(task.Track),
				"task_language":         string(task.Language),
				"task_repo_class":       task.RepoClass,
				"task_benchmark_family": task.BenchmarkFamily,
				"task_output":           taskOutput,
			})

			cmd := exec.Command("bash", "-lc", command)
			output, runErr := cmd.CombinedOutput()
			result, loadErr := loadRunResult(taskOutput)
			if loadErr != nil {
				if runErr != nil {
					return nil, fmt.Errorf("run baseline plan: task %s run %d failed: %w: %s", task.ID, run, runErr, strings.TrimSpace(string(output)))
				}
				return nil, fmt.Errorf("run baseline plan: task %s run %d result: %w", task.ID, run, loadErr)
			}
			if result.TaskID == "" {
				result.TaskID = task.ID
			}
			if result.Track == "" {
				result.Track = task.Track
			}
			if result.Language == "" {
				result.Language = task.Language
			}
			result.Run = run
			scorecard.Results = append(scorecard.Results, *result)
			if err := writeScorecard(plan.OutputPath, scorecard); err != nil {
				return nil, err
			}
		}
	}

	if err := scorecard.Validate(); err != nil {
		return nil, err
	}
	if err := writeScorecard(plan.OutputPath, scorecard); err != nil {
		return nil, err
	}
	return scorecard, nil
}

func loadRunResult(path string) (*RunResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load run result: %w", err)
	}
	var result RunResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("load run result: parse json: %w", err)
	}
	return &result, nil
}

func renderCommandTemplate(template string, values map[string]string) string {
	rendered := template
	for key, value := range values {
		rendered = strings.ReplaceAll(rendered, "{{"+key+"}}", shellQuote(value))
	}
	return rendered
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func writeScorecard(path string, scorecard *Scorecard) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("run baseline plan: mkdir output dir: %w", err)
	}
	data, err := json.MarshalIndent(scorecard, "", "  ")
	if err != nil {
		return fmt.Errorf("run baseline plan: marshal scorecard: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("run baseline plan: write scorecard: %w", err)
	}
	return nil
}
