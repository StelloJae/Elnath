package learning

import (
	"testing"
	"time"
)

func appendN(t *testing.T, store *OutcomeStore, projectID, intent, workflow string, total, successCount int) {
	t.Helper()
	base := time.Now().UTC()
	for i := 0; i < total; i++ {
		success := i < successCount
		finishReason := "budget_exceeded"
		if success {
			finishReason = "stop"
		}
		r := OutcomeRecord{
			ProjectID:    projectID,
			Intent:       intent,
			Workflow:     workflow,
			FinishReason: finishReason,
			Success:      success,
			Timestamp:    base.Add(time.Duration(i) * time.Millisecond),
		}
		if err := store.Append(r); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
}

func TestAdviseNoData(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")
	advisor := NewRoutingAdvisor(store)

	pref, err := advisor.Advise("proj")
	if err != nil {
		t.Fatalf("advise: %v", err)
	}
	if pref != nil {
		t.Errorf("want nil, got %+v", pref)
	}
}

func TestAdviseBelowMinSamples(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")
	advisor := NewRoutingAdvisor(store)

	// 3 records — below minSamples=5
	appendN(t, store, "proj", "complex", "team", 3, 1)

	pref, err := advisor.Advise("proj")
	if err != nil {
		t.Fatalf("advise: %v", err)
	}
	if pref != nil {
		t.Errorf("want nil (below min samples), got %+v", pref)
	}
}

func TestAdvisePreferHighSuccessRate(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")
	advisor := NewRoutingAdvisor(store)

	// team: 5/10 = 50%, ralph: 9/10 = 90% → prefer ralph (diff >= 20%)
	appendN(t, store, "proj", "complex", "team", 10, 5)
	appendN(t, store, "proj", "complex", "ralph", 10, 9)

	pref, err := advisor.Advise("proj")
	if err != nil {
		t.Fatalf("advise: %v", err)
	}
	if pref == nil {
		t.Fatal("want non-nil pref")
	}
	if pref.PreferredWorkflows["complex"] != "ralph" {
		t.Errorf("want ralph preferred for complex, got %q", pref.PreferredWorkflows["complex"])
	}
}

func TestAdviseAvoidLowSuccessRate(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")
	advisor := NewRoutingAdvisor(store)

	// team: 1/10 = 10% → avoided
	appendN(t, store, "proj", "complex", "team", 10, 1)
	// ralph: 8/10 = 80% → not avoided
	appendN(t, store, "proj", "complex", "ralph", 10, 8)

	pref, err := advisor.Advise("proj")
	if err != nil {
		t.Fatalf("advise: %v", err)
	}
	if pref == nil {
		t.Fatal("want non-nil pref")
	}

	avoided := false
	for _, w := range pref.AvoidWorkflows {
		if w == "team" {
			avoided = true
		}
	}
	if !avoided {
		t.Errorf("want team in AvoidWorkflows, got %v", pref.AvoidWorkflows)
	}
}

func TestAdviseSimilarRates(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")
	advisor := NewRoutingAdvisor(store)

	// team: 7/10 = 70%, ralph: 7/10 = 70% → no preference (diff < 20%)
	appendN(t, store, "proj", "complex", "team", 10, 7)
	appendN(t, store, "proj", "complex", "ralph", 10, 7)

	pref, err := advisor.Advise("proj")
	if err != nil {
		t.Fatalf("advise: %v", err)
	}
	// Both at 70% — neither avoided (>=30%), no preference (diff=0%)
	if pref != nil && len(pref.PreferredWorkflows) > 0 {
		t.Errorf("want no preferred workflow for similar rates, got %v", pref.PreferredWorkflows)
	}
}

func TestAdviseMultipleIntents(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")
	advisor := NewRoutingAdvisor(store)

	// complex: ralph wins
	appendN(t, store, "proj", "complex", "ralph", 10, 9)
	appendN(t, store, "proj", "complex", "team", 10, 4)

	// simple: single wins
	appendN(t, store, "proj", "simple", "single", 10, 10)
	appendN(t, store, "proj", "simple", "team", 10, 3)

	pref, err := advisor.Advise("proj")
	if err != nil {
		t.Fatalf("advise: %v", err)
	}
	if pref == nil {
		t.Fatal("want non-nil pref")
	}
	if pref.PreferredWorkflows["complex"] != "ralph" {
		t.Errorf("want ralph for complex, got %q", pref.PreferredWorkflows["complex"])
	}
	if pref.PreferredWorkflows["simple"] != "single" {
		t.Errorf("want single for simple, got %q", pref.PreferredWorkflows["simple"])
	}
}

func TestRoutingAdvisorIgnoresPreferenceUsedOutcomes(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")
	advisor := NewRoutingAdvisor(store)

	base := time.Now().UTC()
	for i := 0; i < 10; i++ {
		rec := OutcomeRecord{
			ProjectID:      "proj",
			Intent:         "complex",
			Workflow:       "team",
			FinishReason:   "stop",
			Success:        true,
			PreferenceUsed: true,
			Timestamp:      base.Add(time.Duration(i) * time.Millisecond),
		}
		if err := store.Append(rec); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	pref, err := advisor.Advise("proj")
	if err != nil {
		t.Fatalf("advise: %v", err)
	}
	if pref != nil {
		t.Fatalf("want nil pref (all outcomes pinned), got %+v", pref)
	}
}

func TestRoutingAdvisorUsesNonPreferenceOutcomesOnly(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")
	advisor := NewRoutingAdvisor(store)

	base := time.Now().UTC()
	// 10 pinned "team" outcomes — should be ignored entirely.
	for i := 0; i < 10; i++ {
		rec := OutcomeRecord{
			ProjectID:      "proj",
			Intent:         "complex",
			Workflow:       "team",
			FinishReason:   "stop",
			Success:        true,
			PreferenceUsed: true,
			Timestamp:      base.Add(time.Duration(i) * time.Millisecond),
		}
		if err := store.Append(rec); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// Natural signal: ralph wins 9/10, team_natural 3/10.
	appendN(t, store, "proj", "complex", "ralph", 10, 9)
	appendN(t, store, "proj", "complex", "team_natural", 10, 3)

	pref, err := advisor.Advise("proj")
	if err != nil {
		t.Fatalf("advise: %v", err)
	}
	if pref == nil {
		t.Fatal("want non-nil pref from natural samples")
	}
	if pref.PreferredWorkflows["complex"] != "ralph" {
		t.Errorf("want ralph (natural winner), got %q", pref.PreferredWorkflows["complex"])
	}
}
