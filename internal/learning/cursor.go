package learning

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CursorStore struct {
	mu   sync.Mutex
	path string
}

type cursorRecord struct {
	SessionID string    `json:"session_id"`
	LastLine  int       `json:"last_line"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewCursorStore(path string) *CursorStore { return &CursorStore{path: path} }

func (c *CursorStore) Get(sessionID string) (int, error) {
	if c == nil || c.path == "" || sessionID == "" {
		return 0, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	f, err := os.Open(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("cursor store: open: %w", err)
	}
	defer f.Close()

	latest := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec cursorRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		if rec.SessionID == sessionID && rec.LastLine > latest {
			latest = rec.LastLine
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("cursor store: scan: %w", err)
	}
	return latest, nil
}

func (c *CursorStore) Update(sessionID string, lastLine int) error {
	if c == nil || c.path == "" || sessionID == "" {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("cursor store: mkdir: %w", err)
	}
	f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("cursor store: open: %w", err)
	}
	defer f.Close()

	rec := cursorRecord{SessionID: sessionID, LastLine: lastLine, UpdatedAt: time.Now().UTC()}
	if err := json.NewEncoder(f).Encode(rec); err != nil {
		return fmt.Errorf("cursor store: encode: %w", err)
	}
	return nil
}
