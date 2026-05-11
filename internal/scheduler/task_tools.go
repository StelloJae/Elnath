package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stello/elnath/internal/tools"
	"gopkg.in/yaml.v3"
)

const (
	ScheduleCreateToolName = "schedule_create"
	ScheduleListToolName   = "schedule_list"
	ScheduleDeleteToolName = "schedule_delete"
)

type scheduleConfigStore struct {
	path string
}

func newScheduleConfigStore(path string) *scheduleConfigStore {
	return &scheduleConfigStore{path: path}
}

func (s *scheduleConfigStore) read() (rawConfig, error) {
	var cfg rawConfig
	if s == nil || strings.TrimSpace(s.path) == "" {
		return cfg, fmt.Errorf("schedule config path is empty")
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return cfg, nil
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *scheduleConfigStore) write(cfg rawConfig) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("schedule config path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

type ScheduleCreateTool struct {
	store *scheduleConfigStore
}

func NewScheduleCreateTool(path string) *ScheduleCreateTool {
	return &ScheduleCreateTool{store: newScheduleConfigStore(path)}
}

func (t *ScheduleCreateTool) Name() string { return ScheduleCreateToolName }

func (t *ScheduleCreateTool) Description() string {
	return "Create a static scheduled daemon task in scheduled_tasks.yaml"
}

func (t *ScheduleCreateTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"name":         tools.String("Unique scheduled task name."),
		"prompt":       tools.String("Prompt to enqueue when the schedule fires."),
		"interval":     tools.String("Go duration interval, minimum 1m, for example 15m or 24h."),
		"type":         tools.StringEnum("Optional task type. Defaults to agent.", "agent", "research", "skill-promote"),
		"run_on_start": tools.Bool("Whether to enqueue once when the daemon scheduler starts."),
		"enabled":      tools.Bool("Whether the task is enabled. Defaults to true."),
		"session_id":   tools.String("Optional session id to continue."),
		"surface":      tools.String("Optional originating surface label."),
	}, []string{"name", "prompt", "interval"})
}

func (t *ScheduleCreateTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *ScheduleCreateTool) Reversible() bool { return false }

func (t *ScheduleCreateTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *ScheduleCreateTool) ShouldCancelSiblingsOnError() bool { return false }

type scheduleCreateToolInput struct {
	Name       string `json:"name"`
	Prompt     string `json:"prompt"`
	Interval   string `json:"interval"`
	Type       string `json:"type"`
	RunOnStart bool   `json:"run_on_start"`
	Enabled    *bool  `json:"enabled"`
	SessionID  string `json:"session_id"`
	Surface    string `json:"surface"`
}

type scheduleToolItem struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Prompt     string `json:"prompt"`
	Interval   string `json:"interval"`
	RunOnStart bool   `json:"run_on_start"`
	Enabled    bool   `json:"enabled"`
	SessionID  string `json:"session_id,omitempty"`
	Surface    string `json:"surface,omitempty"`
}

type scheduleCreateToolOutput struct {
	Path string           `json:"path"`
	Task scheduleToolItem `json:"task"`
}

func (t *ScheduleCreateTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	var input scheduleCreateToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}

	task, raw, err := normalizeScheduleCreateInput(input)
	if err != nil {
		return tools.ErrorResult("schedule_create: " + err.Error()), nil
	}
	cfg, err := t.store.read()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("schedule_create: read config: %v", err)), nil
	}
	for _, existing := range cfg.ScheduledTasks {
		if strings.TrimSpace(existing.Name) == task.Name {
			return tools.ErrorResult(fmt.Sprintf("schedule_create: task %q already exists", task.Name)), nil
		}
	}
	cfg.ScheduledTasks = append(cfg.ScheduledTasks, raw)
	if err := t.store.write(cfg); err != nil {
		return tools.ErrorResult(fmt.Sprintf("schedule_create: write config: %v", err)), nil
	}

	output := scheduleCreateToolOutput{Path: t.store.path, Task: task}
	rawOutput, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("schedule_create: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(rawOutput)), nil
}

type ScheduleListTool struct {
	store *scheduleConfigStore
}

func NewScheduleListTool(path string) *ScheduleListTool {
	return &ScheduleListTool{store: newScheduleConfigStore(path)}
}

func (t *ScheduleListTool) Name() string { return ScheduleListToolName }

func (t *ScheduleListTool) Description() string {
	return "List static scheduled daemon tasks from scheduled_tasks.yaml"
}

func (t *ScheduleListTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{}, nil)
}

func (t *ScheduleListTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *ScheduleListTool) Reversible() bool { return true }

func (t *ScheduleListTool) Scope(json.RawMessage) tools.ToolScope { return tools.ToolScope{} }

func (t *ScheduleListTool) ShouldCancelSiblingsOnError() bool { return false }

type scheduleListToolOutput struct {
	Path  string             `json:"path"`
	Total int                `json:"total"`
	Tasks []scheduleToolItem `json:"tasks"`
}

