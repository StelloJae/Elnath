package fault

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stello/elnath/internal/fault/faulttype"
)

func testScenario(name string, category faulttype.Category, ft faulttype.FaultType) *faulttype.Scenario {
	return &faulttype.Scenario{
		Name:      name,
		Category:  category,
		FaultType: ft,
		FaultRate: 1.0,
	}
}

func TestNoopInjectorInactive(t *testing.T) {
	inj := NoopInjector{}
	if inj.Active() {
		t.Fatal("Active() = true, want false")
	}
	if inj.ShouldFault(testScenario("noop", faulttype.CategoryTool, faulttype.FaultTransientError)) {
		t.Fatal("ShouldFault() = true, want false")
	}
	if err := inj.InjectFault(context.Background(), testScenario("noop", faulttype.CategoryTool, faulttype.FaultTransientError)); err != nil {
		t.Fatalf("InjectFault() error = %v, want nil", err)
	}
}

func TestScenarioInjectorShouldFaultRateBounds(t *testing.T) {
	never := testScenario("never", faulttype.CategoryTool, faulttype.FaultTransientError)
	never.FaultRate = 0
	neverInj := NewScenarioInjector(never, 1)
	for i := 0; i < 32; i++ {
		if neverInj.ShouldFault(never) {
			t.Fatalf("ShouldFault() true at iteration %d, want always false", i)
		}
	}

	always := testScenario("always", faulttype.CategoryTool, faulttype.FaultTransientError)
	always.FaultRate = 1
	alwaysInj := NewScenarioInjector(always, 1)
	for i := 0; i < 32; i++ {
		if !alwaysInj.ShouldFault(always) {
			t.Fatalf("ShouldFault() false at iteration %d, want always true", i)
		}
	}
}

func TestScenarioInjectorInjectFaultTransientError(t *testing.T) {
	s := testScenario("tool-bash-transient-fail", faulttype.CategoryTool, faulttype.FaultTransientError)
	inj := NewScenarioInjector(s, 1)
	err := inj.InjectFault(context.Background(), s)
	if err == nil {
		t.Fatal("InjectFault() error = nil, want non-nil")
	}
	if got := err.Error(); got == "" || !containsAll(got, s.Name, "transient error") {
		t.Fatalf("InjectFault() error = %q, want scenario name + transient marker", got)
	}
}

func TestScenarioInjectorInjectFaultTimeoutCanceledContext(t *testing.T) {
	s := testScenario("tool-web-timeout", faulttype.CategoryTool, faulttype.FaultTimeout)
	s.FaultDuration = time.Second
	inj := NewScenarioInjector(s, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := inj.InjectFault(ctx, s)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("InjectFault() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("InjectFault() elapsed = %s, want immediate return", elapsed)
	}
}

func TestScenarioInjectorInjectFaultMalformedJSON(t *testing.T) {
	s := testScenario("llm-codex-malformed-json", faulttype.CategoryLLM, faulttype.FaultMalformedJSON)
	inj := NewScenarioInjector(s, 1)
	err := inj.InjectFault(context.Background(), s)
	var malformed *MalformedJSONError
	if !errors.As(err, &malformed) {
		t.Fatalf("InjectFault() error = %T, want *MalformedJSONError", err)
	}
}

func TestScenarioInjectorInjectFaultHTTP429Burst(t *testing.T) {
	s := testScenario("llm-anthropic-429-burst", faulttype.CategoryLLM, faulttype.FaultHTTP429Burst)
	s.BurstLimit = 3
	inj := NewScenarioInjector(s, 1)
	err := inj.InjectFault(context.Background(), s)
	var http429 *HTTP429Error
	if !errors.As(err, &http429) {
		t.Fatalf("InjectFault() error = %T, want *HTTP429Error", err)
	}
	if http429.RetryAfter <= 0 {
		t.Fatalf("RetryAfter = %s, want > 0", http429.RetryAfter)
	}
}

func TestScenarioInjectorBurstCounterResetForRun(t *testing.T) {
	s := testScenario("llm-anthropic-429-burst", faulttype.CategoryLLM, faulttype.FaultHTTP429Burst)
	s.BurstLimit = 3
	inj := NewScenarioInjector(s, 1)

	for i := 0; i < 3; i++ {
		if !inj.ShouldFault(s) {
			t.Fatalf("ShouldFault() false at call %d, want true", i+1)
		}
	}
	if inj.ShouldFault(s) {
		t.Fatal("ShouldFault() fourth call = true, want false")
	}

	inj.ResetForRun()
	for i := 0; i < 3; i++ {
		if !inj.ShouldFault(s) {
			t.Fatalf("ShouldFault() after reset false at call %d, want true", i+1)
		}
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
