package scorecard

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendJSONCreatesDirectoryAndAppends(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "scorecard", "2026-04-17.jsonl")

	r1 := Report{Timestamp: time.Now(), SchemaVersion: "1.0", Overall: ScoreNascent}
	if err := AppendJSON(r1, filePath); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	r2 := Report{Timestamp: time.Now(), SchemaVersion: "1.0", Overall: ScoreOK}
	if err := AppendJSON(r2, filePath); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	count := 0
	for s.Scan() {
		count++
		var parsed Report
		if err := json.Unmarshal(s.Bytes(), &parsed); err != nil {
			t.Errorf("line %d invalid JSON: %v", count, err)
		}
	}
	if count != 2 {
		t.Errorf("lines: got %d, want 2", count)
	}

	raw, _ := os.ReadFile(filePath)
	if strings.Count(string(raw), "\n") != 2 {
		t.Errorf("expected 2 trailing newlines, got %d", strings.Count(string(raw), "\n"))
	}
}
