package fault

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stello/elnath/internal/fault/faulttype"
)

type Injector interface {
	Active() bool
	ShouldFault(s *faulttype.Scenario) bool
	InjectFault(ctx context.Context, s *faulttype.Scenario) error
}

type ScenarioInjector struct {
	scenario   *faulttype.Scenario
	rng        *rand.Rand
	rngMu      sync.Mutex
	active     atomic.Bool
	burstCount atomic.Int64
	faultCount atomic.Int64
	burstLimit int
}

func NewScenarioInjector(s *faulttype.Scenario, seed int64) *ScenarioInjector {
	inj := &ScenarioInjector{
		scenario:   s,
		rng:        rand.New(rand.NewSource(seed)),
		burstLimit: s.BurstLimit,
	}
	inj.active.Store(true)
	return inj
}

func (i *ScenarioInjector) Active() bool { return i.active.Load() }

func (i *ScenarioInjector) ShouldFault(s *faulttype.Scenario) bool {
	if !i.Active() {
		return false
	}
	if s == nil {
		s = i.scenario
	}
	if s == nil {
		return false
	}
	if s.FaultType == faulttype.FaultHTTP429Burst {
		n := i.burstCount.Add(1)
		return n <= int64(i.burstLimit)
	}
	i.rngMu.Lock()
	value := i.rng.Float64()
	i.rngMu.Unlock()
	return value < s.FaultRate
}

func (i *ScenarioInjector) ResetForRun() {
	i.burstCount.Store(0)
	i.faultCount.Store(0)
}

func (i *ScenarioInjector) FaultCount() int64 { return i.faultCount.Load() }

func (i *ScenarioInjector) InjectFault(ctx context.Context, s *faulttype.Scenario) error {
	if s == nil {
		s = i.scenario
	}
	if s == nil {
		return nil
	}
	i.faultCount.Add(1)
	switch s.FaultType {
	case faulttype.FaultTransientError:
		return fmt.Errorf("fault: injected transient error (%s)", s.Name)
	case faulttype.FaultPermDenied:
		return fmt.Errorf("fault: injected permission denied (%s): %w", s.Name, os.ErrPermission)
	case faulttype.FaultTimeout:
		dur := s.FaultDuration
		if dur == 0 {
			dur = 30 * time.Second
		}
		select {
		case <-time.After(dur):
		case <-ctx.Done():
		}
		return context.DeadlineExceeded
	case faulttype.FaultMalformedJSON:
		return &MalformedJSONError{Scenario: s.Name}
	case faulttype.FaultHTTP429Burst:
		return &HTTP429Error{Scenario: s.Name, RetryAfter: time.Second}
	default:
		return fmt.Errorf("fault: unknown fault type %q in scenario %q", s.FaultType, s.Name)
	}
}

type NoopInjector struct{}

func (NoopInjector) Active() bool { return false }

func (NoopInjector) ShouldFault(_ *faulttype.Scenario) bool { return false }

func (NoopInjector) InjectFault(_ context.Context, _ *faulttype.Scenario) error { return nil }

type MalformedJSONError struct{ Scenario string }

func (e *MalformedJSONError) Error() string { return "fault: injected malformed JSON in " + e.Scenario }

type HTTP429Error struct {
	Scenario   string
	RetryAfter time.Duration
}

func (e *HTTP429Error) Error() string { return "fault: injected HTTP 429 in " + e.Scenario }
