package core

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/stello/elnath/internal/config"
)

type App struct {
	Config *config.Config
	Logger *slog.Logger
	// DB     *DB      // added when DB is initialized
	// Wiki   *WikiDB  // added when wiki DB is initialized
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

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	a.Logger.Info("elnath shutting down")
	return nil
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
