package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/llm"
)

const (
	defaultTaskHandoffMessages = 6
	maxTaskHandoffMessages     = 20
)

type taskHandoffCLIOutput struct {
	TaskID        int64                  `json:"task_id"`
	Status        string                 `json:"status"`
	SessionID     string                 `json:"session_id"`
	ResumeCommand string                 `json:"resume_command"`
	Summary       string                 `json:"summary,omitempty"`
	ResultTail    string                 `json:"result_tail,omitempty"`
	MessageCount  int                    `json:"message_count"`
	LastMessages  []taskHandoffMessage   `json:"last_messages,omitempty"`
	ResumeCount   int                    `json:"resume_count"`
	Resumes       []taskHandoffResume    `json:"resumes,omitempty"`
	Handoff       *taskHandoffState      `json:"handoff,omitempty"`
	Retired       bool                   `json:"retired"`
	Retirement    *taskHandoffRetirement `json:"retirement,omitempty"`
	CreatedAt     string                 `json:"created_at,omitempty"`
	UpdatedAt     string                 `json:"updated_at,omitempty"`
	CompletedAt   string                 `json:"completed_at,omitempty"`
}

type taskHandoffMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type taskHandoffResume struct {
	Surface   string `json:"surface,omitempty"`
	Principal string `json:"principal,omitempty"`
	At        string `json:"at,omitempty"`
}

type taskHandoffState struct {
	State     string `json:"state"`
	Surface   string `json:"surface,omitempty"`
	Principal string `json:"principal,omitempty"`
	Reason    string `json:"reason,omitempty"`
	At        string `json:"at,omitempty"`
}

type taskHandoffRetirement struct {
	FailureClass string `json:"failure_class,omitempty"`
	Reason       string `json:"reason,omitempty"`
	NextAction   string `json:"next_action,omitempty"`
	At           string `json:"at,omitempty"`
}

func cmdTaskHandoff(ctx context.Context, args []string) error {
	cfg, db, err := openTaskDB()
	if err != nil {
		return err
	}
	defer db.Close()

	queue, err := daemon.NewQueue(db.Main)
	if err != nil {
		return fmt.Errorf("open queue: %w", err)
	}
	return cmdTaskHandoffWithQueue(ctx, queue, cfg.DataDir, args)
}

func cmdTaskHandoffWithQueue(ctx context.Context, queue *daemon.Queue, dataDir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: elnath task handoff <id> [--json|--markdown|--save] [--request SURFACE] [--state STATE --surface SURFACE --reason TEXT] [--max-messages N]")
	}
	taskID, err := parseTaskID(args[0])
	if err != nil {
		return err
	}
	jsonOut := false
	markdownOut := false
	saveMarkdown := false
	requestSurface := ""
	handoffState := ""
	handoffSurface := ""
	handoffReason := ""
	maxMessages := defaultTaskHandoffMessages
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--markdown":
			markdownOut = true
		case "--save":
			saveMarkdown = true
		case "--request":
			value, next, err := parseStringFlag(args, i, "--request")
			if err != nil {
				return err
			}
			requestSurface = strings.TrimSpace(value)
			i = next
		case "--state":
			value, next, err := parseStringFlag(args, i, "--state")
			if err != nil {
				return err
			}
			handoffState = strings.TrimSpace(value)
			i = next
		case "--surface":
			value, next, err := parseStringFlag(args, i, "--surface")
			if err != nil {
				return err
			}
			handoffSurface = strings.TrimSpace(value)
			i = next
		case "--reason":
			value, next, err := parseStringFlag(args, i, "--reason")
			if err != nil {
				return err
			}
			handoffReason = strings.TrimSpace(value)
			i = next
		case "--max-messages":
			value, next, err := parseIntFlag(args, i, "--max-messages")
			if err != nil {
				return err
			}
			maxMessages = normalizeTaskHandoffMessageLimit(value)
			i = next
		default:
			return fmt.Errorf("unknown task handoff flag: %s", args[i])
		}
	}
	if boolCount(jsonOut, markdownOut, saveMarkdown) > 1 {
		return fmt.Errorf("task handoff: choose only one output mode: --json, --markdown, or --save")
	}
	if requestSurface != "" && handoffState != "" {
		return fmt.Errorf("task handoff: choose either --request or --state, not both")
	}
	if requestSurface != "" {
		if err := recordTaskHandoffRequest(ctx, queue, dataDir, taskID, requestSurface); err != nil {
			return err
		}
	}
	if handoffState != "" {
		if err := recordTaskHandoffState(ctx, queue, dataDir, taskID, handoffState, handoffSurface, handoffReason); err != nil {
			return err
		}
	}

	view, err := buildTaskHandoff(ctx, queue, dataDir, taskID, maxMessages)
	if err != nil {
		return err
	}
	if jsonOut {
		raw, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return fmt.Errorf("task handoff: marshal output: %w", err)
		}
		fmt.Println(string(raw))
		return nil
	}
	if markdownOut {
		fmt.Println(formatTaskHandoffMarkdown(view))
		return nil
	}
	if saveMarkdown {
		path, err := saveTaskHandoffMarkdown(dataDir, view)
		if err != nil {
			return err
		}
		fmt.Printf("Saved handoff: %s\n", path)
		return nil
	}
	printTaskHandoff(view)
	return nil
}

