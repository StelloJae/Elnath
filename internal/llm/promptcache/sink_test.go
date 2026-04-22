package promptcache

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNoopSink_RecordReturnsNil(t *testing.T) {
	if err := (NoopSink{}).Record(context.Background(), "sess", Event{}); err != nil {
		t.Errorf("NoopSink.Record = %v, want nil", err)
	}
}

func TestFileSink_EmptySessionIDSkips(t *testing.T) {
	dir := t.TempDir()
	sink := NewFileSink(dir)
	if err := sink.Record(context.Background(), "", Event{Model: "x"}); err != nil {
		t.Errorf("Record with empty session: err = %v, want nil", err)
	}
	// No file should have been created.
	entries, _ := os.ReadDir(filepath.Join(dir, "prompt-cache"))
	if len(entries) != 0 {
		t.Errorf("prompt-cache dir contains %d entries, want 0", len(entries))
	}
}

func TestFileSink_AutoIncrementsTurnsPerSession(t *testing.T) {
	dir := t.TempDir()
	sink := NewFileSink(dir)
	ctx := context.Background()

	// Session A: two writes, expect turns 1 and 2.
	if err := sink.Record(ctx, "sess-a", Event{Model: "m"}); err != nil {
		t.Fatalf("write1: %v", err)
	}
	if err := sink.Record(ctx, "sess-a", Event{Model: "m"}); err != nil {
		t.Fatalf("write2: %v", err)
	}
	// Session B: two writes, should have its own 1/2 counter.
	if err := sink.Record(ctx, "sess-b", Event{Model: "m"}); err != nil {
		t.Fatalf("writeB1: %v", err)
	}

	turns := readTurns(t, filepath.Join(dir, "prompt-cache", "sess-a.jsonl"))
	if len(turns) != 2 || turns[0] != 1 || turns[1] != 2 {
		t.Errorf("session A turns = %v, want [1 2]", turns)
	}
	turnsB := readTurns(t, filepath.Join(dir, "prompt-cache", "sess-b.jsonl"))
	if len(turnsB) != 1 || turnsB[0] != 1 {
		t.Errorf("session B turns = %v, want [1]", turnsB)
	}
}

func TestFileSink_PreservesCallerSuppliedTurn(t *testing.T) {
	dir := t.TempDir()
	sink := NewFileSink(dir)
	if err := sink.Record(context.Background(), "sess", Event{Turn: 7, Model: "m"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	turns := readTurns(t, filepath.Join(dir, "prompt-cache", "sess.jsonl"))
	if len(turns) != 1 || turns[0] != 7 {
		t.Errorf("turns = %v, want [7]", turns)
	}
	// Subsequent auto-assigned turn should advance past the caller-supplied value.
	if err := sink.Record(context.Background(), "sess", Event{Model: "m"}); err != nil {
		t.Fatalf("Record2: %v", err)
	}
	turns = readTurns(t, filepath.Join(dir, "prompt-cache", "sess.jsonl"))
	if len(turns) != 2 || turns[1] != 8 {
		t.Errorf("turns = %v, want second entry == 8", turns)
	}
}

func TestFileSink_ReportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sink := NewFileSink(dir)
	report := &BreakReport{
		Happened:       true,
		CreationTokens: 3400,
		ReadTokens:     0,
		Reasons: []BreakDetail{
			{Reason: ReasonSystemPrompt, Detail: "len 30→44"},
		},
	}
	ev := Event{
		Timestamp: time.Date(2026, 4, 22, 14, 0, 0, 0, time.UTC),
		Model:     "claude-opus-4-7[1m]",
		Report:    report,
	}
	if err := sink.Record(context.Background(), "sess", ev); err != nil {
		t.Fatalf("Record: %v", err)
	}

	f, err := os.Open(filepath.Join(dir, "prompt-cache", "sess.jsonl"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Scan()
	var got Event
	if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "claude-opus-4-7[1m]" {
		t.Errorf("model round-trip = %q", got.Model)
	}
	if got.Report == nil || !got.Report.Happened {
		t.Fatalf("report round-trip broke: %+v", got.Report)
	}
	if got.Report.Reasons[0].Reason != ReasonSystemPrompt {
		t.Errorf("reason round-trip = %q", got.Report.Reasons[0].Reason)
	}
}

// readTurns reads a jsonl file and returns the Turn field of each
// event in write order. Used to verify auto-increment behavior.
func readTurns(t *testing.T, path string) []int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var turns []int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		turns = append(turns, ev.Turn)
	}
	return turns
}
