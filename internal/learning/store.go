package learning

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const scanMaxTokenSize = 8 * 1024 * 1024

type Filter struct {
	Topic      string
	Confidence string
	Since      time.Time
	Before     time.Time
	IDs        []string
	Limit      int
	Reverse    bool
}

func (f Filter) isZero() bool {
	return f.Topic == "" && f.Confidence == "" &&
		f.Since.IsZero() && f.Before.IsZero() && len(f.IDs) == 0
}

func (f Filter) validate() error {
	return validateIDPrefixes(f.IDs)
}

func (f Filter) match(lesson Lesson) bool {
	if f.Topic != "" {
		if !strings.Contains(strings.ToLower(lesson.Topic), strings.ToLower(f.Topic)) {
			return false
		}
	}
	if f.Confidence != "" && !strings.EqualFold(lesson.Confidence, f.Confidence) {
		return false
	}
	if !f.Since.IsZero() && lesson.Created.Before(f.Since) {
		return false
	}
	if !f.Before.IsZero() && !lesson.Created.Before(f.Before) {
		return false
	}
	if len(f.IDs) > 0 {
		hit := false
		for _, prefix := range f.IDs {
			if prefix != "" && strings.HasPrefix(lesson.ID, prefix) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

type Store struct {
	mu       sync.Mutex
	path     string
	redactor Redactor
}

type Redactor func(string) string

type StoreOption func(*Store)

func WithRedactor(r Redactor) StoreOption {
	return func(s *Store) { s.redactor = r }
}

func NewStore(path string, opts ...StoreOption) *Store {
	s := &Store{path: path}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Store) Append(lesson Lesson) error {
	if s == nil || s.path == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.redactor != nil {
		lesson.Text = s.redactor(lesson.Text)
		lesson.Topic = s.redactor(lesson.Topic)
		lesson.Source = s.redactor(lesson.Source)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("learning store: create dir: %w", err)
	}
	if lesson.Created.IsZero() {
		lesson.Created = time.Now().UTC()
	}
	if lesson.ID == "" {
		lesson.ID = deriveID(lesson.Text)
	}

	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("learning store: open file: %w", err)
	}
	if err := json.NewEncoder(file).Encode(lesson); err != nil {
		file.Close()
		return fmt.Errorf("learning store: encode lesson: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("learning store: close file: %w", err)
	}
	return nil
}

func (s *Store) List() ([]Lesson, error) {
	if s == nil || s.path == "" {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.readAllLocked()
}

func (s *Store) ListFiltered(f Filter) ([]Lesson, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}

	lessons, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(lessons) == 0 {
		return nil, nil
	}

	out := lessons[:0:0]
	for _, lesson := range lessons {
		if f.match(lesson) {
			out = append(out, lesson)
		}
	}
	if f.Reverse {
		sort.Slice(out, func(i, j int) bool {
			return out[i].Created.After(out[j].Created)
		})
	}
	if f.Limit > 0 && f.Limit < len(out) {
		out = out[:f.Limit]
	}
	return out, nil
}

func (s *Store) Delete(idPrefixes ...string) (int, error) {
	if s == nil || s.path == "" {
		return 0, nil
	}

	valid := make([]string, 0, len(idPrefixes))
	for _, prefix := range idPrefixes {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			valid = append(valid, prefix)
		}
	}
	if err := validateIDPrefixes(valid); err != nil {
		return 0, err
	}
	if len(valid) == 0 {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	err := s.rewriteLocked(func(lesson Lesson, keep func(Lesson)) {
		for _, prefix := range valid {
			if strings.HasPrefix(lesson.ID, prefix) {
				removed++
				return
			}
		}
		keep(lesson)
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

func (s *Store) DeleteMatching(f Filter) (int, error) {
	if s == nil || s.path == "" {
		return 0, nil
	}

	if f.isZero() {
		return 0, errors.New("learning store: DeleteMatching requires at least one filter")
	}
	if err := f.validate(); err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	err := s.rewriteLocked(func(lesson Lesson, keep func(Lesson)) {
		if f.match(lesson) {
			removed++
			return
		}
		keep(lesson)
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

func (s *Store) Clear() (int, error) {
	if s == nil || s.path == "" {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	lessons, err := s.readAllLocked()
	if err != nil {
		return 0, err
	}
	if len(lessons) == 0 {
		return 0, nil
	}
	if err := s.writeAllLocked(nil); err != nil {
		return 0, err
	}
	return len(lessons), nil
}

type RotateOpts struct {
	KeepLast int
	MaxBytes int64
}

func (o RotateOpts) hasBound() bool {
	return o.KeepLast > 0 || o.MaxBytes > 0
}

func (s *Store) Rotate(opts RotateOpts) (int, error) {
	if s == nil || s.path == "" {
		return 0, nil
	}

	if !opts.hasBound() {
		return 0, errors.New("learning store: Rotate requires KeepLast or MaxBytes")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	lessons, err := s.readAllLocked()
	if err != nil {
		return 0, err
	}
	if len(lessons) == 0 {
		return 0, nil
	}

	keepFromIdx := 0
	if opts.KeepLast > 0 && opts.KeepLast < len(lessons) {
		keepFromIdx = len(lessons) - opts.KeepLast
	}

	if opts.MaxBytes > 0 {
		idxByBytes, err := keepIndexForMaxBytes(lessons, opts.MaxBytes)
		if err != nil {
			return 0, err
		}
		if idxByBytes > keepFromIdx {
			keepFromIdx = idxByBytes
		}
	}

	if keepFromIdx <= 0 {
		return 0, nil
	}

	toArchive := lessons[:keepFromIdx]
	toKeep := lessons[keepFromIdx:]

	if err := s.appendArchiveLocked(toArchive); err != nil {
		return 0, err
	}
	if err := s.writeAllLocked(toKeep); err != nil {
		return 0, err
	}
	return len(toArchive), nil
}

func (s *Store) AutoRotateIfNeeded(opts RotateOpts) (int, error) {
	if s == nil || s.path == "" {
		return 0, nil
	}

	if !opts.hasBound() {
		return 0, nil
	}

	fi, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("learning store: stat: %w", err)
	}
	if opts.MaxBytes > 0 && fi.Size() > opts.MaxBytes {
		return s.Rotate(opts)
	}
	if opts.KeepLast <= 0 {
		return 0, nil
	}

	lessons, err := s.List()
	if err != nil {
		return 0, err
	}
	if len(lessons) <= opts.KeepLast {
		return 0, nil
	}
	return s.Rotate(opts)
}

type Stats struct {
	Total        int
	ByTopic      map[string]int
	ByConfidence map[string]int
	OldestAt     time.Time
	NewestAt     time.Time
	FileBytes    int64
}

func (s *Store) Summary() (Stats, error) {
	if s == nil || s.path == "" {
		return Stats{
			ByTopic:      make(map[string]int),
			ByConfidence: make(map[string]int),
		}, nil
	}

	lessons, err := s.List()
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{
		ByTopic:      make(map[string]int),
		ByConfidence: make(map[string]int),
	}
	if fi, statErr := os.Stat(s.path); statErr == nil {
		stats.FileBytes = fi.Size()
	}
	for _, lesson := range lessons {
		stats.Total++
		stats.ByTopic[lesson.Topic]++
		stats.ByConfidence[lesson.Confidence]++
		if stats.OldestAt.IsZero() || lesson.Created.Before(stats.OldestAt) {
			stats.OldestAt = lesson.Created
		}
		if lesson.Created.After(stats.NewestAt) {
			stats.NewestAt = lesson.Created
		}
	}
	return stats, nil
}

func (s *Store) Recent(n int) ([]Lesson, error) {
	lessons, err := s.List()
	if err != nil {
		return nil, err
	}
	sort.Slice(lessons, func(i, j int) bool {
		return lessons[i].Created.After(lessons[j].Created)
	})
	if n > 0 && n < len(lessons) {
		lessons = lessons[:n]
	}
	return lessons, nil
}

func (s *Store) archivePath() string {
	if strings.HasSuffix(s.path, ".jsonl") {
		return strings.TrimSuffix(s.path, ".jsonl") + ".archive.jsonl"
	}
	return s.path + ".archive"
}

func (s *Store) readAllLocked() ([]Lesson, error) {
	if s == nil || s.path == "" {
		return nil, nil
	}

	file, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("learning store: open file: %w", err)
	}
	defer file.Close()

	var lessons []Lesson
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scanMaxTokenSize)
	for scanner.Scan() {
		var lesson Lesson
		if err := json.Unmarshal(scanner.Bytes(), &lesson); err != nil {
			return nil, fmt.Errorf("learning store: decode lesson: %w", err)
		}
		lessons = append(lessons, lesson)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("learning store: scan file: %w", err)
	}
	if len(lessons) == 0 {
		return nil, nil
	}
	return lessons, nil
}

func (s *Store) writeAllLocked(lessons []Lesson) error {
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".lessons-*.tmp")
	if err != nil {
		return fmt.Errorf("learning store: create tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("learning store: chmod tempfile: %w", err)
	}

	enc := json.NewEncoder(tmp)
	for _, lesson := range lessons {
		if err := enc.Encode(lesson); err != nil {
			_ = tmp.Close()
			cleanup()
			return fmt.Errorf("learning store: encode lesson: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("learning store: sync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("learning store: close tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		cleanup()
		return fmt.Errorf("learning store: rename tempfile: %w", err)
	}
	return nil
}

func (s *Store) rewriteLocked(visit func(lesson Lesson, keep func(Lesson))) error {
	lessons, err := s.readAllLocked()
	if err != nil {
		return err
	}
	if len(lessons) == 0 {
		return nil
	}

	kept := make([]Lesson, 0, len(lessons))
	keep := func(lesson Lesson) {
		kept = append(kept, lesson)
	}
	for _, lesson := range lessons {
		visit(lesson, keep)
	}
	if len(kept) == len(lessons) {
		return nil
	}
	return s.writeAllLocked(kept)
}

func (s *Store) appendArchiveLocked(lessons []Lesson) error {
	if len(lessons) == 0 {
		return nil
	}

	archivePath := s.archivePath()
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return fmt.Errorf("learning store: archive dir: %w", err)
	}
	file, err := os.OpenFile(archivePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("learning store: open archive: %w", err)
	}
	enc := json.NewEncoder(file)
	for _, lesson := range lessons {
		if err := enc.Encode(lesson); err != nil {
			_ = file.Close()
			return fmt.Errorf("learning store: encode archive: %w", err)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("learning store: close archive: %w", err)
	}
	return nil
}

func keepIndexForMaxBytes(lessons []Lesson, maxBytes int64) (int, error) {
	if maxBytes <= 0 {
		return 0, nil
	}

	keepFromIdx := len(lessons)
	usedBytes := int64(0)
	for i := len(lessons) - 1; i >= 0; i-- {
		entryBytes, err := encodedLessonSize(lessons[i])
		if err != nil {
			return 0, err
		}
		if usedBytes+entryBytes > maxBytes {
			break
		}
		usedBytes += entryBytes
		keepFromIdx = i
	}
	if keepFromIdx == len(lessons) && len(lessons) > 0 {
		keepFromIdx = len(lessons) - 1
	}
	return keepFromIdx, nil
}

func validateIDPrefixes(prefixes []string) error {
	for _, prefix := range prefixes {
		if prefix != "" && len(prefix) < 4 {
			return errors.New("learning store: id prefix must be at least 4 chars")
		}
	}
	return nil
}

func encodedLessonSize(lesson Lesson) (int64, error) {
	data, err := json.Marshal(lesson)
	if err != nil {
		return 0, fmt.Errorf("learning store: encode size: %w", err)
	}
	return int64(len(data) + 1), nil
}
