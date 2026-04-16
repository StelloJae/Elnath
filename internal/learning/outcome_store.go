package learning

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type OutcomeStore struct {
	mu       sync.Mutex
	path     string
	redactor Redactor
}

type OutcomeStoreOption func(*OutcomeStore)

func WithOutcomeRedactor(r Redactor) OutcomeStoreOption {
	return func(s *OutcomeStore) { s.redactor = r }
}

func NewOutcomeStore(path string, opts ...OutcomeStoreOption) *OutcomeStore {
	s := &OutcomeStore{path: path}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *OutcomeStore) Append(record OutcomeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("learning outcome: create dir: %w", err)
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	if record.ID == "" {
		record.ID = deriveOutcomeID(record.ProjectID, record.Intent, record.Workflow, record.Timestamp)
	}
	if s.redactor != nil {
		record.ProjectID = s.redactor(record.ProjectID)
		record.Intent = s.redactor(record.Intent)
		record.Workflow = s.redactor(record.Workflow)
		record.FinishReason = s.redactor(record.FinishReason)
	}

	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("learning outcome: open file: %w", err)
	}
	if err := json.NewEncoder(file).Encode(record); err != nil {
		file.Close()
		return fmt.Errorf("learning outcome: encode record: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("learning outcome: close file: %w", err)
	}
	return nil
}

func (s *OutcomeStore) Recent(n int) ([]OutcomeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})
	if n > 0 && n < len(records) {
		records = records[:n]
	}
	return records, nil
}

func (s *OutcomeStore) ForProject(projectID string, n int) ([]OutcomeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}

	var filtered []OutcomeRecord
	for _, r := range all {
		if r.ProjectID == projectID {
			filtered = append(filtered, r)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Timestamp.After(filtered[j].Timestamp)
	})
	if n > 0 && n < len(filtered) {
		filtered = filtered[:n]
	}
	return filtered, nil
}

func (s *OutcomeStore) Rotate(keepLast int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readAllLocked()
	if err != nil {
		return err
	}
	if keepLast >= len(records) {
		return nil
	}

	toKeep := records[len(records)-keepLast:]
	return s.writeAllLocked(toKeep)
}

func (s *OutcomeStore) AutoRotateIfNeeded(keepLast int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readAllLocked()
	if err != nil {
		return err
	}
	if len(records) <= keepLast*2 {
		return nil
	}

	toKeep := records[len(records)-keepLast:]
	return s.writeAllLocked(toKeep)
}

func (s *OutcomeStore) readAllLocked() ([]OutcomeRecord, error) {
	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("learning outcome: open file: %w", err)
	}
	defer file.Close()

	var records []OutcomeRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scanMaxTokenSize)
	for scanner.Scan() {
		var r OutcomeRecord
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("learning outcome: decode record: %w", err)
		}
		records = append(records, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("learning outcome: scan file: %w", err)
	}
	return records, nil
}

func (s *OutcomeStore) writeAllLocked(records []OutcomeRecord) error {
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".outcomes-*.tmp")
	if err != nil {
		return fmt.Errorf("learning outcome: create tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("learning outcome: chmod tempfile: %w", err)
	}

	enc := json.NewEncoder(tmp)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			_ = tmp.Close()
			cleanup()
			return fmt.Errorf("learning outcome: encode record: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("learning outcome: sync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("learning outcome: close tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		cleanup()
		return fmt.Errorf("learning outcome: rename tempfile: %w", err)
	}
	return nil
}
