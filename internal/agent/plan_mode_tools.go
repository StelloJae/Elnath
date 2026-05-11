package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/stello/elnath/internal/tools"
)

const (
	EnterPlanModeToolName = "enter_plan_mode"
	ExitPlanModeToolName  = "exit_plan_mode"
)

type PlanModeController struct {
	permission *Permission
	mu         sync.Mutex
	active     bool
	previous   PermissionMode
}

func NewPlanModeController(permission *Permission) *PlanModeController {
	return &PlanModeController{permission: permission}
}

func (c *PlanModeController) Enter() (previous PermissionMode, current PermissionMode) {
	if c == nil || c.permission == nil {
		return ModeDefault, ModeDefault
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.active {
		c.previous = c.permission.Mode()
		c.active = true
	}
	c.permission.SetMode(ModePlan)
	return c.previous, ModePlan
}

func (c *PlanModeController) Exit() (previous PermissionMode, current PermissionMode, restored bool) {
	if c == nil || c.permission == nil {
		return ModeDefault, ModeDefault, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.active {
		current = c.permission.Mode()
		return current, current, false
	}
	previous = c.previous
	c.permission.SetMode(previous)
	c.active = false
	return previous, previous, true
}

type EnterPlanModeTool struct {
	controller *PlanModeController
}

func NewEnterPlanModeTool(controller *PlanModeController) *EnterPlanModeTool {
	return &EnterPlanModeTool{controller: controller}
}

func (t *EnterPlanModeTool) Name() string { return EnterPlanModeToolName }

func (t *EnterPlanModeTool) Description() string {
	return "Switch this session into read-only planning mode before implementation"
}

func (t *EnterPlanModeTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{}, nil)
}

func (t *EnterPlanModeTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *EnterPlanModeTool) Reversible() bool { return true }

func (t *EnterPlanModeTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *EnterPlanModeTool) ShouldCancelSiblingsOnError() bool { return false }

type planModeToolOutput struct {
	Message      string `json:"message"`
	PreviousMode string `json:"previous_mode"`
	CurrentMode  string `json:"current_mode"`
	Restored     bool   `json:"restored,omitempty"`
}

func (t *EnterPlanModeTool) Execute(_ context.Context, _ json.RawMessage) (*tools.Result, error) {
	if t == nil || t.controller == nil {
		return tools.ErrorResult("enter_plan_mode: controller unavailable"), nil
	}
	previous, current := t.controller.Enter()
	return planModeResult(planModeToolOutput{
		Message:      "Entered plan mode. Continue with read-only exploration, then present or record a concrete implementation plan before editing.",
		PreviousMode: previous.String(),
		CurrentMode:  current.String(),
	})
}

type ExitPlanModeTool struct {
	controller *PlanModeController
}

func NewExitPlanModeTool(controller *PlanModeController) *ExitPlanModeTool {
	return &ExitPlanModeTool{controller: controller}
}

func (t *ExitPlanModeTool) Name() string { return ExitPlanModeToolName }

func (t *ExitPlanModeTool) Description() string {
	return "Exit planning mode and restore the previous permission mode"
}

func (t *ExitPlanModeTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{}, nil)
}

func (t *ExitPlanModeTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *ExitPlanModeTool) Reversible() bool { return true }

func (t *ExitPlanModeTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *ExitPlanModeTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *ExitPlanModeTool) Execute(_ context.Context, _ json.RawMessage) (*tools.Result, error) {
	if t == nil || t.controller == nil {
		return tools.ErrorResult("exit_plan_mode: controller unavailable"), nil
	}
	previous, current, restored := t.controller.Exit()
	message := "No active plan mode transition was found. Permission mode was left unchanged."
	if restored {
		message = "Exited plan mode and restored the previous permission mode."
	}
	return planModeResult(planModeToolOutput{
		Message:      message,
		PreviousMode: previous.String(),
		CurrentMode:  current.String(),
		Restored:     restored,
	})
}

func planModeResult(output planModeToolOutput) (*tools.Result, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("plan mode: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(raw)), nil
}
