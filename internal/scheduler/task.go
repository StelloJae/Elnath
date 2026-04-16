package scheduler

import (
	"fmt"
	"strings"
	"time"
)

type ScheduledTask struct {
	Name       string
	Type       string
	Prompt     string
	Interval   time.Duration
	RunOnStart bool
	Enabled    bool
	SessionID  string
	Surface    string
}

func (t *ScheduledTask) Validate() error {
	t.Name = strings.TrimSpace(t.Name)
	t.Type = strings.TrimSpace(t.Type)
	t.Prompt = strings.TrimSpace(t.Prompt)
	t.SessionID = strings.TrimSpace(t.SessionID)
	t.Surface = strings.TrimSpace(t.Surface)

	if t.Name == "" {
		return fmt.Errorf("scheduled task name required")
	}
	if t.Prompt == "" {
		return fmt.Errorf("scheduled task %q: prompt required", t.Name)
	}
	if t.Interval < time.Minute {
		return fmt.Errorf("scheduled task %q: interval must be >= 1m (got %s)", t.Name, t.Interval)
	}
	if t.Type != "" && t.Type != "agent" && t.Type != "research" && t.Type != "skill-promote" {
		return fmt.Errorf("scheduled task %q: invalid type %q (must be agent, research, or skill-promote)", t.Name, t.Type)
	}
	return nil
}
