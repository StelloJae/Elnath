package orchestrator

import (
	"context"
	"testing"

	"github.com/stello/elnath/internal/conversation"
	routingpref "github.com/stello/elnath/internal/routing"
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

func TestRouteHandlersRepresentativeDispatch(t *testing.T) {
	r := newTestRouter()
	tests := []struct {
		name   string
		intent conversation.Intent
		ctx    *RoutingContext
		want   string
	}{
		{
			name:   "question uses single handler",
			intent: conversation.IntentQuestion,
			want:   "single",
		},
		{
			name:   "complex task uses complex handler",
			intent: conversation.IntentComplexTask,
			ctx:    &RoutingContext{EstimatedFiles: 4},
			want:   "team",
		},
		{
			name:   "project uses project handler",
			intent: conversation.IntentProject,
			want:   "autopilot",
		},
		{
			name:   "research uses research handler",
			intent: conversation.IntentResearch,
			want:   "research",
		},
		{
			name:   "wiki query uses single handler",
			intent: conversation.IntentWikiQuery,
			want:   "single",
		},
		{
			name:   "chat uses single handler",
			intent: conversation.IntentChat,
			want:   "single",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := r.Route(tt.intent, tt.ctx, nil)
			if wf.Name() != tt.want {
				t.Fatalf("intent %q got %q, want %q", tt.intent, wf.Name(), tt.want)
			}
		})
	}
}

func TestRouteQuestion(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentQuestion, nil, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteSimpleTask(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentSimpleTask, nil, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteComplexTask(t *testing.T) {
	r := newTestRouter()
	// nil context → EstimatedFiles defaults to 0, which is < 4, so single
	// To trigger team we need EstimatedFiles >= 4
	wf := r.Route(conversation.IntentComplexTask, &RoutingContext{EstimatedFiles: 4}, nil)
	if wf.Name() != "team" {
		t.Errorf("got %q, want %q", wf.Name(), "team")
	}
}

func TestRouteComplexTaskSmall(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentComplexTask, &RoutingContext{EstimatedFiles: 3}, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteComplexTaskBrownfieldMultiFile(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentComplexTask, &RoutingContext{
		EstimatedFiles:   1,
		ExistingCode:     true,
		VerificationHint: true,
	}, nil)
	if wf.Name() != "ralph" {
		t.Errorf("got %q, want %q", wf.Name(), "ralph")
	}
}

func TestRouteProject(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentProject, nil, nil)
	if wf.Name() != "autopilot" {
		t.Errorf("got %q, want %q", wf.Name(), "autopilot")
	}
}

func TestRouteProjectWithExistingCodeFallsBackToTeam(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentProject, &RoutingContext{
		EstimatedFiles: 3,
		ExistingCode:   true,
	}, nil)
	if wf.Name() != "team" {
		t.Errorf("got %q, want %q", wf.Name(), "team")
	}
}

func TestRouteResearch(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentResearch, nil, nil)
	if wf.Name() != "research" {
		t.Errorf("got %q, want %q", wf.Name(), "research")
	}
}

func TestRouteWikiQuery(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentWikiQuery, nil, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteChat(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentChat, nil, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteUnknownIntentFallsBackToSingle(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.Intent("completely_unknown"), nil, nil)
	if wf.Name() != "single" {
		t.Errorf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteUnmappedKnownIntentFallsBackToSingle(t *testing.T) {
	r := newTestRouter()
	delete(routeHandlers, conversation.IntentChat)
	defer func() {
		routeHandlers[conversation.IntentChat] = routeSingle
	}()

	wf := r.Route(conversation.IntentChat, nil, nil)
	if wf.Name() != "single" {
		t.Fatalf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRoutePreferenceOverride(t *testing.T) {
	r := newTestRouter()
	pref := &routingpref.WorkflowPreference{
		PreferredWorkflows: map[string]string{"question": "research"},
	}

	wf := r.Route(conversation.IntentQuestion, nil, pref)
	if wf.Name() != "research" {
		t.Fatalf("got %q, want %q", wf.Name(), "research")
	}
}

func TestRoutePreferenceAvoidFallsBackToBaseWorkflow(t *testing.T) {
	r := newTestRouter()
	pref := &routingpref.WorkflowPreference{
		PreferredWorkflows: map[string]string{"question": "research"},
		AvoidWorkflows:     []string{"research"},
	}

	wf := r.Route(conversation.IntentQuestion, nil, pref)
	if wf.Name() != "single" {
		t.Fatalf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteAvoidedBaseWorkflowFallsBackToSingle(t *testing.T) {
	r := newTestRouter()
	pref := &routingpref.WorkflowPreference{AvoidWorkflows: []string{"team"}}

	wf := r.Route(conversation.IntentComplexTask, &RoutingContext{
		EstimatedFiles: 4,
	}, pref)
	if wf.Name() != "single" {
		t.Fatalf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRoutePreferenceUnknownWorkflowFallsBackToBaseWorkflow(t *testing.T) {
	r := newTestRouter()
	pref := &routingpref.WorkflowPreference{
		PreferredWorkflows: map[string]string{"question": "does-not-exist"},
	}

	wf := r.Route(conversation.IntentQuestion, nil, pref)
	if wf.Name() != "single" {
		t.Fatalf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteBenchmarkModeForcesSingle(t *testing.T) {
	r := newTestRouter()
	wf := r.Route(conversation.IntentComplexTask, &RoutingContext{
		EstimatedFiles: 4,
		ExistingCode:   true,
		BenchmarkMode:  true,
	}, nil)
	if wf.Name() != "single" {
		t.Fatalf("got %q, want %q", wf.Name(), "single")
	}
}

func TestRouteBenchmarkModeIgnoresPreferenceOverride(t *testing.T) {
	r := newTestRouter()
	pref := &routingpref.WorkflowPreference{
		PreferredWorkflows: map[string]string{"complex_task": "team"},
	}
	wf := r.Route(conversation.IntentComplexTask, &RoutingContext{
		EstimatedFiles: 4,
		BenchmarkMode:  true,
	}, pref)
	if wf.Name() != "single" {
		t.Fatalf("got %q, want %q", wf.Name(), "single")
	}
}
