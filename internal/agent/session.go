package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/stello/elnath/internal/llm"
)

// Session is a persisted conversation stored as a JSONL file.
// Format: first line is a sessionHeader, subsequent lines are llm.Message.
type Session struct {
	ID       string
	path     string
	Messages []llm.Message
}

type sessionHeader struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Version   int       `json:"version"`
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
func (s *Session) AppendMessages(msgs []llm.Message) error {
	for _, m := range msgs {
		if err := s.AppendMessage(m); err != nil {
			return err
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
