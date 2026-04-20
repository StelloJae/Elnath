package tools

import "context"

// sessionIDContextKey keys the active session id on a context. Workspace-aware
// tools (bash, file, git) read this to derive an isolated per-session working
// directory via PathGuard.EnsureSessionWorkDir, preventing cross-session
// artifact contamination observed in dogfood session 4 (FU-WorkspaceScope).
type sessionIDContextKey struct{}

// WithSessionID returns a derived context tagged with the given session id.
// An empty id is preserved as-is so downstream callers can detect "no
// session" and fall back to the root WorkDir.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
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
