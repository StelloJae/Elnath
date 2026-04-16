package skill

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type UsageRecord struct {
	SkillName string    `json:"skill_name"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
}

type PatternRecord struct {
	ID           string    `json:"id"`
	Description  string    `json:"description"`
	SessionIDs   []string  `json:"session_ids"`
	ToolSequence []string  `json:"tool_sequence"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	DraftSkill   string    `json:"draft_skill,omitempty"`
}

type Tracker struct {
	usagePath   string
	patternPath string
}

func NewTracker(dataDir string) *Tracker {
	return &Tracker{
		usagePath:   filepath.Join(dataDir, "skill-usage.jsonl"),
		patternPath: filepath.Join(dataDir, "skill-patterns.jsonl"),
	}
}

func (t *Tracker) RecordUsage(record UsageRecord) error {
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	return appendJSONL(t.usagePath, record)
}

func (t *Tracker) RecordPattern(record PatternRecord) error {
	now := time.Now().UTC()
	if record.FirstSeen.IsZero() {
		record.FirstSeen = now
	}
	if record.LastSeen.IsZero() {
		record.LastSeen = now
	}
	return appendJSONL(t.patternPath, record)
}

func (t *Tracker) LoadPatterns() ([]PatternRecord, error) {
	return readJSONL[PatternRecord](t.patternPath)
}

func (t *Tracker) UsageStats() (map[string]int, error) {
	records, err := readJSONL[UsageRecord](t.usagePath)
	if err != nil {
		return nil, err
	}
	stats := make(map[string]int, len(records))
	for _, record := range records {
		stats[record.SkillName]++
	}
	return stats, nil
}

func appendJSONL[T any](path string, record T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create jsonl dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(record); err != nil {
		return fmt.Errorf("encode jsonl record: %w", err)
	}
	return nil
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var results []T
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("decode jsonl record: %w", err)
		}
		results = append(results, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan jsonl: %w", err)
	}
	return results, nil
}
