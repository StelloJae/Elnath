package prompt

import (
	"context"
	"errors"
	"strings"
	testing "testing"
)

type stubNode struct {
	name     string
	priority int
	body     string
	err      error
}

func (n stubNode) Name() string { return n.name }

func (n stubNode) Priority() int { return n.priority }

func (n stubNode) Render(context.Context, *RenderState) (string, error) {
	if n.err != nil {
		return "", n.err
	}
	return n.body, nil
}

func TestBuilderEmpty(t *testing.T) {
	t.Parallel()

	got, err := NewBuilder().Build(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if got != "" {
		t.Fatalf("Build = %q, want empty string", got)
	}
}

func TestBuilderSingleNode(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "one", priority: 10, body: "alpha"})

	got, err := b.Build(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if got != "alpha" {
		t.Fatalf("Build = %q, want %q", got, "alpha")
	}
}

func TestBuilderMultipleNodesOrdering(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "first", priority: 100, body: "alpha"})
	b.Register(stubNode{name: "second", priority: 1, body: "beta"})
	b.Register(stubNode{name: "third", priority: 50, body: "gamma"})

	got, err := b.Build(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	want := "alpha\n\nbeta\n\ngamma"
	if got != want {
		t.Fatalf("Build = %q, want %q", got, want)
	}
}

func TestBuilderRegistrationOrderIndependentOfPriority(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "low", priority: 1, body: "low"})
	b.Register(stubNode{name: "high", priority: 99, body: "high"})

	got, err := b.Build(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	want := "low\n\nhigh"
	if got != want {
		t.Fatalf("Build = %q, want %q", got, want)
	}
}

func TestBuilderBudgetDropLowestPriorityFirst(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "keep-high", priority: 9, body: strings.Repeat("a", 300)})
	b.Register(stubNode{name: "drop-lowest", priority: 1, body: strings.Repeat("b", 300)})
	b.Register(stubNode{name: "keep-mid", priority: 7, body: strings.Repeat("c", 300)})
	b.Register(stubNode{name: "drop-second", priority: 2, body: strings.Repeat("d", 300)})

	got, err := b.Build(context.Background(), &RenderState{TokenBudget: 800})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	want := strings.Repeat("a", 300) + "\n\n" + strings.Repeat("c", 300)
	if got != want {
		t.Fatalf("Build kept wrong nodes under budget")
	}
}

func TestBuilderBudgetKeepsHighestPriorityWhenAllExceed(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "low", priority: 1, body: strings.Repeat("a", 300)})
	b.Register(stubNode{name: "highest", priority: 10, body: strings.Repeat("b", 300)})
	b.Register(stubNode{name: "mid", priority: 5, body: strings.Repeat("c", 300)})

	got, err := b.Build(context.Background(), &RenderState{TokenBudget: 10})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	want := strings.Repeat("b", 300)
	if got != want {
		t.Fatalf("Build = %q, want only highest-priority node", got)
	}
}

func TestBuilderBudgetZeroNoEnforcement(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "one", priority: 1, body: strings.Repeat("a", 300)})
	b.Register(stubNode{name: "two", priority: 2, body: strings.Repeat("b", 300)})

	got, err := b.Build(context.Background(), &RenderState{TokenBudget: 0})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	want := strings.Repeat("a", 300) + "\n\n" + strings.Repeat("b", 300)
	if got != want {
		t.Fatalf("Build = %q, want %q", got, want)
	}
}

func TestBuilderNodeErrorPropagation(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	boom := errors.New("boom")
	b.Register(stubNode{name: "broken", priority: 1, err: boom})

	_, err := b.Build(context.Background(), &RenderState{})
	if err == nil {
		t.Fatal("Build error = nil, want non-nil")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Build error = %v, want wrapped boom", err)
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("Build error = %v, want node name", err)
	}
}

func TestBuilderEmptyRenderSkippedInJoin(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "one", priority: 1, body: "alpha"})
	b.Register(stubNode{name: "empty", priority: 2, body: ""})
	b.Register(stubNode{name: "two", priority: 3, body: "beta"})

	got, err := b.Build(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	want := "alpha\n\nbeta"
	if got != want {
		t.Fatalf("Build = %q, want %q", got, want)
	}
}

func TestBuilderDeterministic(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "one", priority: 3, body: "alpha"})
	b.Register(stubNode{name: "two", priority: 2, body: "beta"})

	state := &RenderState{SessionID: "sess-1", UserInput: "hello", TokenBudget: 1000}
	first, err := b.Build(context.Background(), state)
	if err != nil {
		t.Fatalf("first Build error: %v", err)
	}
	second, err := b.Build(context.Background(), state)
	if err != nil {
		t.Fatalf("second Build error: %v", err)
	}

	if first != second {
		t.Fatalf("Build outputs differ: %q != %q", first, second)
	}
}

func TestBuildContainsSingleBoundary(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "identity", priority: 100, body: "identity"})
	b.Register(NewDynamicBoundaryNode())
	b.Register(stubNode{name: "dynamic", priority: 10, body: "dynamic"})

	got, err := b.Build(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if strings.Count(got, dynamicBoundary) != 1 {
		t.Fatalf("Build = %q, want exactly one boundary", got)
	}
}

func TestBudgetDropNeverRemovesBoundary(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "low", priority: 1, body: strings.Repeat("a", 300)})
	b.Register(NewDynamicBoundaryNode())
	b.Register(stubNode{name: "mid", priority: 10, body: strings.Repeat("b", 300)})

	got, err := b.Build(context.Background(), &RenderState{TokenBudget: 10})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !strings.Contains(got, dynamicBoundary) {
		t.Fatalf("Build = %q, want boundary to remain", got)
	}
}

func TestBudgetDropNeverRemovesBoundaryWithCompetingHighPriority(t *testing.T) {
	t.Parallel()

	b := NewBuilder()
	b.Register(stubNode{name: "ultra-high", priority: 1000, body: strings.Repeat("x", 300)})
	b.Register(NewDynamicBoundaryNode())
	b.Register(stubNode{name: "low", priority: 1, body: strings.Repeat("y", 300)})

	got, err := b.Build(context.Background(), &RenderState{TokenBudget: 1})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	if !strings.Contains(got, dynamicBoundary) {
		t.Fatalf("Build = %q, want boundary to survive even with competing priority 1000 node", got)
	}
}
