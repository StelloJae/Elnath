package eval

import (
	"path/filepath"
	"strings"
)

const (
	PatchQualityStrong      = "strong"
	PatchQualityWeak        = "weak"
	PatchQualityMissingDiff = "missing_diff"
)

const (
	PatchQualityFindingNoChangedFiles           = "no_changed_files"
	PatchQualityFindingLockOrChecksumOnlyDiff   = "lock_or_checksum_only_diff"
	PatchQualityFindingMissingTestOrFixtureDiff = "missing_test_or_fixture_diff"
	PatchQualityFindingTestOnlyDiff             = "test_only_diff"
	PatchQualityFindingDocsOnlyDiff             = "docs_only_diff"
)

// ClassifyPatchQuality marks whether a successful verified run has durable patch evidence.
func ClassifyPatchQuality(task Task, result RunResult) (string, []string) {
	if !result.Success || !result.VerificationPassed {
		return "", nil
	}
	if len(result.ChangedFiles) == 0 {
		if result.EditIntentDetected {
			return PatchQualityMissingDiff, []string{PatchQualityFindingNoChangedFiles}
		}
		return "", nil
	}

	findings := make([]string, 0, 2)
	allLockOrChecksum := true
	allDocs := true
	hasTestOrFixture := false
	hasProduction := false

	for _, changed := range result.ChangedFiles {
		path := normalizeChangedPath(changed)
		if !isLockOrChecksumFile(path) {
			allLockOrChecksum = false
		}
		if !isDocsFile(path) {
			allDocs = false
		}
		if isTestOrFixtureFile(path) {
			hasTestOrFixture = true
			continue
		}
		if !isLockOrChecksumFile(path) && !isDocsFile(path) {
			hasProduction = true
		}
	}

	if allLockOrChecksum {
		findings = append(findings, PatchQualityFindingLockOrChecksumOnlyDiff)
	}
	if allDocs {
		findings = append(findings, PatchQualityFindingDocsOnlyDiff)
	}
	if taskRequiresTestOrFixtureEvidence(task) && !hasTestOrFixture {
		findings = append(findings, PatchQualityFindingMissingTestOrFixtureDiff)
	}
	if hasTestOrFixture && !hasProduction {
		findings = append(findings, PatchQualityFindingTestOnlyDiff)
	}
	if len(findings) > 0 {
		return PatchQualityWeak, findings
	}
	return PatchQualityStrong, nil
}

func enrichPatchQuality(task Task, result *RunResult) {
	if result == nil || result.PatchQuality != "" {
		return
	}
	status, findings := ClassifyPatchQuality(task, *result)
	result.PatchQuality = status
	result.PatchQualityFindings = findings
}

func validPatchQuality(status string) bool {
	switch status {
	case "", PatchQualityStrong, PatchQualityWeak, PatchQualityMissingDiff:
		return true
	default:
		return false
	}
}

func taskRequiresTestOrFixtureEvidence(task Task) bool {
	for _, criterion := range task.AcceptanceCriteria {
		text := strings.ToLower(criterion)
		if strings.Contains(text, "covered by") ||
			strings.Contains(text, "regression") ||
			strings.Contains(text, "fixture") ||
			strings.Contains(text, "coverage") ||
			strings.Contains(text, "test case") ||
			strings.Contains(text, "focused test") {
			return true
		}
	}
	return false
}

func normalizeChangedPath(path string) string {
	return filepath.ToSlash(strings.TrimSpace(path))
}

func isLockOrChecksumFile(path string) bool {
	base := filepath.Base(path)
	switch base {
	case "go.sum", "go.work.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "poetry.lock", "Pipfile.lock":
		return true
	default:
		return false
	}
}

func isDocsFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "docs/") ||
		strings.Contains(lower, "/docs/") ||
		strings.HasSuffix(lower, ".md") ||
		strings.HasSuffix(lower, ".mdx") ||
		strings.HasSuffix(lower, ".rst") ||
		strings.HasSuffix(lower, ".txt")
}

func isTestOrFixtureFile(path string) bool {
	lower := strings.ToLower(path)
	base := filepath.Base(lower)
	return strings.HasPrefix(lower, "test/") ||
		strings.HasPrefix(lower, "tests/") ||
		strings.HasPrefix(lower, "__tests__/") ||
		strings.HasPrefix(lower, "spec/") ||
		strings.HasPrefix(lower, "fixtures/") ||
		strings.HasPrefix(lower, "fixture/") ||
		strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/__tests__/") ||
		strings.Contains(lower, "/spec/") ||
		strings.Contains(lower, "/fixtures/") ||
		strings.Contains(lower, "/fixture/") ||
		strings.Contains(base, "fixture") ||
		strings.HasSuffix(base, "_test.go") ||
		strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(base, "_test.py") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.")
}
