package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stello/elnath/internal/tools"
)

func TestPlanModeToolsSwitchAndRestorePermissionMode(t *testing.T) {
	perm := NewPermission(WithMode(ModeAcceptEdits))
	controller := NewPlanModeController(perm)

	enterResult, err := NewEnterPlanModeTool(controller).Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("enter Execute error = %v", err)
	}
	if enterResult.IsError {
		t.Fatalf("enter returned error result: %s", enterResult.Output)
	}
	if perm.Mode() != ModePlan {
		t.Fatalf("mode after enter = %s, want plan", perm.Mode())
	}

	var enterOutput planModeToolOutput
	if err := json.Unmarshal([]byte(enterResult.Output), &enterOutput); err != nil {
		t.Fatalf("unmarshal enter output: %v", err)
	}
	if enterOutput.PreviousMode != "accept_edits" || enterOutput.CurrentMode != "plan" {
		t.Fatalf("enter output = %+v, want accept_edits -> plan", enterOutput)
	}
	if enterOutput.Receipt.Tool != EnterPlanModeToolName || enterOutput.Receipt.Action != "enter" {
		t.Fatalf("enter receipt identity = %+v", enterOutput.Receipt)
	}
	if enterOutput.Receipt.Persistent || enterOutput.Receipt.ExecutionAvailable || enterOutput.Receipt.ExecutionPolicy != "permission_mode_transition" {
		t.Fatalf("enter receipt boundary = %+v", enterOutput.Receipt)
	}
	if !enterOutput.Receipt.ReadOnlyAfterTransition || enterOutput.Receipt.PreviousMode != "accept_edits" || enterOutput.Receipt.CurrentMode != "plan" {
		t.Fatalf("enter receipt modes = %+v", enterOutput.Receipt)
	}

	exitResult, err := NewExitPlanModeTool(controller).Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("exit Execute error = %v", err)
	}
	if exitResult.IsError {
		t.Fatalf("exit returned error result: %s", exitResult.Output)
	}
	if perm.Mode() != ModeAcceptEdits {
		t.Fatalf("mode after exit = %s, want accept_edits", perm.Mode())
	}

	var exitOutput planModeToolOutput
	if err := json.Unmarshal([]byte(exitResult.Output), &exitOutput); err != nil {
		t.Fatalf("unmarshal exit output: %v", err)
	}
	if !exitOutput.Restored || exitOutput.CurrentMode != "accept_edits" {
		t.Fatalf("exit output = %+v, want restored accept_edits", exitOutput)
	}
	if exitOutput.Receipt.Tool != ExitPlanModeToolName || exitOutput.Receipt.Action != "exit" {
		t.Fatalf("exit receipt identity = %+v", exitOutput.Receipt)
	}
	if exitOutput.Receipt.Persistent || exitOutput.Receipt.ExecutionAvailable || exitOutput.Receipt.ExecutionPolicy != "permission_mode_transition" {
		t.Fatalf("exit receipt boundary = %+v", exitOutput.Receipt)
	}
	if exitOutput.Receipt.ReadOnlyAfterTransition || exitOutput.Receipt.PreviousMode != "accept_edits" || exitOutput.Receipt.CurrentMode != "accept_edits" || !exitOutput.Receipt.Restored {
		t.Fatalf("exit receipt modes = %+v", exitOutput.Receipt)
	}
}

func TestExitPlanModeWithoutActiveTransitionNoops(t *testing.T) {
	perm := NewPermission(WithMode(ModeBypass))
	controller := NewPlanModeController(perm)

	result, err := NewExitPlanModeTool(controller).Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("Execute returned error result: %s", result.Output)
	}
	if perm.Mode() != ModeBypass {
		t.Fatalf("mode after exit = %s, want bypass", perm.Mode())
	}

	var output planModeToolOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.Restored {
		t.Fatal("Restored = true, want false")
	}
	if output.CurrentMode != "bypass" {
		t.Fatalf("CurrentMode = %q, want bypass", output.CurrentMode)
	}
}

func TestPlanModeToolsMetadata(t *testing.T) {
	controller := NewPlanModeController(NewPermission())
	for _, tool := range []tools.Tool{
		NewEnterPlanModeTool(controller),
		NewExitPlanModeTool(controller),
	} {
		if tool.IsConcurrencySafe(nil) {
			t.Fatalf("%s should not be concurrency-safe", tool.Name())
		}
		if !tool.Reversible() {
			t.Fatalf("%s should be reversible", tool.Name())
		}
		if got := tool.Scope(nil); len(got.ReadPaths) != 0 || len(got.WritePaths) != 0 || got.Network || got.Persistent {
			t.Fatalf("%s Scope() = %+v, want empty scope", tool.Name(), got)
		}
		if tool.ShouldCancelSiblingsOnError() {
			t.Fatalf("%s should not cancel siblings", tool.Name())
		}
	}
}
