package wiki

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	routingpref "github.com/stello/elnath/internal/routing"
)

func TestLoadWorkflowPreferenceMissingPage(t *testing.T) {
	store := newTestStore(t)

	pref, err := LoadWorkflowPreference(store, "elnath")
	if err != nil {
		t.Fatalf("LoadWorkflowPreference: %v", err)
	}
	if pref != nil {
		t.Fatalf("preference = %#v, want nil", pref)
	}
}

func TestLoadWorkflowPreferenceParsesFrontmatter(t *testing.T) {
	store := newTestStore(t)
	relPath := filepath.Join("projects", "elnath", "routing-preferences.md")
	absPath := filepath.Join(store.WikiDir(), relPath)

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	raw := `---
title: Project Routing Preferences
type: concept
preferred_workflows:
  question: research
  complex_task: ralph
avoid_workflows:
  - team
---

Prefer research for questions.
`
	if err := os.WriteFile(absPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pref, err := LoadWorkflowPreference(store, "elnath")
	if err != nil {
		t.Fatalf("LoadWorkflowPreference: %v", err)
	}

	want := &routingpref.WorkflowPreference{
		PreferredWorkflows: map[string]string{
			"question":     "research",
			"complex_task": "ralph",
		},
		AvoidWorkflows: []string{"team"},
	}
	if !reflect.DeepEqual(pref, want) {
		t.Fatalf("preference = %#v, want %#v", pref, want)
	}
}