func buildTaskHandoff(ctx context.Context, queue *daemon.Queue, dataDir string, taskID int64, maxMessages int) (taskHandoffCLIOutput, error) {
	if queue == nil {
		return taskHandoffCLIOutput{}, fmt.Errorf("task handoff: queue is required")
	}
	task, err := queue.Get(ctx, taskID)
	if err != nil {
		return taskHandoffCLIOutput{}, fmt.Errorf("task %d not found: %w", taskID, err)
	}
	sessionID := task.SessionID
	if task.Completion != nil && strings.TrimSpace(task.Completion.SessionID) != "" {
		sessionID = task.Completion.SessionID
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return taskHandoffCLIOutput{}, fmt.Errorf("task %d has no session bound", taskID)
	}
	sess, err := agent.LoadSession(dataDir, sessionID)
	if err != nil {
		return taskHandoffCLIOutput{}, fmt.Errorf("task handoff: load session %s: %w", sessionID, err)
	}

	resumes, err := agent.LoadSessionResumeEvents(dataDir, sessionID)
	if err != nil {
		return taskHandoffCLIOutput{}, fmt.Errorf("task handoff: load resume history %s: %w", sessionID, err)
	}
	retirement, err := agent.LoadSessionRetirementStatus(dataDir, sessionID)
	if err != nil {
		return taskHandoffCLIOutput{}, fmt.Errorf("task handoff: load retirement status %s: %w", sessionID, err)
	}
	handoffStatus, err := agent.LoadSessionHandoffStatus(dataDir, sessionID)
	if err != nil {
		return taskHandoffCLIOutput{}, fmt.Errorf("task handoff: load handoff status %s: %w", sessionID, err)
	}

	messages := sess.SnapshotMessages()
	view := taskHandoffCLIOutput{
		TaskID:        task.ID,
		Status:        string(task.Status),
		SessionID:     sessionID,
		ResumeCommand: fmt.Sprintf("elnath task resume %d", task.ID),
		Summary:       task.Summary,
		ResultTail:    tailString(task.Result, 500),
		MessageCount:  len(messages),
		LastMessages:  taskHandoffMessages(messages, normalizeTaskHandoffMessageLimit(maxMessages)),
		ResumeCount:   len(resumes),
		Resumes:       taskHandoffResumes(resumes),
		Handoff:       taskHandoffStatus(handoffStatus),
		Retired:       retirement != nil,
		CreatedAt:     formatTaskHandoffTime(task.CreatedAt),
		UpdatedAt:     formatTaskHandoffTime(task.UpdatedAt),
		CompletedAt:   formatTaskHandoffTime(task.CompletedAt),
	}
	if retirement != nil {
		view.Retirement = &taskHandoffRetirement{
			FailureClass: retirement.FailureClass,
			Reason:       retirement.Reason,
			NextAction:   retirement.NextAction,
			At:           formatTaskHandoffTime(retirement.At),
		}
	}
	return view, nil
}