func (t *ScheduleListTool) Execute(_ context.Context, _ json.RawMessage) (*tools.Result, error) {
	cfg, err := t.store.read()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("schedule_list: read config: %v", err)), nil
	}
	items := make([]scheduleToolItem, 0, len(cfg.ScheduledTasks))
	for _, raw := range cfg.ScheduledTasks {
		items = append(items, scheduleToolItemFromRaw(raw))
	}
	output := scheduleListToolOutput{Path: t.store.path, Total: len(items), Tasks: items}
	rawOutput, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("schedule_list: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(rawOutput)), nil
}

type ScheduleDeleteTool struct {
	store *scheduleConfigStore
}

func NewScheduleDeleteTool(path string) *ScheduleDeleteTool {
	return &ScheduleDeleteTool{store: newScheduleConfigStore(path)}
}

func (t *ScheduleDeleteTool) Name() string { return ScheduleDeleteToolName }

func (t *ScheduleDeleteTool) Description() string {
	return "Delete a static scheduled daemon task from scheduled_tasks.yaml"
}

func (t *ScheduleDeleteTool) Schema() json.RawMessage {
	return tools.Object(map[string]tools.Property{
		"name": tools.String("Scheduled task name to delete."),
	}, []string{"name"})
}

func (t *ScheduleDeleteTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *ScheduleDeleteTool) Reversible() bool { return false }

func (t *ScheduleDeleteTool) Scope(json.RawMessage) tools.ToolScope {
	return tools.ToolScope{Persistent: true}
}

func (t *ScheduleDeleteTool) ShouldCancelSiblingsOnError() bool { return false }

type scheduleDeleteToolInput struct {
	Name string `json:"name"`
}

type scheduleDeleteToolOutput struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

func (t *ScheduleDeleteTool) Execute(_ context.Context, params json.RawMessage) (*tools.Result, error) {
	var input scheduleDeleteToolInput
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return tools.ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
		}
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return tools.ErrorResult("schedule_delete: name is required"), nil
	}

	cfg, err := t.store.read()
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("schedule_delete: read config: %v", err)), nil
	}
	next := cfg.ScheduledTasks[:0]
	deleted := false
	for _, task := range cfg.ScheduledTasks {
		if strings.TrimSpace(task.Name) == name {
			deleted = true
			continue
		}
		next = append(next, task)
	}
	if !deleted {
		return tools.ErrorResult(fmt.Sprintf("schedule_delete: task %q not found", name)), nil
	}
	cfg.ScheduledTasks = next
	if err := t.store.write(cfg); err != nil {
		return tools.ErrorResult(fmt.Sprintf("schedule_delete: write config: %v", err)), nil
	}

	output := scheduleDeleteToolOutput{Path: t.store.path, Name: name, Deleted: true}
	rawOutput, err := json.Marshal(output)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("schedule_delete: marshal output: %v", err)), nil
	}
	return tools.SuccessResult(string(rawOutput)), nil
}

func normalizeScheduleCreateInput(input scheduleCreateToolInput) (scheduleToolItem, rawTask, error) {
	name := strings.TrimSpace(input.Name)
	prompt := strings.TrimSpace(input.Prompt)
	intervalRaw := strings.TrimSpace(input.Interval)
	taskType := strings.TrimSpace(input.Type)
	sessionID := strings.TrimSpace(input.SessionID)
	surface := strings.TrimSpace(input.Surface)
	interval, err := time.ParseDuration(intervalRaw)
	if err != nil {
		return scheduleToolItem{}, rawTask{}, fmt.Errorf("parse interval: %w", err)
	}
	task := ScheduledTask{
		Name:       name,
		Type:       taskType,
		Prompt:     prompt,
		Interval:   interval,
		RunOnStart: input.RunOnStart,
		Enabled:    true,
		SessionID:  sessionID,
		Surface:    surface,
	}
	if input.Enabled != nil {
		task.Enabled = *input.Enabled
	}
	if err := task.Validate(); err != nil {
		return scheduleToolItem{}, rawTask{}, err
	}
	raw := rawTask{
		Name:       task.Name,
		Type:       task.Type,
		Prompt:     task.Prompt,
		Interval:   task.Interval.String(),
		RunOnStart: task.RunOnStart,
		Enabled:    input.Enabled,
		SessionID:  task.SessionID,
		Surface:    task.Surface,
	}
	return scheduleToolItemFromRaw(raw), raw, nil
}

func scheduleToolItemFromRaw(raw rawTask) scheduleToolItem {
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}
	taskType := strings.TrimSpace(raw.Type)
	if taskType == "" {
		taskType = "agent"
	}
	return scheduleToolItem{
		Name:       strings.TrimSpace(raw.Name),
		Type:       taskType,
		Prompt:     strings.TrimSpace(raw.Prompt),
		Interval:   strings.TrimSpace(raw.Interval),
		RunOnStart: raw.RunOnStart,
		Enabled:    enabled,
		SessionID:  strings.TrimSpace(raw.SessionID),
		Surface:    strings.TrimSpace(raw.Surface),
	}
}
