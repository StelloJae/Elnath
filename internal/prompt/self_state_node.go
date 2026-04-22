package prompt

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type SelfStateNode struct {
	priority int
}

func NewSelfStateNode(priority int) *SelfStateNode {
	return &SelfStateNode{priority: priority}
}

func (n *SelfStateNode) Name() string {
	return "self_state"
}

// CacheBoundary classifies self-state as volatile: the SelfState
// snapshot is re-read every turn.
func (n *SelfStateNode) CacheBoundary() CacheBoundary { return CacheBoundaryVolatile }

func (n *SelfStateNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *SelfStateNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil {
		return "", nil
	}

	sessionID := strings.TrimSpace(state.SessionID)
	if sessionID == "" {
		sessionID = "(new)"
	}

	mode := "interactive"
	if state.DaemonMode {
		mode = "daemon"
	}

	workDir := strings.TrimSpace(state.SessionWorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(state.WorkDir)
	}
	if workDir == "" {
		workDir = "(none)"
	}

	return fmt.Sprintf(
		"Operational state:\n- Session: %s\n- Messages in conversation: %d\n- Mode: %s\n- Working directory: %s\n- Current time: %s",
		sessionID,
		state.MessageCount,
		mode,
		workDir,
		time.Now().UTC().Format(time.RFC3339),
	), nil
}
