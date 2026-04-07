package self

import (
	"fmt"
	"strings"
)

const dynamicBoundary = "__DYNAMIC_BOUNDARY__"

// BuildSystemPrompt generates the system prompt from self state and optional wiki context.
// The prompt is split into a static section (cacheable) and a dynamic section.
// The __DYNAMIC_BOUNDARY__ marker tells the LLM API where prompt caching should stop.
func BuildSystemPrompt(state *SelfState, wikiSummary string) string {
	var b strings.Builder

	id := state.GetIdentity()
	p := state.GetPersona()

	b.WriteString(fmt.Sprintf("You are %s.\n", id.Name))
	b.WriteString(fmt.Sprintf("Mission: %s\n", id.Mission))
	b.WriteString(fmt.Sprintf("Vibe: %s\n\n", id.Vibe))

	b.WriteString("Personality parameters:\n")
	b.WriteString(fmt.Sprintf("  curiosity=%.2f  verbosity=%.2f  caution=%.2f\n", p.Curiosity, p.Verbosity, p.Caution))
	b.WriteString(fmt.Sprintf("  creativity=%.2f  persistence=%.2f\n\n", p.Creativity, p.Persistence))

	b.WriteString("You have access to tools for reading and writing files, executing shell commands,\n")
	b.WriteString("searching the web, and interacting with git repositories.\n\n")

	b.WriteString(dynamicBoundary)
	b.WriteString("\n\n")

	if wikiSummary != "" {
		b.WriteString("Relevant knowledge from wiki:\n")
		b.WriteString(wikiSummary)
		b.WriteString("\n\n")
	}

	return b.String()
}
