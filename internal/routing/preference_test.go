package routing

import "testing"

func TestWorkflowPreferenceAvoids(t *testing.T) {
	pref := &WorkflowPreference{AvoidWorkflows: []string{"team", "ralph"}}

	if !pref.Avoids("team") {
		t.Fatal("expected team to be avoided")
	}
	if pref.Avoids("single") {
		t.Fatal("did not expect single to be avoided")
	}
}