func recordTaskHandoffRequest(ctx context.Context, queue *daemon.Queue, dataDir string, taskID int64, surface string) error {
	view, err := buildTaskHandoff(ctx, queue, dataDir, taskID, 1)
	if err != nil {
		return err
	}
	sess, err := agent.LoadSession(dataDir, view.SessionID)
	if err != nil {
		return fmt.Errorf("task handoff: load session %s: %w", view.SessionID, err)
	}
	return sess.RecordHandoff("requested", surface, identity.Principal{}, "operator requested task handoff")
}

func recordTaskHandoffState(ctx context.Context, queue *daemon.Queue, dataDir string, taskID int64, state, surface, reason string) error {
	view, err := buildTaskHandoff(ctx, queue, dataDir, taskID, 1)
	if err != nil {
		return err
	}
	sess, err := agent.LoadSession(dataDir, view.SessionID)
	if err != nil {
		return fmt.Errorf("task handoff: load session %s: %w", view.SessionID, err)
	}
	principal := taskHandoffOperatorPrincipal(surface)
	return sess.RecordHandoff(state, surface, principal, reason)
}

func taskHandoffOperatorPrincipal(surface string) identity.Principal {
	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = "cli"
	}
	userID := strings.TrimSpace(os.Getenv("USER"))
	if userID == "" {
		userID = "local"
	}
	return identity.Principal{
		UserID:    userID,
		ProjectID: "elnath",
		Surface:   surface,
	}
}

func normalizeTaskHandoffMessageLimit(n int) int {
	if n <= 0 {
		return defaultTaskHandoffMessages
	}
	if n > maxTaskHandoffMessages {
		return maxTaskHandoffMessages
	}
	return n
}

func taskHandoffMessages(messages []llm.Message, limit int) []taskHandoffMessage {
	if limit <= 0 {
		limit = defaultTaskHandoffMessages
	}
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}
	out := make([]taskHandoffMessage, 0, len(messages)-start)
	for _, msg := range messages[start:] {
		out = append(out, taskHandoffMessage{
			Role: msg.Role,
			Text: truncate(strings.TrimSpace(msg.TextContent()), 240),
		})
	}
	return out
}

func taskHandoffResumes(resumes []agent.SessionResumeEvent) []taskHandoffResume {
	out := make([]taskHandoffResume, 0, len(resumes))
	for _, resume := range resumes {
		out = append(out, taskHandoffResume{
			Surface:   resume.Surface,
			Principal: resume.Principal.SurfaceIdentity(),
			At:        formatTaskHandoffTime(resume.At),
		})
	}
	return out
}

func taskHandoffStatus(status *agent.SessionHandoffStatus) *taskHandoffState {
	if status == nil {
		return nil
	}
	return &taskHandoffState{
		State:     status.State,
		Surface:   status.Surface,
		Principal: status.Principal.SurfaceIdentity(),
		Reason:    status.Reason,
		At:        formatTaskHandoffTime(status.At),
	}
}

