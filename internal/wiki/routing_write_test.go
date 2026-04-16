package wiki

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stello/elnath/internal/routing"
)

func TestSaveWorkflowPreferenceNilInputs(t *testing.T) {
	store := newTestStore(t)

	pref := &routing.WorkflowPreference{
		PreferredWorkflows: map[string]string{"complex_task": "ralph"},
	}

	// nil store
	if err := SaveWorkflowPreference(nil, "elnath", pref); err != nil {
		t.Fatalf("nil store: unexpected error: %v", err)
	}

	// empty projectID
	if err := SaveWorkflowPreference(store, "", pref); err != nil {
		t.Fatalf("empty projectID: unexpected error: %v", err)
	}

	// whitespace-only projectID
	if err := SaveWorkflowPreference(store, "   ", pref); err != nil {
		t.Fatalf("whitespace projectID: unexpected error: %v", err)
	}

	// nil pref
	if err := SaveWorkflowPreference(store, "elnath", nil); err != nil {
		t.Fatalf("nil pref: unexpected error: %v", err)
	}
}

func TestSaveWorkflowPreferenceCreatesNew(t *testing.T) {
	store := newTestStore(t)

	pref := &routing.WorkflowPreference{
		PreferredWorkflows: map[string]string{
			"complex_task": "ralph",
			"question":     "research",
		},
		AvoidWorkflows: []string{"team"},
	}

	if err := SaveWorkflowPreference(store, "elnath", pref); err != nil {
		t.Fatalf("SaveWorkflowPreference: %v", err)
	}

	relPath := filepath.ToSlash(filepath.Join("projects", "elnath", "routing-preferences.md"))
	page, err := store.Read(relPath)
	if err != nil {
		t.Fatalf("Read after save: %v", err)
	}

	if page.Title == "" {
		t.Error("page title should not be empty")
	}

	source, _ := page.Extra["source"].(string)
	if source != "self-improvement" {
		t.Errorf("Extra[source] = %q, want %q", source, "self-improvement")
	}

	if _, ok := page.Extra["updated_at"]; !ok {
		t.Error("Extra[updated_at] should be set")
	}

	// Preferred workflows should be stored in Extra.
	preferred, _ := page.Extra["preferred_workflows"].(map[string]any)
	if preferred == nil {
		t.Fatal("Extra[preferred_workflows] should be set")
	}
	if preferred["complex_task"] != "ralph" {
		t.Errorf("preferred_workflows[complex_task] = %v, want %q", preferred["complex_task"], "ralph")
	}

	// Avoid workflows should be stored in Extra.
	avoided, _ := page.Extra["avoid_workflows"].([]any)
	if len(avoided) == 0 {
		t.Fatal("Extra[avoid_workflows] should be non-empty")
	}
	if avoided[0] != "team" {
		t.Errorf("avoid_workflows[0] = %v, want %q", avoided[0], "team")
	}

	// Body should mention the project.
	if page.Content == "" {
		t.Error("page Content should not be empty")
	}
}

func TestSaveWorkflowPreferenceUpdatesOwned(t *testing.T) {
	store := newTestStore(t)
	projectID := "elnath"
	relPath := filepath.ToSlash(filepath.Join("projects", projectID, "routing-preferences.md"))

	// Write an initial self-improvement page.
	firstPref := &routing.WorkflowPreference{
		PreferredWorkflows: map[string]string{"complex_task": "ralph"},
	}
	if err := SaveWorkflowPreference(store, projectID, firstPref); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Update with a different preference.
	secondPref := &routing.WorkflowPreference{
		PreferredWorkflows: map[string]string{"complex_task": "team"},
		AvoidWorkflows:     []string{"ralph"},
	}
	if err := SaveWorkflowPreference(store, projectID, secondPref); err != nil {
		t.Fatalf("second save: %v", err)
	}

	page, err := store.Read(relPath)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	preferred, _ := page.Extra["preferred_workflows"].(map[string]any)
	if preferred == nil {
		t.Fatal("Extra[preferred_workflows] should be set after update")
	}
	if preferred["complex_task"] != "team" {
		t.Errorf("preferred_workflows[complex_task] = %v, want %q", preferred["complex_task"], "team")
	}
}

func TestSaveWorkflowPreferenceProtectsManual(t *testing.T) {
	store := newTestStore(t)
	projectID := "myproject"

	// Write a manual page (no source field).
	relPath := filepath.Join("projects", projectID, "routing-preferences.md")
	absPath := filepath.Join(store.WikiDir(), relPath)

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	manual := `---
title: Manual Routing Preferences
type: concept
preferred_workflows:
  complex_task: ultrawork
---

Manually written.
`
	if err := os.WriteFile(absPath, []byte(manual), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Attempt to overwrite via self-improvement.
	pref := &routing.WorkflowPreference{
		PreferredWorkflows: map[string]string{"complex_task": "ralph"},
	}
	if err := SaveWorkflowPreference(store, projectID, pref); err != nil {
		t.Fatalf("SaveWorkflowPreference: %v", err)
	}

	// Page should be unchanged.
	got, err := store.Read(filepath.ToSlash(relPath))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Title != "Manual Routing Preferences" {
		t.Errorf("title changed to %q, want %q", got.Title, "Manual Routing Preferences")
	}
	// Extra should still not contain source=self-improvement.
	source, _ := got.Extra["source"].(string)
	if source == "self-improvement" {
		t.Error("manual page should not have source=self-improvement after attempted overwrite")
	}
}

func TestSaveWorkflowPreferenceProtectsManualWithOtherSource(t *testing.T) {
	store := newTestStore(t)
	projectID := "myproject2"

	relPath := filepath.Join("projects", projectID, "routing-preferences.md")
	absPath := filepath.Join(store.WikiDir(), relPath)

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Page with explicit source: user.
	manual := `---
title: User Routing Preferences
type: concept
source: user
preferred_workflows:
  question: research
---

Written by user.
`
	if err := os.WriteFile(absPath, []byte(manual), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pref := &routing.WorkflowPreference{
		PreferredWorkflows: map[string]string{"question": "ralph"},
	}
	if err := SaveWorkflowPreference(store, projectID, pref); err != nil {
		t.Fatalf("SaveWorkflowPreference: %v", err)
	}

	got, err := store.Read(filepath.ToSlash(relPath))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Source should still be "user", not "self-improvement".
	source, _ := got.Extra["source"].(string)
	if source != "user" {
		t.Errorf("source = %q, want %q", source, "user")
	}
}
