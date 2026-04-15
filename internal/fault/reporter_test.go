package fault

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type syncFailWriter struct{ bytes.Buffer }

func (w *syncFailWriter) Sync() error { return os.ErrPermission }

func TestJSONLReporterRecordWritesValidJSONL(t *testing.T) {
	file := filepath.Join(t.TempDir(), "runs.jsonl")
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer f.Close()

	reporter := NewJSONLReporter(f)
	rec := RunRecord{Timestamp: time.Now().UTC(), Scenario: "tool-bash-transient-fail", FaultType: FaultTransientError, RunID: "00000000-0000-4000-8000-000000000000", Outcome: "pass", DurationMS: 10, RecoveryAttempts: 1}
	if err := reporter.Record(rec); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var got RunRecord
	if err := json.Unmarshal(bytes.TrimSpace(data), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Scenario != rec.Scenario || got.RunID != rec.RunID || got.Outcome != rec.Outcome {
		t.Fatalf("record mismatch = %#v, want %#v", got, rec)
	}
}

func TestJSONLReporterSyncFailureWarnsOnly(t *testing.T) {
	writer := &syncFailWriter{}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	reporter := NewJSONLReporter(writer)
	rec := RunRecord{Timestamp: time.Now().UTC(), Scenario: "tool-bash-transient-fail", FaultType: FaultTransientError, RunID: "00000000-0000-4000-8000-000000000000", Outcome: "pass"}

	recordRunWarning(logger, reporter, rec)
	if !strings.Contains(logBuf.String(), "fault reporter write failed") {
		t.Fatalf("log output = %q, want warning", logBuf.String())
	}
}

func TestMDReporterRenderAllPass(t *testing.T) {
	out := renderReportForRecords(t, []RunRecord{{Timestamp: time.Now().UTC(), Scenario: "tool-bash-transient-fail", FaultType: FaultTransientError, RunID: "00000000-0000-4000-8000-000000000000", Outcome: "pass"}})
	if !strings.Contains(out, "PASS") {
		t.Fatalf("report = %q, want PASS", out)
	}
}

func TestMDReporterRenderAllFail(t *testing.T) {
	out := renderReportForRecords(t, []RunRecord{{Timestamp: time.Now().UTC(), Scenario: "tool-bash-transient-fail", FaultType: FaultTransientError, RunID: "00000000-0000-4000-8000-000000000000", Outcome: "fail", ErrorDetail: "boom"}})
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("report = %q, want FAIL", out)
	}
}

func TestMDReporterRenderMixedResults(t *testing.T) {
	out := renderReportForRecords(t, []RunRecord{{Timestamp: time.Now().UTC(), Scenario: "tool-bash-transient-fail", FaultType: FaultTransientError, RunID: "00000000-0000-4000-8000-000000000000", Outcome: "pass"}, {Timestamp: time.Now().UTC(), Scenario: "ipc-socket-drop", FaultType: FaultPacketDrop, RunID: "00000000-0000-4000-8000-000000000000", Outcome: "fail", ErrorDetail: "fault: packet drop injected"}})
	if !strings.Contains(out, "PASS") || !strings.Contains(out, "FAIL") {
		t.Fatalf("report = %q, want mixed PASS/FAIL", out)
	}
}

func renderReportForRecords(t *testing.T, records []RunRecord) string {
	t.Helper()
	runFile := filepath.Join(t.TempDir(), "runs.jsonl")
	f, err := os.OpenFile(runFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	reporter := NewJSONLReporter(f)
	for _, rec := range records {
		if err := reporter.Record(rec); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	_ = f.Close()

	var out bytes.Buffer
	if err := NewMDReporter(runFile, &out).Render(); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return out.String()
}
