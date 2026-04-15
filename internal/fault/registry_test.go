package fault

import (
	"testing"

	"github.com/stello/elnath/internal/fault/faulttype"
	"github.com/stello/elnath/internal/fault/scenarios"
)

func TestNewRegistryLoadsBuiltinScenarios(t *testing.T) {
	reg := NewRegistry(scenarios.All())
	if got := len(reg.All()); got != 10 {
		t.Fatalf("len(All()) = %d, want 10", got)
	}
}

func TestScenarioRegistryGet(t *testing.T) {
	reg := NewRegistry(scenarios.All())
	s, ok := reg.Get("tool-bash-transient-fail")
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if s.Name != "tool-bash-transient-fail" {
		t.Fatalf("scenario name = %q, want tool-bash-transient-fail", s.Name)
	}
}

func TestScenarioRegistryMissing(t *testing.T) {
	reg := NewRegistry(scenarios.All())
	if s, ok := reg.Get("nonexistent"); ok || s != nil {
		t.Fatalf("Get(nonexistent) = (%v, %v), want (nil, false)", s, ok)
	}
}

func TestScenarioRegistryDuplicatePanics(t *testing.T) {
	reg := NewRegistry(nil)
	s := &faulttype.Scenario{Name: "dup", Category: faulttype.CategoryTool}
	reg.Register(s)
	defer func() {
		if recover() == nil {
			t.Fatal("Register() panic = nil, want duplicate panic")
		}
	}()
	reg.Register(s)
}
