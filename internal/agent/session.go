package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stello/elnath/internal/llm"
)

// SessionPersister is an optional secondary persistence backend for sessions.
// Implementations (e.g., SQLite history store) mirror the canonical JSONL
// transcript for indexing/search and must not redefine resume semantics.
type SessionPersister interface {
	PersistSession(sessionID string, messages []llm.Message) error
}

// Session is a persisted conversation stored as a JSONL file.
// Format: first line is a sessionHeader, subsequent lines are llm.Message.
type Session struct {
	ID        string
	path      string
	Messages  []llm.Message
	persister SessionPersister // optional secondary persistence
	logger    func(msg string, args ...any)
}

// WithPersister sets an optional secondary persistence backend.
func (s *Session) WithPersister(p SessionPersister) {
	s.persister = p
}

// WithLogger sets a logger for persistence warnings.
func (s *Session) WithSessionLogger(fn func(msg string, args ...any)) {
	s.logger = fn
}

type sessionHeader struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Version   int       `json:"version"`
}

// SessionFileInfo describes the file-backed metadata for a persisted session.
// It is intentionally derived from the canonical JSONL transcript plus
// filesystem state so callers can reconcile it with any secondary history store.
type SessionFileInfo struct {
	ID           string
	Path         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
}

// NewSession creates a new session with a random ID.
// The session is not persisted until Save or AppendMessage is called.
func NewSession(dataDir string) (*Session, error) {
	id := uuid.New().String()
	path := sessionPath(dataDir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("session: create dir: %w", err)
	}
	s := &Session{ID: id, path: path}
	if err := s.writeHeader(); err != nil {
		return nil, err
	}
	return s, nil
}

// LoadSession reads a session from disk by ID.
func LoadSession(dataDir, id string) (*Session, error) {
	path := sessionPath(dataDir, id)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open: %w", err)
	}
	defer f.Close()

	s := &Session{ID: id, path: path}
	scanner := bufio.NewScanner(f)

	// First line: header.
	if !scanner.Scan() {
		return nil, fmt.Errorf("session: empty file")
	}
	var hdr sessionHeader
	if err := json.Unmarshal(scanner.Bytes(), &hdr); err != nil {
		return nil, fmt.Errorf("session: parse header: %w", err)
	}
	s.ID = hdr.ID

	// Remaining lines: messages.
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("session: parse message: %w", err)
		}
		s.Messages = append(s.Messages, msg)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("session: read: %w", err)
	}

	return s, nil
}

// AppendMessage appends a single message to the session file (O_APPEND) and
// updates the in-memory slice. JSONL lines are small enough that O_APPEND is
// atomic on POSIX filesystems for reasonable message sizes.
func (s *Session) AppendMessage(msg llm.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("session: marshal message: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("session: open for append: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write message: %w", err)
	}

	s.Messages = append(s.Messages, msg)
	return nil
}

// AppendMessages appends multiple messages in a single write for efficiency.
// Also syncs to secondary persister (e.g., SQLite) if configured.
func (s *Session) AppendMessages(msgs []llm.Message) error {
	for _, m := range msgs {
		if err := s.AppendMessage(m); err != nil {
			return err
		}
	}
	// Secondary persistence: best-effort, never blocks primary JSONL.
	if s.persister != nil {
		if err := s.persister.PersistSession(s.ID, s.Messages); err != nil {
			if s.logger != nil {
				s.logger("secondary persist failed", "session_id", s.ID, "error", err)
			}
		}
	}
	return nil
}

// Fork creates a new session that starts with a copy of the current messages.
// The forked session has its own ID and file; the original is unchanged.
func (s *Session) Fork(dataDir string) (*Session, error) {
	child, err := NewSession(dataDir)
	if err != nil {
		return nil, fmt.Errorf("session: fork: %w", err)
	}
	if err := child.AppendMessages(s.Messages); err != nil {
		return nil, fmt.Errorf("session: fork copy: %w", err)
	}
	return child, nil
}

// writeHeader writes the JSONL header line to a new file.
func (s *Session) writeHeader() error {
	hdr := sessionHeader{
		ID:        s.ID,
		CreatedAt: time.Now().UTC(),
		Version:   1,
	}
	data, err := json.Marshal(hdr)
	if err != nil {
		return fmt.Errorf("session: marshal header: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("session: create file: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write header: %w", err)
	}
	return nil
}

func sessionPath(dataDir, id string) string {
	return filepath.Join(dataDir, "sessions", id+".jsonl")
}

// ListSessionFiles returns metadata for all JSONL-backed sessions known on disk.
// UpdatedAt is derived from file modification time; CreatedAt is read from the
// JSONL header when available. Files that cannot be parsed still participate via
// their filename and modtime so callers can skip them explicitly later.
func ListSessionFiles(dataDir string) ([]SessionFileInfo, error) {
	sessDir := filepath.Join(dataDir, "sessions")
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: list files: %w", err)
	}

	infos := make([]SessionFileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		path := filepath.Join(sessDir, entry.Name())
		fileInfo, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("session: stat %s: %w", entry.Name(), err)
		}

		meta, err := readSessionFileInfo(path)
		if err != nil {
			meta = SessionFileInfo{
				ID:   strings.TrimSuffix(entry.Name(), ".jsonl"),
				Path: path,
			}
		}
		meta.Path = path
		meta.UpdatedAt = fileInfo.ModTime().UTC()
		if meta.ID == "" {
			meta.ID = strings.TrimSuffix(entry.Name(), ".jsonl")
		}
		infos = append(infos, meta)
	}

	sort.SliceStable(infos, func(i, j int) bool {
		if !infos[i].UpdatedAt.Equal(infos[j].UpdatedAt) {
			return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
		}
		if !infos[i].CreatedAt.Equal(infos[j].CreatedAt) {
			return infos[i].CreatedAt.After(infos[j].CreatedAt)
		}
		return infos[i].ID > infos[j].ID
	})

	return infos, nil
}

func readSessionFileInfo(path string) (SessionFileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionFileInfo{}, fmt.Errorf("session: open metadata: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return SessionFileInfo{}, fmt.Errorf("session: read metadata header: %w", err)
		}
		return SessionFileInfo{}, fmt.Errorf("session: empty file")
	}

	var hdr sessionHeader
	if err := json.Unmarshal(scanner.Bytes(), &hdr); err != nil {
		return SessionFileInfo{}, fmt.Errorf("session: parse metadata header: %w", err)
	}

	info := SessionFileInfo{
		ID:        hdr.ID,
		CreatedAt: hdr.CreatedAt.UTC(),
	}
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		info.MessageCount++
	}
	if err := scanner.Err(); err != nil {
		return SessionFileInfo{}, fmt.Errorf("session: scan metadata: %w", err)
	}

	return info, nil
}
