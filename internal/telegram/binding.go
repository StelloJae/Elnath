package telegram

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const chatSessionBindingVersion = 1

type SessionValidator interface {
	Exists(sessionID string) bool
}

type ChatSessionBinder struct {
	path      string
	validator SessionValidator

	mu       sync.Mutex
	bindings map[string]string
}

type FileSessionValidator struct {
	DataDir string
}

type chatSessionBindingFile struct {
	Version  int               `json:"version"`
	Bindings map[string]string `json:"bindings"`
}

func NewChatSessionBinder(path string, validator SessionValidator) (*ChatSessionBinder, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("telegram binder: path is required")
	}

	bindings, err := loadBindings(path)
	if err != nil {
		return nil, err
	}

	return &ChatSessionBinder{
		path:      path,
		validator: validator,
		bindings:  bindings,
	}, nil
}

func (v FileSessionValidator) Exists(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	dataDir := strings.TrimSpace(v.DataDir)
	if sessionID == "" || dataDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dataDir, "sessions", sessionID+".jsonl"))
	return err == nil
}

func (b *ChatSessionBinder) Lookup(chatID, userID string) (string, bool) {
	key, ok := bindingKey(chatID, userID)
	if !ok {
		return "", false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	sessionID := strings.TrimSpace(b.bindings[key])
	if sessionID == "" {
		return "", false
	}
	if b.validator != nil && !b.validator.Exists(sessionID) {
		delete(b.bindings, key)
		if err := b.saveLocked(); err != nil {
			slog.Warn("telegram: drop stale chat binding", "chat_id", chatID, "user_id", userID, "session_id", sessionID, "error", err)
		}
		return "", false
	}
	return sessionID, true
}

func (b *ChatSessionBinder) Remember(chatID, userID, sessionID string) error {
	key, ok := bindingKey(chatID, userID)
	if !ok {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.bindings[key] == sessionID {
		return nil
	}
	b.bindings[key] = sessionID
	return b.saveLocked()
}

func (b *ChatSessionBinder) Forget(chatID, userID string) error {
	key, ok := bindingKey(chatID, userID)
	if !ok {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.bindings[key]; !exists {
		return nil
	}
	delete(b.bindings, key)
	return b.saveLocked()
}

func loadBindings(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("telegram binder: read %q: %w", path, err)
	}

	var state chatSessionBindingFile
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("telegram: ignore corrupt chat binding file", "path", path, "error", err)
		return make(map[string]string), nil
	}
	if state.Version != chatSessionBindingVersion {
		slog.Warn("telegram: ignore chat binding file with unknown version", "path", path, "version", state.Version)
		return make(map[string]string), nil
	}
	if state.Bindings == nil {
		state.Bindings = make(map[string]string)
	}
	return state.Bindings, nil
}

func (b *ChatSessionBinder) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return fmt.Errorf("telegram binder: mkdir: %w", err)
	}

	bindings := make(map[string]string, len(b.bindings))
	for key, sessionID := range b.bindings {
		bindings[key] = sessionID
	}
	data, err := json.MarshalIndent(chatSessionBindingFile{
		Version:  chatSessionBindingVersion,
		Bindings: bindings,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("telegram binder: encode: %w", err)
	}
	if err := os.WriteFile(b.path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("telegram binder: write: %w", err)
	}
	return nil
}

func bindingKey(chatID, userID string) (string, bool) {
	chatID = strings.TrimSpace(chatID)
	userID = strings.TrimSpace(userID)
	if chatID == "" || userID == "" {
		return "", false
	}
	return chatID + "|" + userID, true
}
