package fault

import (
	"context"
	"encoding/json"

	"github.com/stello/elnath/internal/fault/faulttype"
	"github.com/stello/elnath/internal/tools"
)

type ToolFaultHook struct {
	inner    *tools.Registry
	injector Injector
	scenario *faulttype.Scenario
}

func NewToolFaultHook(reg *tools.Registry, inj Injector, s *faulttype.Scenario) *ToolFaultHook {
	return &ToolFaultHook{inner: reg, injector: inj, scenario: s}
}

func (h *ToolFaultHook) Execute(ctx context.Context, name string, input json.RawMessage) (*tools.Result, error) {
	if !h.injector.Active() {
		return h.inner.Execute(ctx, name, input)
	}
	if h.scenario == nil || h.scenario.Category != faulttype.CategoryTool {
		return h.inner.Execute(ctx, name, input)
	}
	targeted := h.scenario.TargetTool == "" || h.scenario.TargetTool == name
	if targeted && h.injector.ShouldFault(h.scenario) {
		return nil, h.injector.InjectFault(ctx, h.scenario)
	}
	return h.inner.Execute(ctx, name, input)
}

func (h *ToolFaultHook) Registry() *tools.Registry { return h.inner }
