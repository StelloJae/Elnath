package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stello/elnath/internal/llm"
)

func TestResolveSessionID_ExactMatchRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.AppendMessage(llm.NewUserMessage("hello")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	got, err := ResolveSessionID(dir, s.ID)
	if err != nil {
		t.Fatalf("ResolveSessionID(full id): %v", err)
	}
	if got != s.ID {
		t.Fatalf("exact-match = %q, want %q", got, s.ID)
	}
}

func TestResolveSessionID_UniquePrefix(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.AppendMessage(llm.NewUserMessage("hello")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	prefix := s.ID[:13]
	got, err := ResolveSessionID(dir, prefix)
	if err != nil {
		t.Fatalf("ResolveSessionID(prefix): %v", err)
	}
	if got != s.ID {
		t.Fatalf("prefix-match = %q, want %q", got, s.ID)
	}
}

func TestResolveSessionID_TrimsStatusEllipsis(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.AppendMessage(llm.NewUserMessage("hello")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	got, err := ResolveSessionID(dir, s.ID[:13]+"...")
	if err != nil {
		t.Fatalf("ResolveSessionID(prefix+...): %v", err)
	}
	if got != s.ID {
		t.Fatalf("ellipsis-trim = %q, want %q", got, s.ID)
	}

	got, err = ResolveSessionID(dir, "  "+s.ID[:13]+"…  ")
	if err != nil {
		t.Fatalf("ResolveSessionID(unicode-ellipsis): %v", err)
	}
	if got != s.ID {
		t.Fatalf("unicode-ellipsis = %q, want %q", got, s.ID)
	}
}

func TestResolveSessionID_Ambiguous(t *testing.T) {
	dir := t.TempDir()

	sharedPrefix := "12345678-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	otherWithPrefix := "12345678-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	for _, id := range []string{sharedPrefix, otherWithPrefix} {
		path := sessionPath(dir, id)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("{\"id\":\""+id+"\"}\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	_, err := ResolveSessionID(dir, "12345678")
	if err == nil {
		t.Fatal("ambiguous prefix: err = nil, want ambiguous error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "ambiguous") {
		t.Fatalf("err = %q, want ambiguous phrasing", err.Error())
	}
	if !strings.Contains(err.Error(), sharedPrefix) || !strings.Contains(err.Error(), otherWithPrefix) {
		t.Fatalf("err = %q, want both candidate IDs listed", err.Error())
	}
}

func TestResolveSessionID_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveSessionID(dir, "deadbeef")
	if err == nil {
		t.Fatal("not-found: err = nil, want error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("err = %q, want 'not found' phrasing", err.Error())
	}
}

func TestResolveSessionID_EmptyInput(t *testing.T) {
	_, err := ResolveSessionID(t.TempDir(), "")
	if err == nil {
		t.Fatal("empty input: err = nil, want error")
	}
	_, err = ResolveSessionID(t.TempDir(), "   ")
	if err == nil {
		t.Fatal("whitespace-only: err = nil, want error")
	}
}

func TestResolveSessionID_ExactMatchPreferredOverPrefix(t *testing.T) {
	dir := t.TempDir()

	base := "12345678-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	longer := "12345678-aaaa-aaaa-aaaa-aaaaaaaaaaaa-extra"

	for _, id := range []string{base, longer} {
		path := sessionPath(dir, id)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("{\"id\":\""+id+"\"}\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	got, err := ResolveSessionID(dir, base)
	if err != nil {
		t.Fatalf("exact-vs-prefix: %v", err)
	}
	if got != base {
		t.Fatalf("exact input = %q, want %q", got, base)
	}
}
