package scheduler

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type rawTask struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	Prompt     string `yaml:"prompt"`
	Interval   string `yaml:"interval"`
	RunOnStart bool   `yaml:"run_on_start"`
	Enabled    *bool  `yaml:"enabled"`
	SessionID  string `yaml:"session_id"`
	Surface    string `yaml:"surface"`
}

type rawConfig struct {
	ScheduledTasks []rawTask `yaml:"scheduled_tasks"`
}

func LoadConfig(path string) ([]ScheduledTask, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if len(raw.ScheduledTasks) == 0 {
		return nil, nil
	}

	tasks := make([]ScheduledTask, 0, len(raw.ScheduledTasks))
	seen := make(map[string]bool, len(raw.ScheduledTasks))
	for _, item := range raw.ScheduledTasks {
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		if !enabled {
			continue
		}

		interval, err := time.ParseDuration(item.Interval)
		if err != nil {
			return nil, fmt.Errorf("scheduled task %q: parse interval: %w", item.Name, err)
		}

		task := ScheduledTask{
			Name:       item.Name,
			Type:       item.Type,
			Prompt:     item.Prompt,
			Interval:   interval,
			RunOnStart: item.RunOnStart,
			Enabled:    enabled,
			SessionID:  item.SessionID,
			Surface:    item.Surface,
		}
		if err := task.Validate(); err != nil {
			return nil, err
		}
		if seen[task.Name] {
			return nil, fmt.Errorf("scheduled task %q: duplicate name", task.Name)
		}
		seen[task.Name] = true
		tasks = append(tasks, task)
	}

	return tasks, nil
}
