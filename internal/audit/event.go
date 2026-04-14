package audit

import "time"

type EventType string

const (
	EventSecretDetected    EventType = "secret_detected"
	EventSecretRedacted    EventType = "secret_redacted"
	EventInjectionBlocked  EventType = "injection_blocked"
	EventPermissionDenied  EventType = "permission_denied"
	EventPermissionGranted EventType = "permission_granted"
	EventSkillExecuted     EventType = "skill_executed"
)

type Event struct {
	Timestamp time.Time `json:"timestamp"`
	Type      EventType `json:"type"`
	SessionID string    `json:"session_id,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	RuleID    string    `json:"rule_id,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}
