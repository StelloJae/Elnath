package prompt

import "context"

type SkillGuidanceNode struct {
	priority int
}

func NewSkillGuidanceNode(priority int) *SkillGuidanceNode {
	return &SkillGuidanceNode{priority: priority}
}

func (n *SkillGuidanceNode) Name() string { return "skill_guidance" }

// CacheBoundary classifies skill guidance as volatile: it depends on
// the session's active skill catalog.
func (n *SkillGuidanceNode) CacheBoundary() CacheBoundary { return CacheBoundaryVolatile }

func (n *SkillGuidanceNode) Priority() int {
	if n == nil {
		return 0
	}
	return n.priority
}

func (n *SkillGuidanceNode) Render(_ context.Context, state *RenderState) (string, error) {
	if state != nil && state.BenchmarkMode {
		return "", nil
	}
	return `You have a skill tool for invoking available skills and a create_skill tool for creating reusable skills.

Use the skill tool when an available skill matches the user's request.
Use create_skill when:
- You notice a repeated pattern across sessions
- The user says "make this a skill" or similar
- A multi-step workflow could be reusable

When suggesting a skill, briefly explain what it would do before creating it.
Do not suggest skills for one-time tasks.`, nil
}
