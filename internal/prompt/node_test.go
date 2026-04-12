package prompt

import (
	"context"
	"strings"
	testing "testing"

	"github.com/stello/elnath/internal/self"
)

func TestRenderStateNilSafe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := &RenderState{}
	nodes := []Node{
		NewIdentityNode(10),
		NewSessionSummaryNode(20, 5, 200),
		NewWikiRAGNode(30, 3),
	}

	for _, node := range nodes {
		got, err := node.Render(ctx, state)
		if err != nil {
			t.Fatalf("%s Render error: %v", node.Name(), err)
		}
		if got != "" {
			t.Fatalf("%s Render = %q, want empty string", node.Name(), got)
		}
	}
}

func TestRenderStatePartialNilSafe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := &RenderState{
		SessionID: "sess-1",
		UserInput: "where is the wiki context",
		Self: &self.SelfState{
			Identity: self.Identity{
				Name:    "Elnath",
				Mission: "Build reliable systems",
				Vibe:    "calm",
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

	identity, err := NewIdentityNode(10).Render(ctx, state)
	if err != nil {
		t.Fatalf("identity Render error: %v", err)
	}
	if identity == "" {
		t.Fatal("identity Render returned empty string")
	}
	if !strings.Contains(identity, "Elnath") {
		t.Fatalf("identity Render = %q, want identity content", identity)
	}

	summary, err := NewSessionSummaryNode(20, 5, 200).Render(ctx, state)
	if err != nil {
		t.Fatalf("session summary Render error: %v", err)
	}
	if summary != "" {
		t.Fatalf("session summary Render = %q, want empty string", summary)
	}

	rag, err := NewWikiRAGNode(30, 3).Render(ctx, state)
	if err != nil {
		t.Fatalf("wiki rag Render error: %v", err)
	}
	if rag != "" {
		t.Fatalf("wiki rag Render = %q, want empty string", rag)
	}
}
