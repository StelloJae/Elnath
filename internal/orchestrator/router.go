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
	// VerificationHint indicates the task explicitly mentions tests, regressions, or validation.
	VerificationHint bool
}

// Router maps a classified Intent to the appropriate Workflow.
type Router struct {
	workflows map[string]Workflow
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
	switch intent {
	case conversation.IntentComplexTask:
		if ctx != nil && ctx.ExistingCode && ctx.VerificationHint {
			return "ralph"
		}
		if ctx != nil && ctx.ExistingCode && ctx.EstimatedFiles >= 1 {
			return "team"
		}
		if ctx != nil && ctx.EstimatedFiles < 4 {
			return "single"
		}
		return "team"

	case conversation.IntentProject:
		if ctx != nil && ctx.ExistingCode {
			return "team"
		}
		return "autopilot"

	case conversation.IntentResearch:
		return "research"

	case conversation.IntentWikiQuery:
		return "single"

	case conversation.IntentQuestion,
		conversation.IntentSimpleTask,
		conversation.IntentUnclear,
		conversation.IntentChat:
		return "single"

	default:
		return "single"
	}
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
