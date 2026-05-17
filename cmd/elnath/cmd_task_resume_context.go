package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/daemon"
)

const (
	taskResumeHandoffContextFlag     = "--task-resume-handoff-context"
	taskResumeHandoffMessageCount    = 4
	maxTaskResumeHandoffContextChars = 1200
)

type taskResumeHandoffContextKey struct{}

func withTaskResumeHandoffContext(ctx context.Context, value string) context.Context {
	value = strings.TrimSpace(value)
	if value == "" {
		return ctx
	}
	return context.WithValue(ctx, taskResumeHandoffContextKey{}, value)
}

func taskResumeHandoffContextFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(taskResumeHandoffContextKey{}).(string)
	return strings.TrimSpace(value)
}

func consumeTaskResumeHandoffContext(ctx context.Context, pending *string) context.Context {
	if pending == nil {
		return ctx
	}
	value := strings.TrimSpace(*pending)
	if value == "" {
		return ctx
	}
	*pending = ""
	return withTaskResumeHandoffContext(ctx, value)
}

func buildTaskResumeHandoffContext(ctx context.Context, queue *daemon.Queue, dataDir string, taskID int64) (string, error) {
	view, err := buildTaskHandoff(ctx, queue, dataDir, taskID, taskResumeHandoffMessageCount)
	if err != nil {
		return "", err
	}
	return formatTaskResumeHandoffContext(view, maxTaskResumeHandoffContextChars), nil
}

func taskResumeHandoffContextFromArgs(ctx context.Context, db *sql.DB, dataDir string, args []string) (string, error) {
	taskID, ok, err := taskResumeHandoffTaskIDFromArgs(args)
	if err != nil || !ok {
		return "", err
	}
	queue, err := daemon.NewQueue(db)
	if err != nil {
		return "", fmt.Errorf("open queue: %w", err)
	}
	return buildTaskResumeHandoffContext(ctx, queue, dataDir, taskID)
}

func taskResumeHandoffTaskIDFromArgs(args []string) (int64, bool, error) {
	raw := strings.TrimSpace(extractFlagValue(args, taskResumeHandoffContextFlag))
	if raw == "" {
		raw = strings.TrimSpace(extractFlagValue(args, "--continue-task"))
	}
	if raw == "" {
		return 0, false, nil
	}
	taskID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid task ID %q: %w", raw, err)
	}
	return taskID, true, nil
}

func taskResumeHandoffContextRequested(args []string) bool {
	return strings.TrimSpace(extractFlagValue(args, taskResumeHandoffContextFlag)) != "" ||
		strings.TrimSpace(extractFlagValue(args, "--continue-task")) != ""
}

func formatTaskResumeHandoffContext(view taskHandoffCLIOutput, maxChars int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "task_id: %d\n", view.TaskID)
	fmt.Fprintf(&b, "status: %s\n", emptyDash(view.Status))
	fmt.Fprintf(&b, "session_id: %s\n", emptyDash(view.SessionID))
	if view.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", view.Summary)
	}
	if view.ResultTail != "" {
		fmt.Fprintf(&b, "result_tail: %s\n", view.ResultTail)
	}
	fmt.Fprintf(&b, "resume_count: %d\n", view.ResumeCount)
	if view.Handoff != nil {
		fmt.Fprintf(&b, "handoff: %s surface=%s\n", emptyDash(view.Handoff.State), emptyDash(view.Handoff.Surface))
	}
	if view.Retired && view.Retirement != nil {
		fmt.Fprintf(&b, "retired: true reason=%s next_action=%s\n", emptyDash(view.Retirement.Reason), emptyDash(view.Retirement.NextAction))
	} else {
		b.WriteString("retired: false\n")
	}
	if len(view.LastMessages) > 0 {
		b.WriteString("last_messages:\n")
		for _, msg := range view.LastMessages {
			fmt.Fprintf(&b, "- %s: %s\n", emptyDash(msg.Role), msg.Text)
		}
	}
	out := strings.TrimSpace(b.String())
	if maxChars > 0 {
		out = truncate(out, maxChars)
	}
	return out
}
