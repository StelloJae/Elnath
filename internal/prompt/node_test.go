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
		NewPersonaNode(40),
		NewBrownfieldNode(50),
		NewProjectContextNode(60),
		NewModelGuidanceNode(70),
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

// TestCacheBoundaryClassification pins the plan §5 step 8.1.5 cache
// posture for every node in the registry. Plan's canonical split:
//   - 7 stable: identity, persona, locale, model_guidance, boundary,
//     brownfield, greenfield.
//   - 12 volatile: chat_system, chat_tool_guide, context_files, lessons,
//     memory_context, project_context, self_state, session_summary,
//     skill_catalog, skill_guidance, tool_catalog, wiki_rag.
//
// A future contributor adding a 20th node must update this table so the
// classification review is explicit rather than silent.
func TestCacheBoundaryClassification(t *testing.T) {
	t.Parallel()

	stable := []Node{
		NewIdentityNode(10),
		NewPersonaNode(10),
		&LocaleInstructionNode{},
		NewModelGuidanceNode(10),
		NewDynamicBoundaryNode(),
		NewBrownfieldNode(10),
		NewGreenfieldNode(10),
	}
	volatile := []Node{
		NewChatSystemPromptNode(10),
		NewChatToolGuideNode(10),
		NewContextFilesNode(10),
		NewLessonsNode(10, nil, 0, 0),
		NewMemoryContextNode(10, 0, 0),
		NewProjectContextNode(10),
		NewSelfStateNode(10),
		NewSessionSummaryNode(10, 0, 0),
		NewSkillCatalogNode(10, nil),
		NewSkillGuidanceNode(10),
		NewToolCatalogNode(10),
		NewWikiRAGNode(10, 0),
	}

	for _, n := range stable {
		if got := n.CacheBoundary(); got != CacheBoundaryStable {
			t.Errorf("%s CacheBoundary = %s, want stable", n.Name(), got)
		}
	}
	for _, n := range volatile {
		if got := n.CacheBoundary(); got != CacheBoundaryVolatile {
			t.Errorf("%s CacheBoundary = %s, want volatile", n.Name(), got)
		}
	}

	if got := len(stable) + len(volatile); got != 19 {
		t.Errorf("classification set size = %d, want 19 (one row per *_node.go)", got)
	}
}

// TestSystemPromptDynamicBoundaryStable pins the exported boundary-marker
// constant so callers reading it from the public API get the same string
// the builder emits.
func TestSystemPromptDynamicBoundaryStable(t *testing.T) {
	t.Parallel()
	if SystemPromptDynamicBoundary == "" {
		t.Fatal("SystemPromptDynamicBoundary must not be empty")
	}
	if SystemPromptDynamicBoundary != dynamicBoundary {
		t.Errorf("SystemPromptDynamicBoundary = %q, want alias of dynamicBoundary (%q)", SystemPromptDynamicBoundary, dynamicBoundary)
	}
}
