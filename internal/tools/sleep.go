package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	SleepToolName     = "sleep"
	defaultSleepMaxMS = 5000
	sleepActionWait   = "wait"
	sleepStatusSlept  = "completed"
	sleepPolicyTimer  = "timer_wait"
)

type SleepTool struct {
	maxDuration time.Duration
}

func NewSleepTool() *SleepTool {
	return &SleepTool{maxDuration: defaultSleepMaxMS * time.Millisecond}
}

func (t *SleepTool) Name() string { return SleepToolName }

func (t *SleepTool) Description() string {
	return "Wait for a bounded duration without starting a shell process"
}

func (t *SleepTool) Schema() json.RawMessage {
	return Object(map[string]Property{
		"duration_ms": Int("Duration to wait in milliseconds. Must be positive and caps at 5000."),
		"reason":      String("Optional short reason for the wait, such as rate_limit_backoff or poll_spacing."),
	}, []string{"duration_ms"})
}

func (t *SleepTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *SleepTool) Reversible() bool { return true }

func (t *SleepTool) Scope(json.RawMessage) ToolScope { return ToolScope{} }

func (t *SleepTool) ShouldCancelSiblingsOnError() bool { return false }

func (t *SleepTool) DeferInitialToolSchema() bool { return true }

type sleepToolInput struct {
	DurationMS int    `json:"duration_ms"`
	Reason     string `json:"reason"`
}

type sleepToolOutput struct {
	RequestedMS int              `json:"requested_ms"`
	SleptMS     int              `json:"slept_ms"`
	Capped      bool             `json:"capped"`
	Reason      string           `json:"reason,omitempty"`
	Receipt     sleepToolReceipt `json:"receipt"`
}

type sleepToolReceipt struct {
	Tool               string `json:"tool"`
	Action             string `json:"action"`
	ReadOnly           bool   `json:"read_only"`
	Persistent         bool   `json:"persistent"`
	ExecutionAvailable bool   `json:"execution_available"`
	ExecutionPolicy    string `json:"execution_policy"`
	Status             string `json:"status"`
	TimeoutMS          int    `json:"timeout_ms"`
}

func (t *SleepTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var input sleepToolInput
	if len(params) == 0 {
		return ErrorResult("sleep: missing params"), nil
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return ErrorResult(fmt.Sprintf("invalid params: %v", err)), nil
	}
	if input.DurationMS <= 0 {
		return ErrorResult("sleep: duration_ms must be positive"), nil
	}

	requested := time.Duration(input.DurationMS) * time.Millisecond
	maxDuration := t.maxDuration
	if maxDuration <= 0 {
		maxDuration = defaultSleepMaxMS * time.Millisecond
	}
	duration := requested
	capped := false
	if duration > maxDuration {
		duration = maxDuration
		capped = true
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ErrorResult("sleep: interrupted before completion"), nil
	case <-timer.C:
	}

	output := sleepToolOutput{
		RequestedMS: input.DurationMS,
		SleptMS:     durationMillis(duration),
		Capped:      capped,
		Reason:      input.Reason,
		Receipt: sleepToolReceipt{
			Tool:               SleepToolName,
			Action:             sleepActionWait,
			ReadOnly:           true,
			Persistent:         false,
			ExecutionAvailable: true,
			ExecutionPolicy:    sleepPolicyTimer,
			Status:             sleepStatusSlept,
			TimeoutMS:          durationMillis(duration),
		},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return ErrorResult(fmt.Sprintf("sleep: marshal output: %v", err)), nil
	}
	return SuccessResult(string(raw)), nil
}

func durationMillis(d time.Duration) int {
	return int(d / time.Millisecond)
}
