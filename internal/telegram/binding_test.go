package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type stubSessionValidator struct {
	mu     sync.Mutex
	exists map[string]bool
}

func (v *stubSessionValidator) Exists(sessionID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.exists[sessionID]
}

func (v *stubSessionValidator) set(sessionID string, exists bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.exists == nil {
		v.exists = make(map[string]bool)
	}
	v.exists[sessionID] = exists
}

type bindingFileState struct {
	Version  int               `json:"version"`
	Bindings map[string]string `json:"bindings"`
}

func readBindingFile(t *testing.T, path string) bindingFileState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var state bindingFileState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}
	if state.Bindings == nil {
		state.Bindings = make(map[string]string)
	}
	return state
}

func TestBinderLookupMissEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	binder, err := NewChatSessionBinder(path, &stubSessionValidator{})
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}

	if got, ok := binder.Lookup("chat-a", "user-1"); ok || got != "" {
		t.Fatalf("Lookup() = (%q, %v), want miss", got, ok)
	}
}

func TestBinderRememberThenLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	validator := &stubSessionValidator{}
	validator.set("sess-1", true)
	binder, err := NewChatSessionBinder(path, validator)
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}

	if err := binder.Remember("chat-a", "user-1", "sess-1"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	got, ok := binder.Lookup("chat-a", "user-1")
	if !ok || got != "sess-1" {
		t.Fatalf("Lookup() = (%q, %v), want (sess-1, true)", got, ok)
	}
}

func TestBinderLookupStaleSessionForgotten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	validator := &stubSessionValidator{}
	binder, err := NewChatSessionBinder(path, validator)
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}
	if err := binder.Remember("chat-a", "user-1", "stale-sess"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	if got, ok := binder.Lookup("chat-a", "user-1"); ok || got != "" {
		t.Fatalf("Lookup() = (%q, %v), want miss for stale session", got, ok)
	}

	state := readBindingFile(t, path)
	if _, ok := state.Bindings["chat-a|user-1"]; ok {
		t.Fatalf("stale binding still persisted: %+v", state.Bindings)
	}
}

func TestBinderPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	validator := &stubSessionValidator{}
	validator.set("sess-1", true)
	binder, err := NewChatSessionBinder(path, validator)
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}
	if err := binder.Remember("chat-a", "user-1", "sess-1"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	reopened, err := NewChatSessionBinder(path, validator)
	if err != nil {
		t.Fatalf("NewChatSessionBinder(reopen): %v", err)
	}
	got, ok := reopened.Lookup("chat-a", "user-1")
	if !ok || got != "sess-1" {
		t.Fatalf("Lookup() after reopen = (%q, %v), want (sess-1, true)", got, ok)
	}
}

func TestBinderKeyIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	validator := &stubSessionValidator{}
	binder, err := NewChatSessionBinder(path, validator)
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}

	entries := []struct {
		chatID    string
		userID    string
		sessionID string
	}{
		{chatID: "chat-a", userID: "user-1", sessionID: "sess-1"},
		{chatID: "chat-a", userID: "user-2", sessionID: "sess-2"},
		{chatID: "chat-b", userID: "user-1", sessionID: "sess-3"},
	}
	for _, entry := range entries {
		validator.set(entry.sessionID, true)
		if err := binder.Remember(entry.chatID, entry.userID, entry.sessionID); err != nil {
			t.Fatalf("Remember(%+v): %v", entry, err)
		}
	}

	for _, entry := range entries {
		got, ok := binder.Lookup(entry.chatID, entry.userID)
		if !ok || got != entry.sessionID {
			t.Fatalf("Lookup(%q, %q) = (%q, %v), want (%q, true)", entry.chatID, entry.userID, got, ok, entry.sessionID)
		}
	}
}

func TestBinderConcurrentRememberSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	validator := &stubSessionValidator{}
	binder, err := NewChatSessionBinder(path, validator)
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("sess-%02d", i)
			userID := fmt.Sprintf("user-%02d", i)
			validator.set(sessionID, true)
			if err := binder.Remember("chat-a", userID, sessionID); err != nil {
				t.Errorf("Remember(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	reopened, err := NewChatSessionBinder(path, validator)
	if err != nil {
		t.Fatalf("NewChatSessionBinder(reopen): %v", err)
	}
	for i := 0; i < 50; i++ {
		sessionID := fmt.Sprintf("sess-%02d", i)
		userID := fmt.Sprintf("user-%02d", i)
		got, ok := reopened.Lookup("chat-a", userID)
		if !ok || got != sessionID {
			t.Fatalf("Lookup(user=%q) = (%q, %v), want (%q, true)", userID, got, ok, sessionID)
		}
	}

	state := readBindingFile(t, path)
	if len(state.Bindings) != 50 {
		t.Fatalf("persisted bindings = %d, want 50", len(state.Bindings))
	}
}

func TestBinderEmptyInputNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	binder, err := NewChatSessionBinder(path, &stubSessionValidator{})
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}

	calls := []struct {
		name string
		fn   func() error
	}{
		{
			name: "empty chat",
			fn:   func() error { return binder.Remember(" ", "user-1", "sess-1") },
		},
		{
			name: "empty user",
			fn:   func() error { return binder.Remember("chat-a", " ", "sess-1") },
		},
		{
			name: "empty session",
			fn:   func() error { return binder.Remember("chat-a", "user-1", " ") },
		},
		{
			name: "forget empty key",
			fn:   func() error { return binder.Forget(" ", "user-1") },
		},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); err != nil {
				t.Fatalf("call returned error: %v", err)
			}
		})
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("binding file should not exist after no-op calls, stat err=%v", err)
	}
}

func TestBinderVersionMismatchFailsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"bindings":{"chat-a|user-1":"sess-1"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	binder, err := NewChatSessionBinder(path, &stubSessionValidator{})
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}
	if len(binder.bindings) != 0 {
		t.Fatalf("bindings loaded from unknown version: %+v", binder.bindings)
	}
	if got, ok := binder.Lookup("chat-a", "user-1"); ok || got != "" {
		t.Fatalf("Lookup() = (%q, %v), want miss after fail-open", got, ok)
	}
}

func TestBinderCorruptFileFailsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telegram-chat-bindings.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	binder, err := NewChatSessionBinder(path, &stubSessionValidator{})
	if err != nil {
		t.Fatalf("NewChatSessionBinder: %v", err)
	}
	if len(binder.bindings) != 0 {
		t.Fatalf("bindings loaded from corrupt file: %+v", binder.bindings)
	}
	if got, ok := binder.Lookup("chat-a", "user-1"); ok || got != "" {
		t.Fatalf("Lookup() = (%q, %v), want miss after fail-open", got, ok)
	}
}
