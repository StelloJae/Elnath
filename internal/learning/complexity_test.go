package learning

import "testing"

func TestComplexityGateShouldExtract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		gate              ComplexityGate
		messageCount      int
		toolCallCount     int
		wantShouldExtract bool
	}{
		{name: "default blocks short", gate: DefaultComplexityGate, messageCount: 3, toolCallCount: 5, wantShouldExtract: false},
		{name: "default blocks no tools", gate: DefaultComplexityGate, messageCount: 10, toolCallCount: 0, wantShouldExtract: false},
		{name: "default passes", gate: DefaultComplexityGate, messageCount: 5, toolCallCount: 1, wantShouldExtract: true},
		{name: "no tool requirement", gate: ComplexityGate{MinMessages: 5}, messageCount: 5, toolCallCount: 0, wantShouldExtract: true},
		{name: "boundary passes", gate: DefaultComplexityGate, messageCount: 5, toolCallCount: 1, wantShouldExtract: true},
		{name: "below boundary blocks", gate: DefaultComplexityGate, messageCount: 4, toolCallCount: 1, wantShouldExtract: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.gate.ShouldExtract(tt.messageCount, tt.toolCallCount); got != tt.wantShouldExtract {
				t.Fatalf("ShouldExtract(%d, %d) = %v, want %v", tt.messageCount, tt.toolCallCount, got, tt.wantShouldExtract)
			}
		})
	}
}
