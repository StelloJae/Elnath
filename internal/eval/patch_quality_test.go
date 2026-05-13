package eval

import "testing"

func TestClassifyPatchQualityStrongWithProductionAndTestDiff(t *testing.T) {
	task := Task{
		ID:                 "V8-JS-BUG-001",
		AcceptanceCriteria: []string{"mounted-app next('router') behavior is covered by a regression test"},
	}
	result := RunResult{
		Success:            true,
		VerificationPassed: true,
		ChangedFiles:       []string{"lib/application.js", "test/app.use.js"},
	}

	status, findings := ClassifyPatchQuality(task, result)
	if status != PatchQualityStrong {
		t.Fatalf("status = %q, want %q; findings=%v", status, PatchQualityStrong, findings)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %v, want none", findings)
	}
}

func TestClassifyPatchQualityWeakForLockOnlyDiff(t *testing.T) {
	task := Task{
		ID:                 "V8-MIX-BF-001",
		AcceptanceCriteria: []string{"the transformer or configuration behavior is covered by a fixture test"},
	}
	result := RunResult{
		Success:            true,
		VerificationPassed: true,
		ChangedFiles:       []string{"go.work.sum"},
	}

	status, findings := ClassifyPatchQuality(task, result)
	if status != PatchQualityWeak {
		t.Fatalf("status = %q, want %q; findings=%v", status, PatchQualityWeak, findings)
	}
	if !hasPatchQualityFinding(findings, PatchQualityFindingLockOrChecksumOnlyDiff) {
		t.Fatalf("findings = %v, want %q", findings, PatchQualityFindingLockOrChecksumOnlyDiff)
	}
	if !hasPatchQualityFinding(findings, PatchQualityFindingMissingTestOrFixtureDiff) {
		t.Fatalf("findings = %v, want %q", findings, PatchQualityFindingMissingTestOrFixtureDiff)
	}
}

func TestClassifyPatchQualityWeakForProductionOnlyDiffWhenTestsRequired(t *testing.T) {
	task := Task{
		ID:                 "V8-PY-BUG-001",
		AcceptanceCriteria: []string{"the option propagation edge case is covered by tests"},
	}
	result := RunResult{
		Success:            true,
		VerificationPassed: true,
		ChangedFiles:       []string{"src/requests/sessions.py"},
	}

	status, findings := ClassifyPatchQuality(task, result)
	if status != PatchQualityWeak {
		t.Fatalf("status = %q, want %q; findings=%v", status, PatchQualityWeak, findings)
	}
	if !hasPatchQualityFinding(findings, PatchQualityFindingMissingTestOrFixtureDiff) {
		t.Fatalf("findings = %v, want %q", findings, PatchQualityFindingMissingTestOrFixtureDiff)
	}
}

func TestClassifyPatchQualityMissingDiffForEditIntentWithoutChangedFiles(t *testing.T) {
	task := Task{ID: "V8-TS-BUG-003"}
	result := RunResult{
		Success:            true,
		VerificationPassed: true,
		EditIntentDetected: true,
	}

	status, findings := ClassifyPatchQuality(task, result)
	if status != PatchQualityMissingDiff {
		t.Fatalf("status = %q, want %q; findings=%v", status, PatchQualityMissingDiff, findings)
	}
	if !hasPatchQualityFinding(findings, PatchQualityFindingNoChangedFiles) {
		t.Fatalf("findings = %v, want %q", findings, PatchQualityFindingNoChangedFiles)
	}
}

func TestClassifyPatchQualityLeavesFailedTasksUnclassified(t *testing.T) {
	task := Task{ID: "V8-GO-BUG-003", AcceptanceCriteria: []string{"regression test exists"}}
	result := RunResult{
		Success:            false,
		VerificationPassed: false,
		FailureFamily:      "verification_failed",
		ChangedFiles:       []string{"bug.go", "bug_test.go"},
	}

	status, findings := ClassifyPatchQuality(task, result)
	if status != "" || len(findings) != 0 {
		t.Fatalf("status=%q findings=%v, want unclassified failed task", status, findings)
	}
}

func hasPatchQualityFinding(findings []string, want string) bool {
	for _, finding := range findings {
		if finding == want {
			return true
		}
	}
	return false
}
