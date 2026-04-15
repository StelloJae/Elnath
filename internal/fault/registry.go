package fault

import (
	"fmt"
	"time"

	"github.com/stello/elnath/internal/fault/faulttype"
)

type ScenarioRegistry struct {
	scenarios map[string]*faulttype.Scenario
	ordered   []*faulttype.Scenario
}

func NewRegistry(scenarios []*faulttype.Scenario) *ScenarioRegistry {
	r := &ScenarioRegistry{scenarios: make(map[string]*faulttype.Scenario)}
	for _, scenario := range scenarios {
		r.Register(scenario)
	}
	return r
}

func (r *ScenarioRegistry) Register(s *faulttype.Scenario) {
	if s == nil {
		panic("fault: cannot register nil scenario")
	}
	if _, exists := r.scenarios[s.Name]; exists {
		panic(fmt.Sprintf("fault: duplicate scenario name %q", s.Name))
	}
	if (s.FaultType == faulttype.FaultSlowConn || s.FaultType == faulttype.FaultBackpressure) && s.FaultDuration > 5*time.Second {
		panic(fmt.Sprintf("fault: scenario %s FaultDuration exceeds 5s cap", s.Name))
	}
	r.scenarios[s.Name] = s
	r.ordered = append(r.ordered, s)
}

func (r *ScenarioRegistry) Get(name string) (*faulttype.Scenario, bool) {
	scenario, ok := r.scenarios[name]
	if !ok {
		return nil, false
	}
	return scenario, true
}

func (r *ScenarioRegistry) All() []*faulttype.Scenario {
	out := make([]*faulttype.Scenario, len(r.ordered))
	copy(out, r.ordered)
	return out
}
