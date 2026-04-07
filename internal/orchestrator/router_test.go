package orchestrator

import (
	"context"
	"testing"

	"github.com/stello/elnath/internal/conversation"
)

type mockWorkflow struct{ name string }

func (m *mockWorkflow) Name() string { return m.name }
func (m *mockWorkflow) Run(_ context.Context, _ WorkflowInput) (*WorkflowResult, error) {
	return &WorkflowResult{Workflow: m.name}, nil
}

func newTestRouter() *Router {
	return NewRouter(map[string]Workflow{
		"single":    &mockWorkflow{name: "single"},
		"team":      &mockWorkflow{name: "team"},
		"autopilot": &mockWorkflow{name: "autopilot"},
		"ralph":     &mockWorkflow{name: "ralph"},
		"research":  &mockWorkflow{name: "research"},
	})
}

func TestRouteQuestion(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentQuestion, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteSimpleTask(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentSimpleTask, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteComplexTask(t *testing.T) {
	r := newTestRouter()
	// nil context → EstimatedFiles defaults to 0, which is < 4, so single
	// To trigger team we need EstimatedFiles >= 4
	wf := r.Route(conversation.IntentComplexTask, &RoutingContext{EstimatedFiles: 4})
	if wf.Name() != "team" {
		t.Errorf("got %q, want %q", wf.Name(), "team")
	}
}

func TestRouteComplexTaskSmall(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentComplexTask, &RoutingContext{EstimatedFiles: 3})
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteProject(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentProject, nil)
	if wf.Name() != "autopilot" {
		t.Errorf("got %q, want %q", wf.Name(), "autopilot")
	}
}

func TestRouteResearch(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentResearch, nil)
	if wf.Name() != "research" {
		t.Errorf("got %q, want %q", wf.Name(), "research")
	}
}

func TestRouteWikiQuery(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentWikiQuery, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteChat(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentChat, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteUnknown(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.Intent("completely_unknown"), nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}
