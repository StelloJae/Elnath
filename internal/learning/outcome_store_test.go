package learning

import (
	"fmt"
	"os"
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

// --- FU-LearningObservability: OutcomeRecord schema extension ---

// TestOutcomeStoreAppend_PreservesExtendedFields pins the P3 learning-
// observability contract: MaxIterations, InputTokens, OutputTokens, ToolStats,
// and SessionID must round-trip through the JSONL store. These fields are the
// lens daemon self-analysis uses to correlate routing decisions with
// real-world cost, tool behavior, and session continuity.
func TestOutcomeStoreAppend_PreservesExtendedFields(t *testing.T) {
	dir := t.TempDir()
	store := NewOutcomeStore(dir + "/outcomes.jsonl")

	ts := time.Now().UTC()
	want := OutcomeRecord{
		ProjectID:     "proj-42",
		Intent:        "research",
		Workflow:      "single",
		FinishReason:  "stop",
		Success:       true,
		Duration:      3.25,
		Iterations:    4,
		MaxIterations: 50,
		InputTokens:   1500,
		OutputTokens:  320,
		ToolStats: []AgentToolStat{
			{Name: "read", Calls: 3, Errors: 0, TotalTime: 120 * time.Millisecond},
			{Name: "bash", Calls: 1, Errors: 1, TotalTime: 50 * time.Millisecond},
		},
		SessionID: "sess-abc123",
		Timestamp: ts,
	}

	if err := store.Append(want); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := store.Recent(1)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}

	if got[0].MaxIterations != 50 {
		t.Errorf("MaxIterations = %d, want 50", got[0].MaxIterations)
	}
	if got[0].InputTokens != 1500 {
		t.Errorf("InputTokens = %d, want 1500", got[0].InputTokens)
	}
	if got[0].OutputTokens != 320 {
		t.Errorf("OutputTokens = %d, want 320", got[0].OutputTokens)
	}
	if got[0].SessionID != "sess-abc123" {
		t.Errorf("SessionID = %q, want sess-abc123", got[0].SessionID)
	}
	if len(got[0].ToolStats) != 2 {
		t.Fatalf("ToolStats len = %d, want 2", len(got[0].ToolStats))
	}
	if got[0].ToolStats[0].Name != "read" || got[0].ToolStats[0].Calls != 3 {
		t.Errorf("ToolStats[0] = %+v, want {Name:read Calls:3}", got[0].ToolStats[0])
	}
	if got[0].ToolStats[1].Errors != 1 {
		t.Errorf("ToolStats[1].Errors = %d, want 1", got[0].ToolStats[1].Errors)
	}
}

// TestOutcomeRecord_BackwardCompatibleParse guards the read path against the
// historical schema. A raw legacy line sampled from outcomes.jsonl must still
// decode successfully, with the newly-added fields zero-valued.
func TestOutcomeRecord_BackwardCompatibleParse(t *testing.T) {
	legacyLine := `{"id":"44874a55","project_id":"23c6a04a","intent":"question","workflow":"single","finish_reason":"stop","success":true,"duration_s":0.91114025,"cost":0,"iterations":1,"input_snippet":"What is 2+2? Just the number.","estimated_files":1,"timestamp":"2026-04-16T20:46:51.969612Z"}`

	dir := t.TempDir()
	path := dir + "/outcomes.jsonl"
	if err := os.WriteFile(path, []byte(legacyLine+"\n"), 0o600); err != nil {
		t.Fatalf("write legacy line: %v", err)
	}

	store := NewOutcomeStore(path)
	got, err := store.Recent(1)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1 from legacy line", len(got))
	}

	if got[0].ID != "44874a55" {
		t.Errorf("legacy ID lost: %q", got[0].ID)
	}
	if got[0].Intent != "question" {
		t.Errorf("legacy Intent lost: %q", got[0].Intent)
	}
	if got[0].MaxIterations != 0 || got[0].InputTokens != 0 || got[0].OutputTokens != 0 {
		t.Errorf("new fields should default to zero on legacy record; got Max=%d In=%d Out=%d",
			got[0].MaxIterations, got[0].InputTokens, got[0].OutputTokens)
	}
	if got[0].ToolStats != nil {
		t.Errorf("ToolStats = %v, want nil for legacy record", got[0].ToolStats)
	}
	if got[0].SessionID != "" {
		t.Errorf("SessionID = %q, want empty for legacy record", got[0].SessionID)
	}
}
