package fault

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stello/elnath/internal/fault/faulttype"
	"github.com/stello/elnath/internal/tools"
)

type toolHookInjector struct {
	active      bool
	shouldFault bool
	err         error
	shouldCalls int
	injectCalls int
}

func (i *toolHookInjector) Active() bool { return i.active }

func (i *toolHookInjector) ShouldFault(_ *faulttype.Scenario) bool {
	i.shouldCalls++
	return i.shouldFault
}

func (i *toolHookInjector) InjectFault(context.Context, *faulttype.Scenario) error {
	i.injectCalls++
	return i.err
}

type hookTool struct {
	name  string
	calls int
}

func (t *hookTool) Name() string                           { return t.name }
func (t *hookTool) Description() string                    { return t.name }
func (t *hookTool) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (t *hookTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (t *hookTool) Reversible() bool                       { return false }
func (t *hookTool) Scope(json.RawMessage) tools.ToolScope  { return tools.ConservativeScope() }
func (t *hookTool) ShouldCancelSiblingsOnError() bool      { return false }
func (t *hookTool) Execute(context.Context, json.RawMessage) (*tools.Result, error) {
	t.calls++
	return tools.SuccessResult("ok"), nil
}

func TestToolFaultHookDelegatesWhenInactive(t *testing.T) {
	reg := tools.NewRegistry()
	tool := &hookTool{name: "bash"}
	reg.Register(tool)
	hook := NewToolFaultHook(reg, &toolHookInjector{}, testScenario("tool-bash-transient-fail", faulttype.CategoryTool, faulttype.FaultTransientError))

	result, err := hook.Execute(context.Background(), "bash", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result == nil || result.Output != "ok" {
		t.Fatalf("Execute() result = %#v, want success", result)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", tool.calls)
	}
}

func TestToolFaultHookDelegatesWhenShouldFaultFalse(t *testing.T) {
	reg := tools.NewRegistry()
	tool := &hookTool{name: "bash"}
	reg.Register(tool)
	inj := &toolHookInjector{active: true}
	hook := NewToolFaultHook(reg, inj, testScenario("tool-bash-transient-fail", faulttype.CategoryTool, faulttype.FaultTransientError))

	if _, err := hook.Execute(context.Background(), "bash", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", tool.calls)
	}
}

func TestToolFaultHookReturnsInjectedError(t *testing.T) {
	reg := tools.NewRegistry()
	tool := &hookTool{name: "bash"}
	reg.Register(tool)
	inj := &toolHookInjector{active: true, shouldFault: true, err: errors.New("boom")}
	hook := NewToolFaultHook(reg, inj, testScenario("tool-bash-transient-fail", faulttype.CategoryTool, faulttype.FaultTransientError))

	if _, err := hook.Execute(context.Background(), "bash", json.RawMessage(`{}`)); err == nil {
		t.Fatal("Execute() error = nil, want injected error")
	}
	if tool.calls != 0 {
		t.Fatalf("tool calls = %d, want 0", tool.calls)
	}
}

func TestToolFaultHookSkipsNonTargetTool(t *testing.T) {
	reg := tools.NewRegistry()
	tool := &hookTool{name: "read_file"}
	reg.Register(tool)
	inj := &toolHookInjector{active: true, shouldFault: true, err: errors.New("boom")}
	s := testScenario("tool-bash-transient-fail", faulttype.CategoryTool, faulttype.FaultTransientError)
	s.TargetTool = "bash"
	hook := NewToolFaultHook(reg, inj, s)

	if _, err := hook.Execute(context.Background(), "read_file", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", tool.calls)
	}
}

func TestToolFaultHookSkipsWrongCategory(t *testing.T) {
	reg := tools.NewRegistry()
	tool := &hookTool{name: "bash"}
	reg.Register(tool)
	inj := &toolHookInjector{active: true, shouldFault: true, err: errors.New("boom")}
	hook := NewToolFaultHook(reg, inj, testScenario("llm-anthropic-429-burst", faulttype.CategoryLLM, faulttype.FaultHTTP429Burst))

	if _, err := hook.Execute(context.Background(), "bash", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", tool.calls)
	}
}
