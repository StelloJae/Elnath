package prompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/stello/elnath/internal/identity"
)

// IdentityNode renders self identity and persona fields without using the
// existing prompt builder helpers.
type IdentityNode struct {
	priority int
}

func NewIdentityNode(priority int) *IdentityNode {
	return &IdentityNode{priority: priority}
}

func (n *IdentityNode) Name() string {
	return "identity"
}

func (n *IdentityNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

// CacheBoundary classifies identity as stable: self-identity + persona
// parameters change only on explicit reconfiguration, not per call.
func (n *IdentityNode) CacheBoundary() CacheBoundary { return CacheBoundaryStable }

func (n *IdentityNode) Render(_ context.Context, state *RenderState) (string, error) {
	if n == nil || state == nil || state.Self == nil {
		return "", nil
	}

	id := state.Self.GetIdentity()
	p := state.Self.GetPersona()

	var b strings.Builder
	fmt.Fprintf(&b, "You are %s.\n", id.Name)
	fmt.Fprintf(&b, "Mission: %s\n", id.Mission)
	fmt.Fprintf(&b, "Vibe: %s\n\n", id.Vibe)
	b.WriteString("Personality parameters:\n")
	fmt.Fprintf(&b, "  curiosity=%.2f\n", p.Curiosity)
	fmt.Fprintf(&b, "  verbosity=%.2f\n", p.Verbosity)
	fmt.Fprintf(&b, "  caution=%.2f\n", p.Caution)
	fmt.Fprintf(&b, "  creativity=%.2f\n", p.Creativity)
	fmt.Fprintf(&b, "  persistence=%.2f", p.Persistence)

	if line := principalLine(state.Principal); line != "" {
		b.WriteString("\n\n")
		b.WriteString(line)
	}

	return strings.TrimSpace(b.String()), nil
}

func principalLine(p identity.Principal) string {
	if p.IsZero() {
		return ""
	}
	var parts []string
	if uid := strings.TrimSpace(p.UserID); uid != "" {
		parts = append(parts, fmt.Sprintf("user=%s", uid))
	}
	if surface := strings.TrimSpace(p.Surface); surface != "" {
		parts = append(parts, fmt.Sprintf("surface=%s", surface))
	}
	if proj := strings.TrimSpace(p.ProjectID); proj != "" {
		parts = append(parts, fmt.Sprintf("project=%s", proj))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Currently assisting: " + strings.Join(parts, ", ")
}
