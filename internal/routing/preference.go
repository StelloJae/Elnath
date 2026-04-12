package routing

import "strings"

// WorkflowPreference captures per-project routing overrides loaded from wiki metadata.
type WorkflowPreference struct {
	PreferredWorkflows map[string]string `yaml:"preferred_workflows,omitempty"`
	AvoidWorkflows     []string          `yaml:"avoid_workflows,omitempty"`
}

func (p *WorkflowPreference) PreferredWorkflow(intent string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.PreferredWorkflows[strings.TrimSpace(intent)])
}

func (p *WorkflowPreference) Avoids(workflow string) bool {
	if p == nil {
		return false
	}
	workflow = strings.TrimSpace(workflow)
	for _, avoided := range p.AvoidWorkflows {
		if strings.TrimSpace(avoided) == workflow {
			return true
		}
	}
	return false
}
