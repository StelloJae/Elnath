package reflection

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StoreMeta carries the non-LLM envelope fields written alongside the Report.
// Principal/ProjectID are Phase 0 enrichment fields: passthrough-only, never
// consulted by trigger or evaluation logic. Empty values are omitted from the
// on-disk JSON (backward compatible with pre-v8.1 records).
type StoreMeta struct {
	TS        time.Time
	TaskID    string
	SessionID string
	Principal string
	ProjectID string
}

// Store persists Phase 0 reflection observations. Implementations MUST be
// append-only and safe for concurrent use.
type Store interface {
	Append(ctx context.Context, report Report, meta StoreMeta) error
}

// FileStore writes one JSON record per line to a local path. The format
// mirrors internal/learning/outcome_store.go so tooling (scorecard, grep,
// jq) can reuse existing idioms.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a FileStore rooted at path. The parent directory is
// created lazily on first Append.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Path returns the configured JSONL path (useful for status summaries and
// smoke tests).
func (s *FileStore) Path() string {
	return s.path
}

// diskRecord is the on-disk shape. Keeps Report flat and prefixes envelope
// fields so the file is scanner-friendly.
type diskRecord struct {
	TS                string `json:"ts"`
	TaskID            string `json:"task_id,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
	PrincipalUserID   string `json:"principal_user_id,omitempty"`
	ProjectID         string `json:"project_id,omitempty"`
	Fingerprint       string `json:"fingerprint"`
	FinishReason      string `json:"finish_reason"`
	ErrorCategory     string `json:"error_category"`
	SuggestedStrategy string `json:"suggested_strategy"`
	Reasoning         string `json:"reasoning,omitempty"`
	TaskSummary       string `json:"task_summary,omitempty"`
}

// Append writes one reflection record. The directory is created on first
// write (0o755) and the file uses 0o600 since observation data may include
// task-derived text.
func (s *FileStore) Append(ctx context.Context, report Report, meta StoreMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("reflection store: mkdir: %w", err)
	}
	if meta.TS.IsZero() {
		meta.TS = time.Now().UTC()
	}
	rec := diskRecord{
		TS:                meta.TS.UTC().Format(time.RFC3339Nano),
		TaskID:            meta.TaskID,
		SessionID:         meta.SessionID,
		PrincipalUserID:   meta.Principal,
		ProjectID:         meta.ProjectID,
		Fingerprint:       string(report.Fingerprint),
		FinishReason:      report.FinishReason,
		ErrorCategory:     report.ErrorCategory,
		SuggestedStrategy: string(report.SuggestedStrategy),
		Reasoning:         report.Reasoning,
		TaskSummary:       report.TaskSummary,
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("reflection store: open: %w", err)
	}
	if err := json.NewEncoder(f).Encode(rec); err != nil {
		_ = f.Close()
		return fmt.Errorf("reflection store: encode: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("reflection store: close: %w", err)
	}
	return nil
}

// Summary aggregates observation counts for the daemon status --self-heal
// command. It is read-only; callers should not mutate the returned maps.
type Summary struct {
	Total             int
	Path              string
	FinishReason      map[string]int
	ErrorCategory     map[string]int
	StrategyCounts    map[string]int
	FirstTS           time.Time
	LastTS            time.Time
	SchemaFailures    int
	SchemaFailureRate float64
}

// Read loads the entire JSONL file and returns a Summary. Missing file
// returns a zero-valued Summary without error. Malformed lines are skipped
// silently to keep the status command resilient against partial writes.
func (s *FileStore) Read() (Summary, error) {
	sum := Summary{
		Path:           s.path,
		FinishReason:   map[string]int{},
		ErrorCategory:  map[string]int{},
		StrategyCounts: map[string]int{},
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return sum, nil
		}
		return sum, fmt.Errorf("reflection store: open: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		var rec diskRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		sum.Total++
		if rec.FinishReason != "" {
			sum.FinishReason[rec.FinishReason]++
		}
		if rec.ErrorCategory != "" {
			sum.ErrorCategory[rec.ErrorCategory]++
		}
		if rec.SuggestedStrategy != "" {
			sum.StrategyCounts[rec.SuggestedStrategy]++
		}
		if rec.SuggestedStrategy == string(StrategyUnknown) {
			sum.SchemaFailures++
		}
		if ts, err := time.Parse(time.RFC3339Nano, rec.TS); err == nil {
			if sum.FirstTS.IsZero() || ts.Before(sum.FirstTS) {
				sum.FirstTS = ts
			}
			if ts.After(sum.LastTS) {
				sum.LastTS = ts
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return sum, fmt.Errorf("reflection store: scan: %w", err)
	}
	if sum.Total > 0 {
		sum.SchemaFailureRate = float64(sum.SchemaFailures) / float64(sum.Total)
	}
	return sum, nil
}
