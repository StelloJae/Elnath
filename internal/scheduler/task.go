package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

type ScheduledTask struct {
	Name            string
	Type            string
	Prompt          string
	Interval        time.Duration
	RunOnStart      bool
	Enabled         bool
	SessionID       string
	Surface         string
	DeliveryTargets []string
}

func (t *ScheduledTask) Validate() error {
	t.Name = strings.TrimSpace(t.Name)
	t.Type = strings.TrimSpace(t.Type)
	t.Prompt = strings.TrimSpace(t.Prompt)
	t.SessionID = strings.TrimSpace(t.SessionID)
	t.Surface = strings.TrimSpace(t.Surface)
	t.DeliveryTargets = normalizeScheduleDeliveryTargets(t.DeliveryTargets)

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
	if _, err := daemon.ParseDeliveryTargets(t.DeliveryTargets); err != nil {
		return fmt.Errorf("scheduled task %q: invalid delivery target: %w", t.Name, err)
	}
	return nil
}

func normalizeScheduleDeliveryTargets(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