func printTaskHandoff(view taskHandoffCLIOutput) {
	fmt.Println("Task handoff")
	fmt.Printf("ID:           %d\n", view.TaskID)
	fmt.Printf("Status:       %s\n", view.Status)
	fmt.Printf("Session:      %s\n", view.SessionID)
	fmt.Printf("Resume:       %s\n", view.ResumeCommand)
	if view.Summary != "" {
		fmt.Printf("Summary:      %s\n", view.Summary)
	}
	if view.ResultTail != "" {
		fmt.Printf("Result tail:  %s\n", view.ResultTail)
	}
	fmt.Printf("Messages:     %d\n", view.MessageCount)
	fmt.Printf("Resumes:      %d\n", view.ResumeCount)
	if view.Handoff != nil {
		fmt.Printf("Handoff:     %s surface=%s\n", emptyDash(view.Handoff.State), emptyDash(view.Handoff.Surface))
	}
	if view.Retired && view.Retirement != nil {
		fmt.Printf("Retired:      true reason=%s next_action=%s\n", emptyDash(view.Retirement.Reason), emptyDash(view.Retirement.NextAction))
	} else {
		fmt.Println("Retired:      false")
	}
	if len(view.LastMessages) > 0 {
		fmt.Println("Last messages:")
		for _, msg := range view.LastMessages {
			fmt.Printf("  - %s: %s\n", msg.Role, msg.Text)
		}
	}
}

func formatTaskHandoffMarkdown(view taskHandoffCLIOutput) string {
	var b strings.Builder
	b.WriteString("# Task Handoff\n\n")
	fmt.Fprintf(&b, "- Task ID: %d\n", view.TaskID)
	fmt.Fprintf(&b, "- Status: %s\n", emptyDash(view.Status))
	fmt.Fprintf(&b, "- Session: %s\n", emptyDash(view.SessionID))
	fmt.Fprintf(&b, "- Resume command: `%s`\n", emptyDash(view.ResumeCommand))
	if view.Summary != "" {
		fmt.Fprintf(&b, "- Summary: %s\n", view.Summary)
	}
	if view.ResultTail != "" {
		fmt.Fprintf(&b, "- Result tail: %s\n", view.ResultTail)
	}
	fmt.Fprintf(&b, "- Message count: %d\n", view.MessageCount)
	fmt.Fprintf(&b, "- Resume count: %d\n", view.ResumeCount)
	if view.Retired && view.Retirement != nil {
		fmt.Fprintf(&b, "- Retired: true\n")
		fmt.Fprintf(&b, "- Retirement reason: %s\n", emptyDash(view.Retirement.Reason))
		fmt.Fprintf(&b, "- Retirement next action: %s\n", emptyDash(view.Retirement.NextAction))
	} else {
		b.WriteString("- Retired: false\n")
	}
	if view.Handoff != nil {
		fmt.Fprintf(&b, "- Handoff state: %s\n", emptyDash(view.Handoff.State))
		fmt.Fprintf(&b, "- Handoff surface: %s\n", emptyDash(view.Handoff.Surface))
	}
	if len(view.LastMessages) > 0 {
		b.WriteString("\n## Last Messages\n\n")
		for _, msg := range view.LastMessages {
			fmt.Fprintf(&b, "- %s: %s\n", emptyDash(msg.Role), msg.Text)
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func saveTaskHandoffMarkdown(dataDir string, view taskHandoffCLIOutput) (string, error) {
	dir := filepath.Join(dataDir, "task-handoffs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("task handoff: create handoff dir: %w", err)
	}
	sessionToken := safeTaskHandoffFileToken(view.SessionID)
	if sessionToken == "" {
		sessionToken = "session"
	}
	if len(sessionToken) > 12 {
		sessionToken = sessionToken[:12]
	}
	path := filepath.Join(dir, fmt.Sprintf("task-%d-%s.md", view.TaskID, sessionToken))
	if err := os.WriteFile(path, []byte(formatTaskHandoffMarkdown(view)), 0o644); err != nil {
		return "", fmt.Errorf("task handoff: write markdown: %w", err)
	}
	return path, nil
}

func safeTaskHandoffFileToken(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tailString(value string, max int) string {
	value = strings.TrimSpace(value)
	if value == "" || max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	return "..." + value[len(value)-max+3:]
}

func formatTaskHandoffTime(t time.Time) string {
	if t.IsZero() || t.UnixMilli() == 0 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
