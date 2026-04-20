package prompt

import (
	"context"
	"strings"
	testing "testing"

	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/self"
)

func TestIdentityNodeNilState(t *testing.T) {
	t.Parallel()

	got, err := NewIdentityNode(10).Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestIdentityNodeNilSelf(t *testing.T) {
	t.Parallel()

	got, err := NewIdentityNode(10).Render(context.Background(), &RenderState{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if got != "" {
		t.Fatalf("Render = %q, want empty string", got)
	}
}

func TestIdentityNodeRendersIdentity(t *testing.T) {
	t.Parallel()

	node := NewIdentityNode(10)
	state := &RenderState{
		Self: &self.SelfState{
			Identity: self.Identity{
				Name:    "Elnath",
				Mission: "Build reliable systems",
				Vibe:    "calm and exact",
			},
			Persona: self.Persona{
				Curiosity:   0.6,
				Verbosity:   0.4,
				Caution:     0.8,
				Creativity:  0.5,
				Persistence: 0.9,
			},
		},
	}

	got, err := node.Render(context.Background(), state)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	wants := []string{
		"You are Elnath.",
		"Mission: Build reliable systems",
		"Vibe: calm and exact",
		"curiosity=0.60",
		"verbosity=0.40",
		"caution=0.80",
		"creativity=0.50",
		"persistence=0.90",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q", got, want)
		}
	}
}

func TestIdentityNodePriorityReturned(t *testing.T) {
	t.Parallel()

	if got := NewIdentityNode(42).Priority(); got != 42 {
		t.Fatalf("Priority = %d, want 42", got)
	}
}

func TestIdentityNodeIncludesPrincipalWhenPresent(t *testing.T) {
	t.Parallel()

	node := NewIdentityNode(10)
	state := &RenderState{
		Self: &self.SelfState{
			Identity: self.Identity{Name: "Elnath", Mission: "m", Vibe: "v"},
		},
		Principal: identity.NewPrincipal(identity.PrincipalSource{
			UserID:    "jay",
			ProjectID: "elnath",
			Surface:   "telegram",
		}),
	}

	got, err := node.Render(context.Background(), state)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	wants := []string{"jay", "telegram", "elnath"}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("Render = %q, want substring %q (principal should be surfaced)", got, want)
		}
	}
}

func TestIdentityNodeSkipsPrincipalWhenZero(t *testing.T) {
	t.Parallel()

	node := NewIdentityNode(10)
	state := &RenderState{
		Self: &self.SelfState{
			Identity: self.Identity{Name: "Elnath", Mission: "m", Vibe: "v"},
		},
		Principal: identity.Principal{},
	}

	got, err := node.Render(context.Background(), state)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	if strings.Contains(strings.ToLower(got), "currently assisting") {
		t.Fatalf("Render = %q, should not mention assisting when principal is zero", got)
	}
}
