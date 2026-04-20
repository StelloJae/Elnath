package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/stello/elnath/internal/config"
	"github.com/stello/elnath/internal/core"
)

var version = "dev"

func main() {
	defer recoverPanic()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args); err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "elnath: interrupted")
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "elnath: %s\n", core.FormatUserError(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cmd, cmdArgs := splitCommandArgs(args)
	if cmd == "" || cmd == "--help" || cmd == "-h" {
		return executeCommand(ctx, "help", nil)
	}
	return executeCommand(ctx, cmd, cmdArgs)
}

func extractConfigFlag(args []string) string {
	for i, arg := range args {
		if arg == "--config" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func extractPersonaFlag(args []string) string {
	for i, arg := range args {
		if arg == "--persona" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func extractDataDirFlag(args []string) string {
	return extractFlagValue(args, "--data-dir")
}

func extractSessionFlag(args []string) string {
	for i, arg := range args {
		if arg == "--session" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func extractFlagValue(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

// applyGlobalFlagOverrides mutates cfg based on process-wide CLI flags that
// are not tied to a specific subcommand. Currently handles --no-self-heal,
// which disables the Phase 0 reflection observer regardless of config.yaml.
func applyGlobalFlagOverrides(cfg *config.Config, args []string) {
	if cfg == nil {
		return
	}
	if hasFlag(args, "--no-self-heal") {
		cfg.SelfHealing.Enabled = false
	}
}

func splitCommandArgs(args []string) (string, []string) {
	if len(args) < 2 {
		return "", nil
	}
	flagsWithValue := map[string]struct{}{
		"--config":     {},
		"--data-dir":   {},
		"--persona":    {},
		"--session":    {},
		"--principal":  {},
		"--project-id": {},
	}
	booleanFlags := map[string]struct{}{
		"--continue":        {},
		"--non-interactive": {},
		"--no-self-heal":    {},
	}
	for i := 1; i < len(args); i++ {
		if _, ok := flagsWithValue[args[i]]; ok {
			i++
			continue
		}
		if _, ok := booleanFlags[args[i]]; ok {
			continue
		}
		return args[i], args[i+1:]
	}
	return "", nil
}

func recoverPanic() {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "elnath panic: %v\n%s\n", r, debug.Stack())
		os.Exit(2)
	}
}
