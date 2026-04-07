package core

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/stello/elnath/internal/config"
)

// Closer is anything that can be closed during shutdown.
type Closer interface {
	Close() error
}

type App struct {
	Config *config.Config
	Logger *slog.Logger

	mu      sync.Mutex
	closers []namedCloser
	closed  bool
}

type namedCloser struct {
	name string
	c    Closer
}

func New(cfg *config.Config) (*App, error) {
	logger := SetupLogger(cfg.LogLevel)

	if err := ensureDirs(cfg); err != nil {
		return nil, fmt.Errorf("ensure directories: %w", err)
	}

	app := &App{
		Config: cfg,
		Logger: logger,
	}

	logger.Info("elnath initialized",
		"data_dir", cfg.DataDir,
		"wiki_dir", cfg.WikiDir,
		"log_level", cfg.LogLevel,
	)

	return app, nil
}

// RegisterCloser adds a resource to be cleaned up on shutdown.
// Resources are closed in LIFO order (last registered = first closed).
func (a *App) RegisterCloser(name string, c Closer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closers = append(a.closers, namedCloser{name: name, c: c})
}

// Close shuts down all registered resources in reverse order.
// Safe to call multiple times.
func (a *App) Close() error {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	closers := make([]namedCloser, len(a.closers))
	copy(closers, a.closers)
	a.mu.Unlock()

	a.Logger.Info("elnath shutting down", "resources", len(closers))

	var firstErr error
	for i := len(closers) - 1; i >= 0; i-- {
		nc := closers[i]
		if err := nc.c.Close(); err != nil {
			a.Logger.Error("close failed", "resource", nc.name, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("close %s: %w", nc.name, err)
			}
		} else {
			a.Logger.Debug("closed resource", "resource", nc.name)
		}
	}

	return firstErr
}

func ensureDirs(cfg *config.Config) error {
	dirs := []string{cfg.DataDir, cfg.WikiDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}
