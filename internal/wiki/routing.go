package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	routingpref "github.com/stello/elnath/internal/routing"
	"gopkg.in/yaml.v3"
)

type workflowPreferenceYAML struct {
	PreferredWorkflows map[string]string `yaml:"preferred_workflows,omitempty"`
	AvoidWorkflows     []string          `yaml:"avoid_workflows,omitempty"`
}

// LoadWorkflowPreference loads per-project routing preferences from the wiki.
// Missing preference pages are treated as an opt-out and return (nil, nil).
func LoadWorkflowPreference(store *Store, projectID string) (*routingpref.WorkflowPreference, error) {
	if store == nil {
		return nil, nil
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, nil
	}

	relPath := filepath.ToSlash(filepath.Join("projects", projectID, "routing-preferences.md"))
	absPath, err := store.absPath(relPath)
	if err != nil {
		return nil, fmt.Errorf("wiki routing preference: resolve %q: %w", relPath, err)
	}
	if _, err := os.Stat(absPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("wiki routing preference: stat %q: %w", relPath, err)
	}

	page, err := store.Read(relPath)
	if err != nil {
		return nil, fmt.Errorf("wiki routing preference: read %q: %w", relPath, err)
	}

	var parsed workflowPreferenceYAML
	if len(page.Extra) > 0 {
		data, err := yaml.Marshal(page.Extra)
		if err != nil {
			return nil, fmt.Errorf("wiki routing preference: marshal extras: %w", err)
		}
		if err := yaml.Unmarshal(data, &parsed); err != nil {
			return nil, fmt.Errorf("wiki routing preference: parse extras: %w", err)
		}
	}

	if len(parsed.PreferredWorkflows) == 0 && len(parsed.AvoidWorkflows) == 0 {
		return nil, nil
	}

	return &routingpref.WorkflowPreference{
		PreferredWorkflows: parsed.PreferredWorkflows,
		AvoidWorkflows:     parsed.AvoidWorkflows,
	}, nil
}
