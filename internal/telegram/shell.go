package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/daemon"
)

type Update struct {
	ID      int64   `json:"update_id"`
	Message Message `json:"message"`
}

type Message struct {
	ChatID string
	Text   string
}

type BotClient interface {
	SendMessage(ctx context.Context, chatID, text string) error
	GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error)
}

type Shell struct {
	queue     *daemon.Queue
	approvals *daemon.ApprovalStore
	bot       BotClient
	chatID    string
	statePath string
}

type shellState struct {
	NotifiedCompletionIDs []int64 `json:"notified_completion_ids"`
	NextUpdateOffset      int64   `json:"next_update_offset,omitempty"`
}

func NewShell(queue *daemon.Queue, approvals *daemon.ApprovalStore, bot BotClient, chatID, statePath string) (*Shell, error) {
	if queue == nil {
		return nil, fmt.Errorf("telegram shell: queue is required")
	}
	if approvals == nil {
		return nil, fmt.Errorf("telegram shell: approval store is required")
	}
	if bot == nil {
		return nil, fmt.Errorf("telegram shell: bot client is required")
	}
	if strings.TrimSpace(chatID) == "" {
		return nil, fmt.Errorf("telegram shell: chat id is required")
	}
	if strings.TrimSpace(statePath) == "" {
		return nil, fmt.Errorf("telegram shell: state path is required")
	}
	return &Shell{
		queue:     queue,
		approvals: approvals,
		bot:       bot,
		chatID:    strings.TrimSpace(chatID),
		statePath: statePath,
	}, nil
}

func (s *Shell) HandleUpdate(ctx context.Context, update Update) error {
	if strings.TrimSpace(update.Message.Text) == "" {
		return nil
	}
	if update.Message.ChatID != "" && update.Message.ChatID != s.chatID {
		return nil
	}

	reply, err := s.handleCommand(ctx, strings.TrimSpace(update.Message.Text))
	if err != nil {
		reply = "error: " + err.Error()
	}
	return s.bot.SendMessage(ctx, s.chatID, reply)
}

func (s *Shell) NotifyCompletions(ctx context.Context) error {
	state, err := s.loadState()
	if err != nil {
		return err
	}
	notified := make(map[int64]struct{}, len(state.NotifiedCompletionIDs))
	for _, id := range state.NotifiedCompletionIDs {
		notified[id] = struct{}{}
	}

	tasks, err := s.queue.List(ctx)
	if err != nil {
		return err
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	for _, task := range tasks {
		if task.Completion == nil {
			continue
		}
		if _, ok := notified[task.ID]; ok {
			continue
		}
		text := fmt.Sprintf(
			"Completion #%d\nstatus: %s\nsession: %s\nsummary: %s",
			task.ID,
			task.Completion.Status,
			emptyFallback(task.Completion.SessionID, "-"),
			emptyFallback(task.Completion.Summary, emptyFallback(task.Summary, "-")),
		)
		if err := s.bot.SendMessage(ctx, s.chatID, text); err != nil {
			return err
		}
		notified[task.ID] = struct{}{}
		state.NotifiedCompletionIDs = append(state.NotifiedCompletionIDs, task.ID)
	}
	return s.saveState(state)
}

func (s *Shell) NextOffset() (int64, error) {
	state, err := s.loadState()
	if err != nil {
		return 0, err
	}
	if state.NextUpdateOffset < 0 {
		return 0, nil
	}
	return state.NextUpdateOffset, nil
}

func (s *Shell) RememberOffset(nextOffset int64) error {
	state, err := s.loadState()
	if err != nil {
		return err
	}
	if nextOffset <= state.NextUpdateOffset {
		return nil
	}
	state.NextUpdateOffset = nextOffset
	return s.saveState(state)
}

func (s *Shell) handleCommand(ctx context.Context, text string) (string, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "empty command", nil
	}

	switch fields[0] {
	case "/status":
		return s.renderStatus(ctx)
	case "/approvals":
		return s.renderApprovals(ctx)
	case "/approve":
		return s.resolveApproval(ctx, fields, true)
	case "/deny":
		return s.resolveApproval(ctx, fields, false)
	case "/followup", "/resume":
		return s.enqueueFollowUp(ctx, text)
	case "/help":
		return "Commands: /status, /approvals, /approve <id>, /deny <id>, /followup <session_id> <message>", nil
	default:
		return "Unknown command. Use /help.", nil
	}
}

func (s *Shell) renderStatus(ctx context.Context) (string, error) {
	tasks, err := s.queue.List(ctx)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "No daemon tasks.", nil
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID > tasks[j].ID })
	limit := len(tasks)
	if limit > 5 {
		limit = 5
	}
	lines := []string{"Daemon status"}
	for _, task := range tasks[:limit] {
		progress := daemon.RenderProgress(task.Progress)
		if progress == "" {
			progress = "-"
		}
		lines = append(lines, fmt.Sprintf("#%d %s session=%s progress=%s", task.ID, task.Status, emptyFallback(task.SessionID, "-"), progress))
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Shell) renderApprovals(ctx context.Context) (string, error) {
	requests, err := s.approvals.ListPending(ctx)
	if err != nil {
		return "", err
	}
	if len(requests) == 0 {
		return "No pending approvals.", nil
	}
	lines := []string{"Pending approvals"}
	for _, req := range requests {
		lines = append(lines, fmt.Sprintf("#%d %s %s", req.ID, req.ToolName, strings.TrimSpace(req.Input)))
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Shell) resolveApproval(ctx context.Context, fields []string, approved bool) (string, error) {
	if len(fields) < 2 {
		return "", fmt.Errorf("usage: %s <id>", fields[0])
	}
	id, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid approval id %q", fields[1])
	}
	if err := s.approvals.Decide(ctx, id, approved); err != nil {
		return "", err
	}
	if approved {
		return fmt.Sprintf("Approved request #%d.", id), nil
	}
	return fmt.Sprintf("Denied request #%d.", id), nil
}

func (s *Shell) enqueueFollowUp(ctx context.Context, raw string) (string, error) {
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) < 3 {
		return "", fmt.Errorf("usage: /followup <session_id> <message>")
	}
	payload := daemon.TaskPayload{
		Prompt:    strings.TrimSpace(parts[2]),
		SessionID: strings.TrimSpace(parts[1]),
		Surface:   "telegram",
	}
	if payload.Prompt == "" || payload.SessionID == "" {
		return "", fmt.Errorf("usage: /followup <session_id> <message>")
	}
	id, err := s.queue.Enqueue(ctx, daemon.EncodeTaskPayload(payload))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Queued follow-up task #%d for session %s.", id, payload.SessionID), nil
}

func (s *Shell) loadState() (*shellState, error) {
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &shellState{}, nil
		}
		return nil, fmt.Errorf("telegram shell: read state: %w", err)
	}
	var state shellState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("telegram shell: parse state: %w", err)
	}
	return &state, nil
}

func (s *Shell) saveState(state *shellState) error {
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o755); err != nil {
		return fmt.Errorf("telegram shell: mkdir state: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("telegram shell: encode state: %w", err)
	}
	if err := os.WriteFile(s.statePath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("telegram shell: write state: %w", err)
	}
	return nil
}

func emptyFallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
