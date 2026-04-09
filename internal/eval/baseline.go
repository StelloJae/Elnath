package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// NewBaselineRunPlan returns a starter plan for running an external baseline.
func NewBaselineRunPlan(corpusPath string) BaselineRunPlan {
	return BaselineRunPlan{
		Version:           "v1",
		System:            "baseline-runner",
		Baseline:          "claude-codex-omx-omc",
		CorpusPath:        corpusPath,
		CommandTemplate:   "\"$BASELINE_BIN\" {{task_output}} {{task_id}} {{task_track}} {{task_language}} {{task_prompt}} {{task_repo}} {{task_repo_ref}} {{task_repo_class}} {{task_benchmark_family}}",
		OutputPath:        "benchmarks/results/baseline-scorecard.v1.json",
		Context:           "benchmark",
		RuntimePolicy:     "",
		RepeatedRuns:      1,
		InterventionNotes: false,
		RequiredEnv:       []string{"BASELINE_BIN"},
		Notes: []string{
			"Replace BASELINE_BIN with the wrapper that runs Claude Code / Codex CLI under your OMX/OMC workflow.",
			"The wrapper command should write one RunResult JSON object to the provided {{task_output}} path.",
			"RunResult may omit task_id/track/language; the runner will backfill from the corpus.",
			"Set runtime_policy to the exact sandbox/approval mode used for the benchmark run before publishing results.",
			"Do not count hidden manual rescue as a clean success.",
		},
	}
}

// LoadBaselineRunPlan reads and validates a baseline run plan file.
func LoadBaselineRunPlan(path string) (*BaselineRunPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load baseline plan: %w", err)
	}
	var plan BaselineRunPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("load baseline plan: parse json: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return &plan, nil
}

// Validate checks the baseline plan shape.
func (p *BaselineRunPlan) Validate() error {
	if p == nil {
		return fmt.Errorf("validate baseline plan: plan is nil")
	}
	if p.Version == "" {
		return fmt.Errorf("validate baseline plan: version is required")
	}
	if p.Baseline == "" {
		return fmt.Errorf("validate baseline plan: baseline is required")
	}
	if p.System == "" {
		p.System = "baseline-runner"
	}
	if p.CorpusPath == "" {
		return fmt.Errorf("validate baseline plan: corpus_path is required")
	}
	if p.CommandTemplate == "" {
		return fmt.Errorf("validate baseline plan: command_template is required")
	}
	if p.OutputPath == "" {
		return fmt.Errorf("validate baseline plan: output_path is required")
	}
	if p.RepeatedRuns < 0 {
		return fmt.Errorf("validate baseline plan: repeated_runs must be >= 0")
	}
	for _, envKey := range p.RequiredEnv {
		if envKey == "" {
			return fmt.Errorf("validate baseline plan: required_env entries must be non-empty")
		}
	}
	return nil
}

// NewCurrentRunPlan returns a starter plan for evaluating Elnath itself under the same contract.
func NewCurrentRunPlan(corpusPath string) BaselineRunPlan {
	return BaselineRunPlan{
		Version:           "v1",
		System:            "elnath-current",
		Baseline:          "self",
		CorpusPath:        corpusPath,
		CommandTemplate:   "\"$CURRENT_BIN\" {{task_output}} {{task_id}} {{task_track}} {{task_language}} {{task_prompt}} {{task_repo}} {{task_repo_ref}} {{task_repo_class}} {{task_benchmark_family}}",
		OutputPath:        "benchmarks/results/current-scorecard.v1.json",
		Context:           "benchmark",
		RuntimePolicy:     "",
		RepeatedRuns:      1,
		InterventionNotes: true,
		RequiredEnv:       []string{"CURRENT_BIN"},
		Notes: []string{
			"Replace CURRENT_BIN with the wrapper that executes Elnath on one task and writes one RunResult JSON file.",
			"Use the same task contract as the external baseline so diffs remain fair.",
			"Set runtime_policy to the exact sandbox/approval mode used for the benchmark run before publishing results.",
			"Record intervention metadata honestly if human steering occurred.",
		},
	}
}

// WriteBaselineRunPlan writes a baseline scaffold to disk.
func WriteBaselineRunPlan(path string, plan BaselineRunPlan) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("write baseline plan: marshal: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write baseline plan: %w", err)
	}
	return nil
}
