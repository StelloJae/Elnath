package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stello/elnath/internal/userfacingerr"
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

// sessionWorkDirSubdir is the subdirectory under the root WorkDir that holds
// per-session workspaces. Keeping sessions under a dedicated subdir keeps the
// root cleanly separable from legacy files and per-project artifacts.
const sessionWorkDirSubdir = "sessions"

// SessionWorkDir returns the workspace path for a given session. An empty
// sessionID falls back to the root WorkDir, preserving legacy callers. The
// session id is sanitized so a malicious id cannot escape the root.
//
// This is a pure path computation; use EnsureSessionWorkDir when the directory
// must exist on disk.
func (g *PathGuard) SessionWorkDir(sessionID string) string {
	if sessionID == "" {
		return g.workDir
	}
	return filepath.Join(g.workDir, sessionWorkDirSubdir, sanitizeSessionID(sessionID))
}

// EnsureSessionWorkDir returns the session workspace path and creates the
// directory (with parents) if it does not yet exist. An empty sessionID
// returns the root WorkDir without touching the filesystem.
func (g *PathGuard) EnsureSessionWorkDir(sessionID string) (string, error) {
	dir := g.SessionWorkDir(sessionID)
	if sessionID == "" {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create session workspace %q: %w", dir, err)
	}
	return dir, nil
}

// PurgeSessionWorkDir removes the per-session subdir and its contents. The
// call is idempotent: empty session ids and missing directories are no-ops.
// As a safety net the resolved path must live under <root>/sessions/; any
// resolved path that escapes (e.g. via a malformed sanitize result) is
// refused so a stray sid can never wipe the project root.
func (g *PathGuard) PurgeSessionWorkDir(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	dir := g.SessionWorkDir(sessionID)
	sessionsRoot := filepath.Join(g.workDir, sessionWorkDirSubdir)
	rel, err := filepath.Rel(sessionsRoot, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing to purge %q: outside session root %q", dir, sessionsRoot)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("purge session workspace %q: %w", dir, err)
	}
	return nil
}

// sanitizeSessionID strips path separators and traversal segments so the
// returned id is always a single, safe directory name.
func sanitizeSessionID(sessionID string) string {
	cleaned := filepath.Base(sessionID)
	cleaned = strings.ReplaceAll(cleaned, "..", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || cleaned == "." {
		return "_invalid"
	}
	return cleaned
}

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

// pathWithin reports whether candidate is located at or under root.
// Both paths MUST be cleaned, absolute, and canonicalized via EvalSymlinks
// before being passed here — this helper does only the containment check
// using filepath.Rel semantics, so prefix-leak bugs such as "/tmp/root2"
// matching "/tmp/root" do not slip through.
func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ResolveWorkingDir resolves rawPath against sessionDir and verifies the
// result lies within the session workspace after symlink resolution. The
// target must already exist and be a directory. Used by tools (bash) that
// accept a caller-supplied working directory as a security boundary rather
// than a lock hint; read-only tools may still use ResolveIn.
func (g *PathGuard) ResolveWorkingDir(sessionDir, rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("empty path")
	}

	p := expandHome(g.homeDir, rawPath)
	if !filepath.IsAbs(p) {
		p = filepath.Join(sessionDir, p)
	}
	cleaned := filepath.Clean(p)

	rootReal, err := filepath.EvalSymlinks(sessionDir)
	if err != nil {
		return "", fmt.Errorf("resolve session root: %w", err)
	}
	candidateReal, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", fmt.Errorf("working directory does not exist or is not resolvable: %w", err)
	}

	info, err := os.Stat(candidateReal)
	if err != nil {
		return "", fmt.Errorf("stat working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working_dir is not a directory")
	}

	if !pathWithin(rootReal, candidateReal) {
		return "", fmt.Errorf("working_dir escapes session root")
	}

	return candidateReal, nil
}

// CheckWrite returns an error if absPath falls under a protected directory.
func (g *PathGuard) CheckWrite(absPath string) error {
	cleaned := filepath.Clean(absPath)
	for _, pp := range g.protectedPaths {
		if cleaned == pp || strings.HasPrefix(cleaned, pp+string(filepath.Separator)) {
			inner := fmt.Errorf("write denied: %q is under protected path %q", absPath, pp)
			return userfacingerr.Wrap(userfacingerr.ELN020, inner, "path guard")
		}
	}
	return nil
}

// CheckScope validates that every write path in scope is allowed under the
// guard's protected-path rules. Read paths are not checked.
func (g *PathGuard) CheckScope(scope ToolScope) error {
	for _, p := range scope.WritePaths {
		if err := g.CheckWrite(p); err != nil {
			return err
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
