package main

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/stello/elnath/internal/orchestrator"
	"github.com/stello/elnath/internal/tools"
)

type completionChangedFilesSnapshot struct {
	Available bool
	WorkDir   string
	Files     []string
}

func (rt *executionRuntime) completionChangedFilesSnapshot(ctx context.Context, input orchestrator.WorkflowInput) completionChangedFilesSnapshot {
	if rt == nil || rt.guard == nil {
		return completionChangedFilesSnapshot{}
	}
	toolCtx := rt.toolContextForSession(ctx, input.Session)
	workDir, err := tools.SessionWorkDirFromContext(toolCtx, rt.guard)
	if err != nil || strings.TrimSpace(workDir) == "" {
		return completionChangedFilesSnapshot{}
	}
	files, err := gitChangedFiles(ctx, workDir)
	if err != nil {
		return completionChangedFilesSnapshot{WorkDir: workDir}
	}
	return completionChangedFilesSnapshot{
		Available: true,
		WorkDir:   workDir,
		Files:     files,
	}
}

func completionChangedFilesDelta(before completionChangedFilesSnapshot, after completionChangedFilesSnapshot) []string {
	if !before.Available || !after.Available || before.WorkDir != after.WorkDir {
		return nil
	}
	seen := make(map[string]struct{}, len(before.Files))
	for _, file := range before.Files {
		if normalized := normalizeCompletionScopePath(file); normalized != "" {
			seen[normalized] = struct{}{}
		}
	}
	var changed []string
	for _, file := range after.Files {
		normalized := normalizeCompletionScopePath(file)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		changed = append(changed, normalized)
	}
	sort.Strings(changed)
	return changed
}

func gitChangedFiles(ctx context.Context, workDir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var files []string
	for _, args := range [][]string{
		{"diff", "--name-only"},
		{"diff", "--cached", "--name-only"},
		{"ls-files", "--others", "--exclude-standard"},
	} {
		out, err := gitOutput(ctx, workDir, args...)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(out, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				files = append(files, trimmed)
			}
		}
	}
	return normalizeCompletionScopePaths(files), nil
}

func gitOutput(ctx context.Context, workDir string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func mergeCompletionScopePaths(a []string, b []string) []string {
	if len(a) == 0 {
		return normalizeCompletionScopePaths(b)
	}
	if len(b) == 0 {
		return normalizeCompletionScopePaths(a)
	}
	merged := make([]string, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	return normalizeCompletionScopePaths(merged)
}

func withCompletionRetryChangedFiles(summary completionContractSummary, changedFiles []string) completionContractSummary {
	changedFiles = normalizeCompletionScopePaths(changedFiles)
	if len(changedFiles) == 0 {
		return summary
	}
	summary.MutatedPaths = mergeCompletionScopePaths(summary.MutatedPaths, changedFiles)
	outOfScope := outOfScopeMutatingPaths(summary.MutatedPaths, summary.AllowedRecoveryPaths, summary.ForbiddenRecoveryPaths)
	summary.OutOfScopeChangedFiles = mergeCompletionScopePaths(summary.OutOfScopeChangedFiles, outOfScope)
	if len(summary.OutOfScopeChangedFiles) > 0 {
		summary.CompletionWarning = "scope_drift"
	}
	return summary
}
