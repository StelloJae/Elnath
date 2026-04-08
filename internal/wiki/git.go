package wiki

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitSync tracks wiki directory changes in a local git repository.
type GitSync struct {
	dir    string
	logger *slog.Logger
}

// NewGitSync creates a GitSync rooted at wikiDir.
// If logger is nil, slog.Default() is used.
func NewGitSync(wikiDir string, logger *slog.Logger) *GitSync {
	if logger == nil {
		logger = slog.Default()
	}
	return &GitSync{dir: wikiDir, logger: logger}
}

// Init ensures wikiDir is a git repository, running git init if needed.
func (g *GitSync) Init() error {
	if _, err := os.Stat(filepath.Join(g.dir, ".git")); err == nil {
		return nil
	}
	if _, err := runGitCmd(g.dir, "init"); err != nil {
		return fmt.Errorf("wiki git: init: %w", err)
	}
	g.logger.Info("wiki git: initialized repository", "dir", g.dir)
	return nil
}

// Commit stages all changes and commits them with msg.
// If there is nothing to commit, it returns nil without creating a commit.
func (g *GitSync) Commit(msg string) error {
	if _, err := runGitCmd(g.dir, "add", "-A"); err != nil {
		return fmt.Errorf("wiki git: add: %w", err)
	}

	status, err := runGitCmd(g.dir, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("wiki git: status: %w", err)
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}

	if _, err := runGitCmd(g.dir, "commit", "-m", msg); err != nil {
		return fmt.Errorf("wiki git: commit: %w", err)
	}
	g.logger.Info("wiki git: committed", "msg", msg)
	return nil
}

// runGitCmd runs a git subcommand in dir and returns combined output.
func runGitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=elnath",
		"GIT_AUTHOR_EMAIL=elnath@local",
		"GIT_COMMITTER_NAME=elnath",
		"GIT_COMMITTER_EMAIL=elnath@local",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, buf.String())
	}
	return buf.String(), nil
}
