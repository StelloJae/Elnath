package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/stello/elnath/internal/routing"
)

// SaveWorkflowPreference writes a self-improvement-generated routing preference
// page to the wiki for the given project. It will not overwrite pages that were
// created manually (i.e. pages whose Extra["source"] is not "self-improvement").
//
// All nil/empty guard inputs are treated as a no-op and return nil.
func SaveWorkflowPreference(store *Store, projectID string, pref *routing.WorkflowPreference) error {
	if store == nil || strings.TrimSpace(projectID) == "" || pref == nil {
		return nil
	}

	relPath := filepath.ToSlash(filepath.Join("projects", projectID, "routing-preferences.md"))

	absPath, err := store.absPath(relPath)
	if err != nil {
		return fmt.Errorf("wiki routing preference: resolve %q: %w", relPath, err)
	}

	if _, statErr := os.Stat(absPath); statErr == nil {
		// Page exists — check whether it was written by self-improvement.
		existing, readErr := store.Read(relPath)
		if readErr != nil {
			return fmt.Errorf("wiki routing preference: read existing %q: %w", relPath, readErr)
		}
		if !existing.IsOwnedBy(SourceSelfImprovement) {
			// Manual page — do not overwrite.
			return nil
		}
	}

	page := buildPreferencePage(relPath, projectID, pref)
	if err := store.Upsert(page); err != nil {
		return fmt.Errorf("wiki routing preference: upsert %q: %w", relPath, err)
	}
	return nil
}

// SaveUserWorkflowPreference writes a user-controlled routing preference to the
// wiki for the given project. Unlike SaveWorkflowPreference, it always writes
// (source: "user") and merges new intent mappings into any existing page rather
// than replacing it wholesale. Pages with source "user" are never overwritten by
// the automatic routing advisor.
func SaveUserWorkflowPreference(store *Store, projectID string, pref *routing.WorkflowPreference) error {
	if store == nil || strings.TrimSpace(projectID) == "" || pref == nil {
		return nil
	}

	relPath := filepath.ToSlash(filepath.Join("projects", projectID, "routing-preferences.md"))

	// Merge with existing preference if the page already exists.
	merged := &routing.WorkflowPreference{
		PreferredWorkflows: make(map[string]string),
	}
	if absPath, absErr := store.absPath(relPath); absErr == nil {
		if _, statErr := os.Stat(absPath); statErr == nil {
			if existing, readErr := store.Read(relPath); readErr == nil && existing != nil {
				if wf, ok := existing.Extra["preferred_workflows"]; ok {
					if wfMap, ok := wf.(map[string]interface{}); ok {
						for k, v := range wfMap {
							if vs, ok := v.(string); ok {
								merged.PreferredWorkflows[k] = vs
							}
						}
					}
				}
				if av, ok := existing.Extra["avoid_workflows"]; ok {
					if avSlice, ok := av.([]interface{}); ok {
						for _, v := range avSlice {
							if vs, ok := v.(string); ok {
								merged.AvoidWorkflows = append(merged.AvoidWorkflows, vs)
							}
						}
					}
				}
			}
		}
	}

	// Apply new mappings on top of existing ones.
	for intent, workflow := range pref.PreferredWorkflows {
		merged.PreferredWorkflows[intent] = workflow
	}
	merged.AvoidWorkflows = append(merged.AvoidWorkflows, pref.AvoidWorkflows...)

	page := &Page{
		Path:    relPath,
		Title:   fmt.Sprintf("Routing Preferences: %s", projectID),
		Type:    PageTypeConcept,
		Content: buildUserPreferenceBody(projectID, merged),
		Extra: map[string]any{
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	}
	page.SetSource(SourceUser, "", "")
	if len(merged.PreferredWorkflows) > 0 {
		page.Extra["preferred_workflows"] = merged.PreferredWorkflows
	}
	if len(merged.AvoidWorkflows) > 0 {
		page.Extra["avoid_workflows"] = merged.AvoidWorkflows
	}
	if err := store.Upsert(page); err != nil {
		return fmt.Errorf("wiki user routing preference: upsert %q: %w", relPath, err)
	}
	return nil
}

// buildUserPreferenceBody renders a human-readable markdown summary for a user-controlled preference.
func buildUserPreferenceBody(projectID string, pref *routing.WorkflowPreference) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("User-controlled routing preferences for project `%s`.\n\n", projectID))
	if len(pref.PreferredWorkflows) > 0 {
		sb.WriteString("## Preferred Workflows\n\n")
		intents := make([]string, 0, len(pref.PreferredWorkflows))
		for intent := range pref.PreferredWorkflows {
			intents = append(intents, intent)
		}
		sort.Strings(intents)
		for _, intent := range intents {
			sb.WriteString(fmt.Sprintf("- `%s` → `%s`\n", intent, pref.PreferredWorkflows[intent]))
		}
		sb.WriteByte('\n')
	}
	if len(pref.AvoidWorkflows) > 0 {
		sb.WriteString("## Avoid Workflows\n\n")
		for _, w := range pref.AvoidWorkflows {
			sb.WriteString(fmt.Sprintf("- `%s`\n", w))
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("> This page is managed by the user via /override. The routing advisor will not overwrite it.\n")
	return sb.String()
}

// buildPreferencePage constructs the wiki Page for a self-improvement preference.
func buildPreferencePage(relPath, projectID string, pref *routing.WorkflowPreference) *Page {
	page := &Page{
		Path:    relPath,
		Title:   fmt.Sprintf("Routing Preferences: %s", projectID),
		Type:    PageTypeConcept,
		Content: buildPreferenceBody(projectID, pref),
		Extra: map[string]any{
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		},
	}
	page.SetSource(SourceSelfImprovement, "", "")
	if len(pref.PreferredWorkflows) > 0 {
		page.Extra["preferred_workflows"] = pref.PreferredWorkflows
	}
	if len(pref.AvoidWorkflows) > 0 {
		page.Extra["avoid_workflows"] = pref.AvoidWorkflows
	}
	return page
}

// buildPreferenceBody renders a human-readable markdown summary of the preference.
func buildPreferenceBody(projectID string, pref *routing.WorkflowPreference) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Auto-generated routing preferences for project `%s`.\n\n", projectID))

	if len(pref.PreferredWorkflows) > 0 {
		sb.WriteString("## Preferred Workflows\n\n")

		// Sort keys for deterministic output.
		intents := make([]string, 0, len(pref.PreferredWorkflows))
		for intent := range pref.PreferredWorkflows {
			intents = append(intents, intent)
		}
		sort.Strings(intents)

		for _, intent := range intents {
			sb.WriteString(fmt.Sprintf("- `%s` → `%s`\n", intent, pref.PreferredWorkflows[intent]))
		}
		sb.WriteByte('\n')
	}

	if len(pref.AvoidWorkflows) > 0 {
		sb.WriteString("## Avoid Workflows\n\n")
		for _, w := range pref.AvoidWorkflows {
			sb.WriteString(fmt.Sprintf("- `%s`\n", w))
		}
		sb.WriteByte('\n')
	}

	sb.WriteString("> This page is managed by Elnath self-improvement. Manual edits will be preserved if you remove `source: self-improvement` from the frontmatter.\n")

	return sb.String()
}
