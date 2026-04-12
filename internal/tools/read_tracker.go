package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ReadTracker struct {
	mu          sync.Mutex
	seen        map[readKey]readEntry
	consecutive map[readKey]int
	lastReadKey *readKey
}

type readKey struct {
	Tool   string
	Path   string
	Offset int
	Limit  int
	Query  string
}

type readEntry struct {
	ModTime time.Time
}

func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		seen:        make(map[readKey]readEntry),
		consecutive: make(map[readKey]int),
	}
}

func (t *ReadTracker) CheckRead(path string, offset, limit int) string {
	return t.check(readKey{Tool: "read", Path: path, Offset: offset, Limit: limit})
}

func (t *ReadTracker) CheckGrep(path, query string) string {
	return t.check(readKey{Tool: "grep", Path: path, Query: query})
}

func (t *ReadTracker) NotifyTool(toolName string) {
	if t == nil {
		return
	}
	switch toolName {
	case "read_file", "grep", "glob":
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.consecutive = make(map[readKey]int)
	t.lastReadKey = nil
}

func (t *ReadTracker) ResetDedup() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen = make(map[readKey]readEntry)
}

func (t *ReadTracker) RefreshPath(path string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for key := range t.seen {
		if key.Path == path {
			delete(t.seen, key)
		}
	}
}

func (t *ReadTracker) check(key readKey) string {
	if t == nil {
		return ""
	}
	modTime, err := trackedModTime(key.Path)
	if err != nil {
		return ""
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastReadKey != nil && *t.lastReadKey == key {
		t.consecutive[key]++
	} else {
		t.consecutive[key] = 1
	}
	keyCopy := key
	t.lastReadKey = &keyCopy

	entry, seen := t.seen[key]
	if !seen || !entry.ModTime.Equal(modTime) {
		t.seen[key] = readEntry{ModTime: modTime}
		t.consecutive[key] = 1
		return ""
	}

	count := t.consecutive[key]
	if count >= 4 {
		return blockedMessage(key)
	}

	msg := unchangedMessage(key)
	if count == 3 {
		msg += "\n" + warningMessage(key)
	}
	return msg
}

func unchangedMessage(key readKey) string {
	if key.Tool == "grep" {
		return fmt.Sprintf("[Search unchanged since last grep for %q in %s. Use a different pattern or path.]", key.Query, key.Path)
	}
	start := 1
	if key.Offset > 0 {
		start = key.Offset
	}
	end := start
	if key.Limit > 0 {
		end = start + key.Limit - 1
	}
	return fmt.Sprintf("[File unchanged since last read at line %d-%d. Use edit_file to make changes, or read a different section.]", start, end)
}

func warningMessage(key readKey) string {
	if key.Tool == "grep" {
		return "[WARNING: You have run this exact grep 3 times. Consider making your edit or searching a different path.]"
	}
	return "[WARNING: You have read this exact file region 3 times. Consider making your edit or reading a different file.]"
}

func blockedMessage(key readKey) string {
	if key.Tool == "grep" {
		return "[BLOCKED: You have run this exact grep 4 times consecutively. The content has not changed. Proceed with editing or move to a different file.]"
	}
	return "[BLOCKED: You have read this exact region 4 times consecutively. The content has not changed. Proceed with editing or move to a different file.]"
}

func trackedModTime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	if !info.IsDir() {
		return info.ModTime(), nil
	}
	latest := info.ModTime()
	err = filepath.WalkDir(path, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		entryInfo, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if entryInfo.ModTime().After(latest) {
			latest = entryInfo.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return latest, nil
}
