package learning

type ComplexityGate struct {
	MinMessages     int
	RequireToolCall bool
}

var DefaultComplexityGate = ComplexityGate{MinMessages: 5, RequireToolCall: true}

func (g ComplexityGate) ShouldExtract(msgCount, toolCalls int) bool {
	if msgCount < g.MinMessages {
		return false
	}
	if g.RequireToolCall && toolCalls <= 0 {
		return false
	}
	return true
}
