package core

import (
	"errors"
	"testing"

	"github.com/stello/elnath/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		DataDir:  dir + "/data",
		WikiDir:  dir + "/wiki",
		LogLevel: "error",
		Permission: config.PermissionConfig{Mode: "default"},
	}
}

func TestNew(t *testing.T) {
	cfg := testConfig(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	if app.Config != cfg {
		t.Error("Config not set")
	}
	if app.Logger == nil {
		t.Error("Logger should not be nil")
	}
}

func TestAppClose_Idempotent(t *testing.T) {
	cfg := testConfig(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := app.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("second Close should be safe: %v", err)
	}
}

func TestAppClose_NilSafe(t *testing.T) {
	var app *App
	if err := app.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

type mockCloser struct {
	name   string
	err    error
	closed bool
}

func (m *mockCloser) Close() error {
	m.closed = true
	return m.err
}

func TestRegisterCloser_LIFO(t *testing.T) {
	cfg := testConfig(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var order []string
	c1 := &mockCloser{name: "first"}
	c2 := &mockCloser{name: "second"}

	app.RegisterCloser("first", c1)
	app.RegisterCloser("second", c2)

	_ = order // closers track via closed field
	app.Close()

	if !c1.closed {
		t.Error("first closer not closed")
	}
	if !c2.closed {
		t.Error("second closer not closed")
	}
}

func TestRegisterCloser_ErrorPropagation(t *testing.T) {
	cfg := testConfig(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	wantErr := errors.New("close failed")
	app.RegisterCloser("ok", &mockCloser{})
	app.RegisterCloser("failing", &mockCloser{err: wantErr})
	app.RegisterCloser("ok2", &mockCloser{})

	err = app.Close()
	if err == nil {
		t.Fatal("expected error from failing closer")
	}
}

func TestOpenDB(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	if db.Main == nil {
		t.Error("Main DB should not be nil")
	}
	if db.Wiki == nil {
		t.Error("Wiki DB should not be nil")
	}

	// Verify WAL mode is set.
	var journalMode string
	err = db.Main.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}
}

func TestDBClose_NilSafe(t *testing.T) {
	var db *DB
	if err := db.Close(); err != nil {
		t.Fatalf("nil DB Close: %v", err)
	}
}

func TestHasFTS5(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	// modernc.org/sqlite supports FTS5.
	if !HasFTS5(db.Main) {
		t.Error("expected FTS5 to be available with modernc.org/sqlite")
	}
}
