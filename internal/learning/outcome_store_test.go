package learning

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func makeRecord(projectID, intent, workflow, finishReason string, success bool, ts time.Time) OutcomeRecord {
	return OutcomeRecord{
		ProjectID:    projectID,
		Intent:       intent,
		Workflow:     workflow,
		FinishReason: finishReason,
		Success:      success,
		Timestamp:    ts,
	}
}

func TestOutcomeStoreAppendAndRecent(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")

	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		r := makeRecord("proj", "intent", "workflow", "stop", true, base.Add(time.Duration(i)*time.Second))
		if err := store.Append(r); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got, err := store.Recent(3)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	// Newest first: index 4, 3, 2
	if !got[0].Timestamp.After(got[1].Timestamp) {
		t.Error("not sorted newest first")
	}
}

func TestOutcomeStoreForProject(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")

	base := time.Now().UTC()
	for i := 0; i < 4; i++ {
		_ = store.Append(makeRecord("alpha", "i", "w", "stop", true, base.Add(time.Duration(i)*time.Second)))
	}
	for i := 0; i < 3; i++ {
		_ = store.Append(makeRecord("beta", "i", "w", "stop", true, base.Add(time.Duration(i)*time.Second)))
	}

	alpha, err := store.ForProject("alpha", 10)
	if err != nil {
		t.Fatalf("ForProject alpha: %v", err)
	}
	if len(alpha) != 4 {
		t.Fatalf("want 4 for alpha, got %d", len(alpha))
	}

	beta, err := store.ForProject("beta", 10)
	if err != nil {
		t.Fatalf("ForProject beta: %v", err)
	}
	if len(beta) != 3 {
		t.Fatalf("want 3 for beta, got %d", len(beta))
	}

	for _, r := range alpha {
		if r.ProjectID != "alpha" {
			t.Errorf("unexpected project %q in alpha results", r.ProjectID)
		}
	}
}

func TestOutcomeStoreRotate(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")

	base := time.Now().UTC()
	for i := 0; i < 10; i++ {
		_ = store.Append(makeRecord("proj", "i", "w", "stop", true, base.Add(time.Duration(i)*time.Second)))
	}

	if err := store.Rotate(5); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	all, err := store.Recent(100)
	if err != nil {
		t.Fatalf("recent after rotate: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("want 5 after rotate, got %d", len(all))
	}
}

func TestOutcomeStoreAutoRotateIfNeeded(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")

	base := time.Now().UTC()
	// 12 records with keepLast=5 → 12 > 5*2=10, should trigger
	for i := 0; i < 12; i++ {
		_ = store.Append(makeRecord("proj", "i", "w", "stop", true, base.Add(time.Duration(i)*time.Second)))
	}

	if err := store.AutoRotateIfNeeded(5); err != nil {
		t.Fatalf("auto rotate: %v", err)
	}

	all, err := store.Recent(100)
	if err != nil {
		t.Fatalf("recent after auto rotate: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("want 5 after auto rotate, got %d", len(all))
	}
}

func TestOutcomeStoreDefaults(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")

	r := OutcomeRecord{
		ProjectID: "proj",
		Intent:    "intent",
		Workflow:  "wf",
	}
	if err := store.Append(r); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := store.Recent(1)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 1 {
		t.Fatal("expected 1 record")
	}
	if got[0].ID == "" {
		t.Error("ID should be auto-set")
	}
	if got[0].Timestamp.IsZero() {
		t.Error("Timestamp should be auto-set")
	}
}

func TestOutcomeStoreConcurrency(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")

	var wg sync.WaitGroup
	base := time.Now().UTC()
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				ts := base.Add(time.Duration(g*100+i) * time.Millisecond)
				r := makeRecord(fmt.Sprintf("proj%d", g), "intent", "workflow", "stop", true, ts)
				if err := store.Append(r); err != nil {
					t.Errorf("goroutine %d append %d: %v", g, i, err)
				}
			}
		}(g)
	}
	wg.Wait()

	all, err := store.Recent(100)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(all) != 50 {
		t.Fatalf("want 50, got %d", len(all))
	}
}

func TestIsSuccessful(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		{"stop", true},
		{"budget_exceeded", false},
		{"error", false},
		{"", false},
		{"ack_loop", false},
	}
	for _, c := range cases {
		if got := IsSuccessful(c.reason); got != c.want {
			t.Errorf("IsSuccessful(%q) = %v, want %v", c.reason, got, c.want)
		}
	}
}

func TestShouldRecord(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		{"stop", true},
		{"budget_exceeded", true},
		{"error", true},
		{"", false},
	}
	for _, c := range cases {
		if got := ShouldRecord(c.reason); got != c.want {
			t.Errorf("ShouldRecord(%q) = %v, want %v", c.reason, got, c.want)
		}
	}
}
