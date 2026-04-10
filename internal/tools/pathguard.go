package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathGuard resolves tool paths and enforces write-deny rules.
// Read operations are unrestricted. Write operations are blocked
// for paths under any protected directory.
type PathGuard struct {
	workDir        string
	protectedPaths []string
	homeDir        string
}

// NewPathGuard creates a PathGuard with the given working directory
// and write-protected paths. Protected paths are expanded and cleaned.
func NewPathGuard(workDir string, protectedPaths []string) *PathGuard {
	home, _ := os.UserHomeDir()
	cleaned := make([]string, 0, len(protectedPaths))
	for _, p := range protectedPaths {
		p = expandHome(home, p)
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		cleaned = append(cleaned, filepath.Clean(p))
	}
	return &PathGuard{
		workDir:        workDir,
		protectedPaths: cleaned,
		homeDir:        home,
	}
}

// WorkDir returns the guard's default working directory.
func (g *PathGuard) WorkDir() string { return g.workDir }

// Resolve expands ~ and converts rawPath to an absolute, cleaned path.
// Relative paths are resolved against the guard's working directory.
func (g *PathGuard) Resolve(rawPath string) (string, error) {
	return g.ResolveIn(g.workDir, rawPath)
}

// ResolveIn resolves rawPath against an explicit base directory.
func (g *PathGuard) ResolveIn(cwd, rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("empty path")
	}
	p := expandHome(g.homeDir, rawPath)
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p), nil
}

// CheckWrite returns an error if absPath falls under a protected directory.
func (g *PathGuard) CheckWrite(absPath string) error {
	cleaned := filepath.Clean(absPath)
	for _, pp := range g.protectedPaths {
		if cleaned == pp || strings.HasPrefix(cleaned, pp+string(filepath.Separator)) {
			return fmt.Errorf("write denied: %q is under protected path %q", absPath, pp)
		}
	}
	return nil
}

func expandHome(home, p string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
