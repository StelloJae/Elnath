package magicdocs

import "github.com/stello/elnath/internal/event"

type FilterResult struct {
	Signal  []event.Event
	Context []event.Event
}

func Filter(events []event.Event) FilterResult {
	var result FilterResult
	for _, e := range events {
		switch classify(e) {
		case pass:
			result.Signal = append(result.Signal, e)
		case context_:
			result.Context = append(result.Context, e)
		}
	}
	return result
}

func classify(e event.Event) classification {
	switch ev := e.(type) {
	case event.TextDeltaEvent:
		return drop
	case event.ToolUseStartEvent:
		return drop
	case event.ToolUseDeltaEvent:
		return drop
	case event.StreamDoneEvent:
		return drop
	case event.StreamErrorEvent:
		return drop
	case event.IterationStartEvent:
		return drop

	case event.ResearchProgressEvent:
		if ev.Phase == "conclusion" || ev.Phase == "synthesis" {
			return pass
		}
		return context_
	case event.HypothesisEvent:
		return pass
	case event.AgentFinishEvent:
		return pass
	case event.SkillExecuteEvent:
		if ev.Status == "done" {
			return pass
		}
		return context_
	case event.DaemonTaskEvent:
		if ev.Status == "done" {
			return pass
		}
		return context_

	case event.ToolUseDoneEvent:
		return context_
	case event.ToolProgressEvent:
		return context_
	case event.CompressionEvent:
		return context_
	case event.WorkflowProgressEvent:
		return context_
	case event.UsageProgressEvent:
		return context_
	case event.SessionResumeEvent:
		return context_
	case event.ClassifiedErrorEvent:
		return context_

	default:
		return drop
	}
}
