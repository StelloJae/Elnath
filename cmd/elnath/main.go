package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

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
	if len(args) < 2 {
		return executeCommand(ctx, "help", args)
	}

	configPath := extractConfigFlag(args)
	cmd := args[1]

	_ = configPath // passed to commands that need it
	return executeCommand(ctx, cmd, args[2:])
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

func recoverPanic() {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "elnath panic: %v\n%s\n", r, debug.Stack())
		os.Exit(2)
	}
}
