package fault

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/stello/elnath/internal/fault/scenarios"
)

// Fault를 수신한 후 agent loop의 top-level iteration 1회 = recovery attempt 1회.
// streamWithRetry 내부 재시도는 count하지 않는다. 시나리오 #10의 daemon
// recover block 자체는 count하지 않으며, 그 다음 task re-submission이 1회다.
type RunRecord struct {
	Timestamp        time.Time `json:"timestamp"`
	Scenario         string    `json:"scenario"`
	FaultType        FaultType `json:"fault_type"`
	RunID            string    `json:"run_id"`
	Outcome          string    `json:"outcome"`
	DurationMS       int64     `json:"duration_ms"`
	RecoveryAttempts int       `json:"recovery_attempts"`
	ErrorDetail      string    `json:"error_detail,omitempty"`
}

type JSONLReporter struct{ w io.Writer }

func NewJSONLReporter(w io.Writer) *JSONLReporter { return &JSONLReporter{w: w} }

func (r *JSONLReporter) Record(rec RunRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("fault reporter: marshal: %w", err)
	}
	if _, err := fmt.Fprintf(r.w, "%s\n", b); err != nil {
		return fmt.Errorf("fault reporter: write: %w", err)
	}
	if syncer, ok := r.w.(interface{ Sync() error }); ok {
		if err := syncer.Sync(); err != nil {
			return fmt.Errorf("fault reporter: sync: %w", err)
		}
	}
	return nil
}

func recordRunWarning(logger *slog.Logger, reporter *JSONLReporter, rec RunRecord) {
	if reporter == nil {
		return
	}
	if err := reporter.Record(rec); err != nil && logger != nil {
		logger.Warn("fault reporter write failed", "error", err)
	}
}

type MDReporter struct {
	runFile string
	out     io.Writer
}

func NewMDReporter(runFile string, out io.Writer) *MDReporter {
	return &MDReporter{runFile: runFile, out: out}
}

func (r *MDReporter) Render() error {
	records, err := loadRunRecords(r.runFile)
	if err != nil {
		return err
	}
	registry := NewRegistry(scenarios.All())
	type stat struct {
		runs        int
		passes      int
		failures    int
		maxAttempts int
	}
	stats := map[string]*stat{}
	for _, rec := range records {
		st := stats[rec.Scenario]
		if st == nil {
			st = &stat{}
			stats[rec.Scenario] = st
		}
		st.runs++
		if rec.Outcome == "pass" {
			st.passes++
		} else {
			st.failures++
		}
		if rec.RecoveryAttempts > st.maxAttempts {
			st.maxAttempts = rec.RecoveryAttempts
		}
	}
	date := time.Now().UTC().Format("2006-01-02")
	if _, err := fmt.Fprintf(r.out, "# Fault Injection Report - %s\n\n", date); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out, "## Summary"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out, "| scenario | runs | pass | fail | pass-rate | status |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out, "| --- | ---: | ---: | ---: | ---: | --- |"); err != nil {
		return err
	}
	for _, scenario := range registry.All() {
		st := stats[scenario.Name]
		if st == nil {
			continue
		}
		passRate := float64(st.passes) / float64(st.runs)
		status := "PASS"
		if passRate < scenario.Threshold.RecoveryRate || st.maxAttempts > scenario.Threshold.MaxRecoveryAttempts {
			status = "FAIL"
		}
		if _, err := fmt.Fprintf(r.out, "| %s | %d | %d | %d | %.2f | %s |\n", scenario.Name, st.runs, st.passes, st.failures, passRate, status); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(r.out); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out, "## Failed Runs"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out); err != nil {
		return err
	}
	failed := topFailedRuns(records, 5)
	if len(failed) == 0 {
		if _, err := fmt.Fprintln(r.out, "- none"); err != nil {
			return err
		}
	} else {
		for _, rec := range failed {
			detail := strings.TrimSpace(rec.ErrorDetail)
			if detail == "" {
				detail = rec.Outcome
			}
			if _, err := fmt.Fprintf(r.out, "- %s: %s\n", rec.Scenario, detail); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(r.out); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out, "## Recommendations"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.out); err != nil {
		return err
	}
	recommended := false
	for _, scenario := range registry.All() {
		st := stats[scenario.Name]
		if st == nil || st.runs == 0 {
			continue
		}
		passRate := float64(st.passes) / float64(st.runs)
		if passRate >= scenario.Threshold.RecoveryRate && st.maxAttempts <= scenario.Threshold.MaxRecoveryAttempts {
			continue
		}
		recommended = true
		if _, err := fmt.Fprintf(r.out, "- %s below threshold (pass-rate %.2f, max attempts %d)\n", scenario.Name, passRate, st.maxAttempts); err != nil {
			return err
		}
	}
	if !recommended {
		_, err = fmt.Fprintln(r.out, "- all recorded scenarios met their thresholds")
		return err
	}
	return nil
}

func loadRunRecords(runFile string) ([]RunRecord, error) {
	f, err := os.Open(runFile)
	if err != nil {
		return nil, fmt.Errorf("fault reporter: open runs file: %w", err)
	}
	defer f.Close()

	var records []RunRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec RunRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("fault reporter: decode run record: %w", err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("fault reporter: scan runs file: %w", err)
	}
	return records, nil
}

func topFailedRuns(records []RunRecord, limit int) []RunRecord {
	failed := make([]RunRecord, 0, len(records))
	for _, rec := range records {
		if rec.Outcome != "pass" {
			failed = append(failed, rec)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].Timestamp.After(failed[j].Timestamp) })
	if len(failed) > limit {
		failed = failed[:limit]
	}
	return failed
}
