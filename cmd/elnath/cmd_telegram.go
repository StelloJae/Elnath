package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/telegram"
)

func cmdTelegram(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Print(`Usage: elnath telegram <subcommand>

Subcommands:
  shell              Start the thin Telegram operator shell
`)
		return nil
	}
	switch args[0] {
	case "shell":
		return cmdTelegramShell(ctx)
	default:
		return fmt.Errorf("unknown telegram subcommand: %s", args[0])
	}
}

func cmdTelegramShell(ctx context.Context) error {
	cfgPath := extractConfigFlag(os.Args)
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.Telegram.Enabled {
		return fmt.Errorf("telegram shell requires telegram.enabled=true")
	}

	app, err := core.New(cfg)
	if err != nil {
		return fmt.Errorf("init app: %w", err)
	}
	defer app.Close()

	db, err := core.OpenDB(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	app.RegisterCloser("database", db)

	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		return fmt.Errorf("create queue: %w", err)
	}
	approvals, err := daemon.NewApprovalStore(db.Main)
	if err != nil {
		return fmt.Errorf("create approval store: %w", err)
	}
	bot := telegram.NewHTTPClient(cfg.Telegram.BotToken, cfg.Telegram.APIBaseURL)
	statePath := filepath.Join(cfg.DataDir, "telegram-shell-state.json")
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get cwd: %w", err)
	}
	shell, err := telegram.NewShell(queue, approvals, bot, cfg.Telegram.ChatID, statePath, telegram.WithWorkDir(cwd))
	if err != nil {
		return err
	}
	return runTelegramShell(ctx, shell, bot, cfg.Telegram.PollTimeoutSeconds, app.Logger)
}

func runTelegramShell(ctx context.Context, shell *telegram.Shell, bot telegram.BotClient, pollTimeout int, logger *slog.Logger) error {
	if pollTimeout <= 0 {
		pollTimeout = 30
	}
	offset, err := shell.NextOffset()
	if err != nil {
		return fmt.Errorf("telegram load shell state: %w", err)
	}
	for {
		if err := shell.NotifyCompletions(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if logger != nil {
				logger.Error("telegram notify completions", "error", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		updates, err := bot.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if telegram.IsPollingConflict(err) {
				return fmt.Errorf("telegram get updates: another Telegram poller is already using this bot token; stop the other poller and retry: %w", err)
			}
			if logger != nil {
				logger.Error("telegram get updates", "offset", offset, "error", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, update := range updates {
			if update.ID >= offset {
				offset = update.ID + 1
			}
			if err := shell.HandleUpdate(ctx, update); err != nil && logger != nil {
				logger.Error("telegram handle update", "update_id", update.ID, "error", err)
			}
		}
		if err := shell.RememberOffset(offset); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if logger != nil {
				logger.Error("telegram persist offset", "offset", offset, "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
