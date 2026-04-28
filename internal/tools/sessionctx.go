package tools

import "context"

// sessionIDContextKey keys the active session id on a context. Workspace-aware
// tools (bash, file, git) read this to derive an isolated per-session working
// directory via PathGuard.EnsureSessionWorkDir, preventing cross-session
// artifact contamination observed in dogfood session 4 (FU-WorkspaceScope).
type sessionIDContextKey struct{}
type rootSessionWorkDirContextKey struct{}

// WithSessionID returns a derived context tagged with the given session id.
// An empty id is preserved as-is so downstream callers can detect "no
// session" and fall back to the root WorkDir.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
}

// WithRootSessionWorkDir marks ctx so workspace-aware tools use the guard's
// root WorkDir as the session boundary while preserving the actual session id.
// Benchmark runs use this to operate directly in the cloned target repo
// without falling back to legacy unscoped path resolution.
func WithRootSessionWorkDir(ctx context.Context) context.Context {
	return context.WithValue(ctx, rootSessionWorkDirContextKey{}, true)
}

// SessionIDFrom returns the session id stored on ctx, or "" when no session
// is bound. The empty value intentionally maps to root-WorkDir behavior in
// EnsureSessionWorkDir, preserving legacy callers that never set the key.
func SessionIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(sessionIDContextKey{}).(string); ok {
		return v
	}
	return ""
}

// RootSessionWorkDirFrom reports whether ctx should use the guard root as
// the active session workspace boundary.
func RootSessionWorkDirFrom(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(rootSessionWorkDirContextKey{}).(bool)
	return v
}

// SessionWorkDirFromContext returns the active workspace boundary for tools.
// Normal sessions use <root>/sessions/<session-id>. Root-scoped benchmark
// sessions use the guard root while still keeping SessionIDFrom(ctx)
// available for attribution and per-run proxy/audit state.
func SessionWorkDirFromContext(ctx context.Context, guard *PathGuard) (string, error) {
	if RootSessionWorkDirFrom(ctx) {
		return guard.WorkDir(), nil
	}
	return guard.EnsureSessionWorkDir(SessionIDFrom(ctx))
}

func sessionScopedPathsEnabled(ctx context.Context) bool {
	return RootSessionWorkDirFrom(ctx) || SessionIDFrom(ctx) != ""
}
