package eval

import (
	"bytes"
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
	keepTmp := os.Getenv("ELNATH_BENCHMARK_KEEP_TMP") == "1"
	debugDir := ""
	if keepTmp {
		debugDir = debugEvidenceDir(plan.OutputPath)
		if err := os.MkdirAll(debugDir, 0o755); err != nil {
			return nil, fmt.Errorf("run baseline plan: mkdir debug evidence dir: %w", err)
		}
	}

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
				"corpus_path":               plan.CorpusPath,
				"task_id":                   task.ID,
				"task_title":                task.Title,
				"task_prompt":               task.Prompt,
				"task_repo":                 task.Repo,
				"task_repo_ref":             task.RepoRef,
				"task_source_url":           task.SourceURL,
				"task_verification_command": task.VerificationCommand,
				"task_track":                string(task.Track),
				"task_language":             string(task.Language),
				"task_repo_class":           task.RepoClass,
				"task_benchmark_family":     task.BenchmarkFamily,
				"task_output":               taskOutput,
			})

			cmd := exec.Command("bash", "-lc", command)
			cmd.Env = append(os.Environ(),
				"ELNATH_BENCHMARK_TASK_VERIFICATION_COMMAND="+task.VerificationCommand,
			)
			var stdoutPath, stderrPath, sidecarPath, publicSidecarPath string
			if keepTmp {
				base := filepath.Join(debugDir, fmt.Sprintf("%s-run-%d", safeArtifactName(task.ID), run))
				stdoutPath = base + ".wrapper.stdout"
				stderrPath = base + ".wrapper.stderr"
				sidecarPath = base + ".debug-evidence.json"
				publicSidecarPath = relativeToOutputDir(plan.OutputPath, sidecarPath)
				cmd.Env = append(cmd.Env,
					"ELNATH_BENCHMARK_WRAPPER_STDOUT_PATH="+stdoutPath,
					"ELNATH_BENCHMARK_WRAPPER_STDERR_PATH="+stderrPath,
					"ELNATH_BENCHMARK_DEBUG_EVIDENCE_PATH="+sidecarPath,
					"ELNATH_BENCHMARK_DEBUG_EVIDENCE_PUBLIC_PATH="+publicSidecarPath,
				)
			}
			output, runErr := runCommandWithSidecars(cmd, stdoutPath, stderrPath)
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
			enrichPatchQuality(task, result)
			if keepTmp {
				if err := materializeDebugEvidence(result, sidecarPath, stdoutPath, stderrPath, plan.OutputPath); err != nil {
					return nil, err
				}
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

func runCommandWithSidecars(cmd *exec.Cmd, stdoutPath, stderrPath string) ([]byte, error) {
	if stdoutPath == "" && stderrPath == "" {
		return cmd.CombinedOutput()
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdoutPath != "" {
		if writeErr := writeBytesFile(stdoutPath, stdout.Bytes()); writeErr != nil && err == nil {
			err = writeErr
		}
	}
	if stderrPath != "" {
		if writeErr := writeBytesFile(stderrPath, stderr.Bytes()); writeErr != nil && err == nil {
			err = writeErr
		}
	}
	combined := append(stdout.Bytes(), stderr.Bytes()...)
	return combined, err
}

func writeBytesFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("run baseline plan: mkdir sidecar dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("run baseline plan: write sidecar: %w", err)
	}
	return nil
}

func debugEvidenceDir(outputPath string) string {
	ext := filepath.Ext(outputPath)
	if ext == "" {
		return outputPath + ".debug"
	}
	return strings.TrimSuffix(outputPath, ext) + ".debug"
}

func safeArtifactName(value string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		" ", "_",
		":", "_",
	)
	name := replacer.Replace(value)
	if name == "" {
		return "task"
	}
	return name
}

func materializeDebugEvidence(result *RunResult, sidecarPath, stdoutPath, stderrPath, outputPath string) error {
	evidence := DebugEvidence{}
	if result.DebugEvidence != nil {
		evidence = *result.DebugEvidence
		if result.DebugEvidence.SidecarPath != "" {
			sidecarEvidence, err := loadDebugEvidenceSidecar(result.DebugEvidence.SidecarPath, outputPath)
			if err == nil && sidecarEvidence != nil {
				evidence = *sidecarEvidence
			}
		}
	}
	evidence.SidecarPath = ""
	if evidence.WrapperStdoutPath == "" {
		evidence.WrapperStdoutPath = stdoutPath
	}
	if evidence.WrapperStderrPath == "" {
		evidence.WrapperStderrPath = stderrPath
	}
	scrubDebugEvidencePaths(&evidence, filepath.Dir(sidecarPath))
	if err := writeDebugEvidenceSidecar(sidecarPath, &evidence); err != nil {
		return err
	}
	result.DebugEvidence = &DebugEvidence{SidecarPath: relativeToOutputDir(outputPath, sidecarPath)}
	return nil
}

func loadDebugEvidenceSidecar(path, outputPath string) (*DebugEvidence, error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(outputPath), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var evidence DebugEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return nil, err
	}
	return &evidence, nil
}

func writeDebugEvidenceSidecar(path string, evidence *DebugEvidence) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("run baseline plan: mkdir debug evidence dir: %w", err)
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fmt.Errorf("run baseline plan: marshal debug evidence: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("run baseline plan: write debug evidence: %w", err)
	}
	return nil
}

func scrubDebugEvidencePaths(evidence *DebugEvidence, sidecarDir string) {
	retainedRoot := trustedRetainedRoot(evidence.RetainedTempRoot)
	evidence.RetainedTempRoot = retainedRoot
	allowedRoots := []string{sidecarDir}
	if retainedRoot != "" {
		allowedRoots = append(allowedRoots, retainedRoot)
	}
	evidence.WrapperStdoutPath = existingPathInRoots(evidence.WrapperStdoutPath, allowedRoots)
	evidence.WrapperStderrPath = existingPathInRoots(evidence.WrapperStderrPath, allowedRoots)
	evidence.RunLogPath = existingPathInRoots(evidence.RunLogPath, allowedRoots)
	evidence.RecoveryLogPath = existingPathInRoots(evidence.RecoveryLogPath, allowedRoots)
	evidence.VerificationLogPath = existingPathInRoots(evidence.VerificationLogPath, allowedRoots)
	evidence.VerificationRetryLogPath = existingPathInRoots(evidence.VerificationRetryLogPath, allowedRoots)
	evidence.DiffPath = existingPathInRoots(evidence.DiffPath, allowedRoots)
	evidence.WorktreeStatusPath = existingPathInRoots(evidence.WorktreeStatusPath, allowedRoots)
}

func existingDir(path string) string {
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ""
	}
	return path
}

func trustedRetainedRoot(path string) string {
	root := existingDir(path)
	if root == "" {
		return ""
	}
	base := filepath.Base(root)
	if strings.HasPrefix(base, "elnath-current-benchmark.") || strings.HasPrefix(base, "elnath-baseline-benchmark.") {
		return root
	}
	return ""
}

func existingPathInRoots(path string, roots []string) string {
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		if pathInsideRoot(path, root) {
			return path
		}
	}
	return ""
}

func pathInsideRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func relativeToOutputDir(outputPath, path string) string {
	rel, err := filepath.Rel(filepath.Dir(outputPath), path)
	if err != nil {
		return filepath.Base(path)
	}
	return rel
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
