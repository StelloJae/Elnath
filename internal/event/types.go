package event

import "github.com/stello/elnath/internal/llm"

// LLM stream events

type TextDeltaEvent struct {
	Base
	Content string
}

func (e TextDeltaEvent) EventType() string { return "text_delta" }

type ToolUseStartEvent struct {
	Base
	ID   string
	Name string
}

func (e ToolUseStartEvent) EventType() string { return "tool_use_start" }

type ToolUseDeltaEvent struct {
	Base
	ID    string
	Input string
}

func (e ToolUseDeltaEvent) EventType() string { return "tool_use_delta" }

type ToolUseDoneEvent struct {
	Base
	ID    string
	Name  string
	Input string
}

func (e ToolUseDoneEvent) EventType() string { return "tool_use_done" }

type StreamDoneEvent struct {
	Base
	Usage llm.UsageStats
}

func (e StreamDoneEvent) EventType() string { return "stream_done" }

type StreamErrorEvent struct {
	Base
	Err error
}

func (e StreamErrorEvent) EventType() string { return "stream_error" }

// Agent lifecycle events

type IterationStartEvent struct {
	Base
	Iteration int
	Max       int
}

func (e IterationStartEvent) EventType() string { return "iteration_start" }

type CompressionEvent struct {
	Base
	BeforeCount int
	AfterCount  int
}

func (e CompressionEvent) EventType() string { return "compression" }

type ClassifiedErrorEvent struct {
	Base
	Classification string
	Err            error
}

func (e ClassifiedErrorEvent) EventType() string { return "classified_error" }

type AgentFinishEvent struct {
	Base
	FinishReason string
	Usage        llm.UsageStats
}

func (e AgentFinishEvent) EventType() string { return "agent_finish" }

// Progress events (replaces daemon.ProgressEvent)

type ToolProgressEvent struct {
	Base
	ToolName string
	Preview  string
}

func (e ToolProgressEvent) EventType() string { return "tool_progress" }

type WorkflowProgressEvent struct {
	Base
	Intent   string
	Workflow string
}

func (e WorkflowProgressEvent) EventType() string { return "workflow_progress" }

type UsageProgressEvent struct {
	Base
	Summary string
}

func (e UsageProgressEvent) EventType() string { return "usage_progress" }

// Research events

type ResearchProgressEvent struct {
	Base
	Phase   string
	Round   int
	Message string
}

func (e ResearchProgressEvent) EventType() string { return "research_progress" }

type HypothesisEvent struct {
	Base
	HypothesisID string
	Statement    string
	Status       string
}

func (e HypothesisEvent) EventType() string { return "hypothesis" }

// Skill events

type SkillExecuteEvent struct {
	Base
	SkillName string
	Status    string
}

func (e SkillExecuteEvent) EventType() string { return "skill_execute" }

// Session events

type SessionResumeEvent struct {
	Base
	SID     string
	Surface string
}

func (e SessionResumeEvent) EventType() string { return "session_resume" }

// Daemon events

type DaemonTaskEvent struct {
	Base
	TaskID string
	Status string
}

func (e DaemonTaskEvent) EventType() string { return "daemon_task" }
