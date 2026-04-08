package onboarding

import (
	"strings"
	"testing"
)

func TestStepProgress_QuickPath(t *testing.T) {
	tests := []struct {
		step    Step
		current int
		total   int
	}{
		{StepWelcome, 1, 5},
		{StepLanguage, 2, 5},
		{StepAPIKey, 3, 5},
		{StepSummary, 4, 5},
		{StepSmokeTest, 5, 5},
		{StepDone, 0, 0},
	}
	for _, tt := range tests {
		current, total := stepProgress(tt.step, true)
		if current != tt.current || total != tt.total {
			t.Errorf("step %d quick: got (%d,%d), want (%d,%d)",
				tt.step, current, total, tt.current, tt.total)
		}
	}
}

func TestStepProgress_FullPath(t *testing.T) {
	tests := []struct {
		step    Step
		current int
		total   int
	}{
		{StepWelcome, 1, 8},
		{StepLanguage, 2, 8},
		{StepAPIKey, 3, 8},
		{StepPermission, 4, 8},
		{StepMCP, 5, 8},
		{StepDirectory, 6, 8},
		{StepSummary, 7, 8},
		{StepSmokeTest, 8, 8},
		{StepDone, 0, 0},
	}
	for _, tt := range tests {
		current, total := stepProgress(tt.step, false)
		if current != tt.current || total != tt.total {
			t.Errorf("step %d full: got (%d,%d), want (%d,%d)",
				tt.step, current, total, tt.current, tt.total)
		}
	}
}

func TestRenderProgress_Content(t *testing.T) {
	rendered := RenderProgress(En, StepAPIKey, false)
	if rendered == "" {
		t.Fatal("expected non-empty progress")
	}
	if !strings.Contains(rendered, "3") {
		t.Error("expected current step number in output")
	}
	if !strings.Contains(rendered, "8") {
		t.Error("expected total step count in output")
	}
	if !strings.Contains(rendered, "━") {
		t.Error("expected progress bar segments")
	}
}

func TestRenderProgress_DoneReturnsEmpty(t *testing.T) {
	rendered := RenderProgress(En, StepDone, false)
	if rendered != "" {
		t.Errorf("expected empty for StepDone, got %q", rendered)
	}
}
