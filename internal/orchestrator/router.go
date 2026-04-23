package orchestrator

import (
	"github.com/stello/elnath/internal/conversation"
	routingpref "github.com/stello/elnath/internal/routing"
)

// RoutingContext carries heuristic signals that the Router uses when the
// intent alone is insufficient to pick a workflow.
type RoutingContext struct {
	ProjectID string
	// EstimatedFiles is a rough count of files the task is expected to touch.
	// Values >= 4 trigger the team workflow for complex_task intents.
	EstimatedFiles int
	// ExistingCode indicates the task is clearly about changing an existing codebase.
	ExistingCode bool
	// VerificationHint indicates the task explicitly mentions running tests, regressions, or validation.
	VerificationHint bool
	BenchmarkMode    bool
	// ExplicitWorkflow, when non-empty, forces the router to that workflow
	// regardless of intent/preference. Values: "single", "ralph", "team".
	// Power-user escape hatch via --workflow=NAME flag or "[NAME] ..." prompt
	// prefix. BenchmarkMode still wins when true.
	ExplicitWorkflow string
}

// Router maps a classified Intent to the appropriate Workflow.
type Router struct {
	workflows map[string]Workflow
}

type routeHandler func(*RoutingContext) string

var routeHandlers = map[conversation.Intent]routeHandler{
	conversation.IntentComplexTask: routeComplexTask,
	conversation.IntentProject:     routeProject,
	conversation.IntentResearch:    routeResearch,
	conversation.IntentWikiQuery:   routeSingle,
	conversation.IntentQuestion:    routeSingle,
	conversation.IntentSimpleTask:  routeSingle,
	conversation.IntentUnclear:     routeSingle,
	conversation.IntentChat:        routeSingle,
}

// NewRouter creates a Router with the provided workflow map.
// Expected keys: "single", "team", "autopilot", "ralph".
// Missing keys cause the router to fall back to "single" gracefully.
func NewRouter(workflows map[string]Workflow) *Router {
	return &Router{workflows: workflows}
}

// Route selects a Workflow based on the intent and routing context.
//
// Routing table:
//
//	question      -> single  (direct answer, no tools needed)
//	simple_task   -> single
//	complex_task  -> team    (unless EstimatedFiles < 4, then single)
//	project       -> autopilot
//	research      -> research (autoresearch loop)
//	unclear       -> single  (with clarification prompt injected by caller)
//	chat          -> single  (no tools)
func (r *Router) Route(intent conversation.Intent, ctx *RoutingContext, pref *routingpref.WorkflowPreference) Workflow {
	if ctx != nil && ctx.BenchmarkMode {
		return r.get("single")
	}
	if ctx != nil && ctx.ExplicitWorkflow != "" {
		return r.get(ctx.ExplicitWorkflow)
	}
	base := r.routeName(intent, ctx)
	if preferred := pref.PreferredWorkflow(string(intent)); preferred != "" && !pref.Avoids(preferred) {
		if wf, ok := r.workflows[preferred]; ok {
			return wf
		}
	}
	if pref.Avoids(base) {
		if base != "single" && !pref.Avoids("single") {
			return r.get("single")
		}
	}
	return r.get(base)
}

func (r *Router) routeName(intent conversation.Intent, ctx *RoutingContext) string {
	if ctx != nil && ctx.BenchmarkMode {
		return "single"
	}
	if handler, ok := routeHandlers[intent]; ok {
		return handler(ctx)
	}
	return "single"
}

// TODO(phase-8-1b): replace phrase-matching with LLM-based intent classifier (Haiku).
func routeComplexTask(ctx *RoutingContext) string {
	if ctx != nil && ctx.ExistingCode && ctx.VerificationHint {
		return "ralph"
	}
	// GPT G2 (Phase 8.1a): threshold raised from >= 1 to >= 4 so small
	// existing-code tasks (e.g., "Add --json flag to cmd/mytool/status.go")
	// stay on single path instead of fanning out to team.
	if ctx != nil && ctx.ExistingCode && ctx.EstimatedFiles >= 4 {
		return "team"
	}
	if ctx != nil && ctx.EstimatedFiles < 4 {
		return "single"
	}
	return "team"
}

func routeProject(ctx *RoutingContext) string {
	if ctx != nil && ctx.ExistingCode {
		return "team"
	}
	return "autopilot"
}

func routeResearch(*RoutingContext) string {
	return "research"
}

func routeSingle(*RoutingContext) string {
	return "single"
}

// get returns the named workflow, falling back to "single" if not found.
// If "single" is also absent it returns nil - callers must guard against this.
func (r *Router) get(name string) Workflow {
	if wf, ok := r.workflows[name]; ok {
		return wf
	}
	if wf, ok := r.workflows["single"]; ok {
		return wf
	}
	return nil
}
