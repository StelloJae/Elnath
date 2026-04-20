package reflection

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestReport() Report {
	return Report{
		Fingerprint:       "FPTEST123456",
		FinishReason:      "error",
		ErrorCategory:     "server_error",
		SuggestedStrategy: StrategyRetrySmallerScope,
		Reasoning:         "retry smaller subset",
		TaskSummary:       "fix failing test",
	}
}

func TestFileStore_Append_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "self_heal_attempts.jsonl")
	store := NewFileStore(path)

	meta := StoreMeta{
		TS:        time.Date(2026, 4, 20, 17, 54, 0, 0, time.UTC),
		TaskID:    "341",
		SessionID: "596b",
		Principal: "jay@workstation",
		ProjectID: "elnath",
	}
	if err := store.Append(context.Background(), newTestReport(), meta); err != nil {
		t.Fatalf("append: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	scanner := bufio.NewScanner(nil)
	scanner = bufio.NewScanner(os.NewFile(0, ""))
	_ = scanner
	var rec diskRecord
	if err := json.Unmarshal(trimNewline(data), &rec); err != nil {
		t.Fatalf("decode: %v (payload=%q)", err, string(data))
	}
	if rec.Fingerprint != "FPTEST123456" {
		t.Fatalf("fingerprint mismatch: %+v", rec)
	}
	if rec.SuggestedStrategy != string(StrategyRetrySmallerScope) {
		t.Fatalf("strategy mismatch: %+v", rec)
	}
	if rec.TaskID != "341" || rec.SessionID != "596b" {
		t.Fatalf("meta mismatch: %+v", rec)
	}
	if rec.PrincipalUserID != "jay@workstation" || rec.ProjectID != "elnath" {
		t.Fatalf("principal mismatch: %+v", rec)
	}
	if rec.TS == "" {
		t.Fatalf("ts missing")
	}

	payload := string(trimNewline(data))
	if !strings.Contains(payload, `"principal_user_id":"jay@workstation"`) {
		t.Fatalf("principal_user_id JSON key missing: %s", payload)
	}
	if !strings.Contains(payload, `"project_id":"elnath"`) {
		t.Fatalf("project_id JSON key missing: %s", payload)
	}
}

func TestFileStore_Append_PrincipalOmitEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "self_heal_attempts.jsonl")
	store := NewFileStore(path)

	// A meta with zero Principal/ProjectID must keep the on-disk JSON
	// backward compatible — the optional keys should be omitted so existing
	// readers and jq queries against older records stay stable.
	meta := StoreMeta{TS: time.Date(2026, 4, 20, 17, 54, 0, 0, time.UTC), TaskID: "t", SessionID: "s"}
	if err := store.Append(context.Background(), newTestReport(), meta); err != nil {
		t.Fatalf("append: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	payload := string(trimNewline(data))
	if strings.Contains(payload, "principal_user_id") {
		t.Fatalf("principal_user_id should be omitted when empty: %s", payload)
	}
	if strings.Contains(payload, "project_id") {
		t.Fatalf("project_id should be omitted when empty: %s", payload)
	}
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func TestFileStore_Append_DirAutoCreate(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "deep", "attempts.jsonl")
	store := NewFileStore(nested)

	if err := store.Append(context.Background(), newTestReport(), StoreMeta{}); err != nil {
		t.Fatalf("append with nested dir: %v", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("file not created at %s: %v", nested, err)
	}
}

func TestFileStore_Append_Concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "attempts.jsonl")
	store := NewFileStore(path)

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			rep := newTestReport()
			rep.Reasoning = "goroutine"
			meta := StoreMeta{TaskID: string(rune('A' + i))}
			if err := store.Append(context.Background(), rep, meta); err != nil {
				t.Errorf("concurrent append: %v", err)
			}
		}(i)
	}
	wg.Wait()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	lines := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec diskRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("line %d decode: %v", lines, err)
		}
		lines++
	}
	if lines != n {
		t.Fatalf("expected %d lines, got %d", n, lines)
	}
}

func TestFileStore_Append_PermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix permission semantics only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode bits")
	}
	dir := t.TempDir()
	readOnly := filepath.Join(dir, "ro")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })

	store := NewFileStore(filepath.Join(readOnly, "sub", "attempts.jsonl"))
	err := store.Append(context.Background(), newTestReport(), StoreMeta{})
	if err == nil {
		t.Fatal("expected permission error")
	}
}

func TestFileStore_Append_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(filepath.Join(dir, "attempts.jsonl"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := store.Append(ctx, newTestReport(), StoreMeta{})
	if err == nil {
		t.Fatal("expected context error")
	}
}

func TestFileStore_Read_Summary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "attempts.jsonl")
	store := NewFileStore(path)

	reps := []Report{
		{Fingerprint: "A", FinishReason: "error", ErrorCategory: "server_error", SuggestedStrategy: StrategyRetrySmallerScope},
		{Fingerprint: "B", FinishReason: "error", ErrorCategory: "timeout", SuggestedStrategy: StrategyUnknown},
		{Fingerprint: "C", FinishReason: "budget_exceeded", ErrorCategory: "context_overflow", SuggestedStrategy: StrategyCompressContext},
	}
	for i, r := range reps {
		meta := StoreMeta{TS: time.Date(2026, 4, 20, 10+i, 0, 0, 0, time.UTC)}
		if err := store.Append(context.Background(), r, meta); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	sum, err := store.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sum.Total != 3 {
		t.Fatalf("total: %d", sum.Total)
	}
	if sum.FinishReason["error"] != 2 || sum.FinishReason["budget_exceeded"] != 1 {
		t.Fatalf("finish reason counts: %+v", sum.FinishReason)
	}
	if sum.StrategyCounts[string(StrategyUnknown)] != 1 {
		t.Fatalf("strategy counts: %+v", sum.StrategyCounts)
	}
	if sum.SchemaFailures != 1 {
		t.Fatalf("schema fail count: %d", sum.SchemaFailures)
	}
	if sum.SchemaFailureRate < 0.33 || sum.SchemaFailureRate > 0.34 {
		t.Fatalf("schema fail rate: %f", sum.SchemaFailureRate)
	}
	if sum.FirstTS.Hour() != 10 || sum.LastTS.Hour() != 12 {
		t.Fatalf("ts window off: %v → %v", sum.FirstTS, sum.LastTS)
	}
}

func TestFileStore_Read_Missing(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "does_not_exist.jsonl"))
	sum, err := store.Read()
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if sum.Total != 0 {
		t.Fatalf("expected zero total, got %d", sum.Total)
	}
}
