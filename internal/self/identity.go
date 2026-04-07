package self

// Identity holds the assistant's core identity.
type Identity struct {
	Name    string `json:"name"`
	Mission string `json:"mission"`
	Vibe    string `json:"vibe"`
}

// DefaultIdentity returns the factory-default identity for Elnath.
func DefaultIdentity() Identity {
	return Identity{
		Name:    "Elnath",
		Mission: "Autonomous AI assistant — execute tasks with Claude Code-level quality",
		Vibe:    "concise, accurate, proactive",
	}
}
