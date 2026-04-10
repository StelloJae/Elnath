package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/llm"
)

type Update struct {
	ID      int64   `json:"update_id"`
	Message Message `json:"message"`
}

type Message struct {
	ChatID    string
	MessageID int64
	Text      string
}

type BotClient interface {
	SendMessage(ctx context.Context, chatID, text string) error
	SendMessageReturningID(ctx context.Context, chatID, text string) (int64, error)
	EditMessage(ctx context.Context, chatID string, messageID int64, text string) error
	SetReaction(ctx context.Context, chatID string, messageID int64, emoji string) error
	GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error)
}

// IntentClassifier classifies user messages into intent categories.
type IntentClassifier interface {
	Classify(ctx context.Context, provider llm.Provider, message string, history []llm.Message) (conversation.Intent, error)
}

// ShellOption configures optional Shell capabilities.
type ShellOption func(*Shell)

// WithChatResponder enables direct chat response for non-task intents.
func WithChatResponder(responder *ChatResponder) ShellOption {
	return func(s *Shell) { s.chatResponder = responder }
}

// WithClassifier enables intent classification before dispatch.
func WithClassifier(classifier IntentClassifier, provider llm.Provider) ShellOption {
	return func(s *Shell) {
		s.classifier = classifier
		s.classifyProvider = provider
	}
}

// TaskTracker receives task-to-message associations for reaction tracking.
type TaskTracker interface {
	TrackUserMessage(taskID, userMsgID int64)
}

// WithTaskTracker registers a sink that tracks user message IDs for reactions.
func WithTaskTracker(tracker TaskTracker) ShellOption {
	return func(s *Shell) { s.taskTracker = tracker }
}

type Shell struct {
	queue              *daemon.Queue
	approvals          *daemon.ApprovalStore
	bot                BotClient
	chatID             string
	statePath          string
	skipNotifyComplete bool
	logger             *slog.Logger
	chatResponder      *ChatResponder
	classifier         IntentClassifier
	classifyProvider   llm.Provider
	taskTracker        TaskTracker
}

type shellState struct {
	NotifiedCompletionIDs []int64 `json:"notified_completion_ids"`
	NextUpdateOffset      int64   `json:"next_update_offset,omitempty"`
}

func NewShell(queue *daemon.Queue, approvals *daemon.ApprovalStore, bot BotClient, chatID, statePath string, opts ...ShellOption) (*Shell, error) {
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
	s := &Shell{
		queue:     queue,
		approvals: approvals,
		bot:       bot,
		chatID:    strings.TrimSpace(chatID),
		statePath: statePath,
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

func (s *Shell) HandleUpdate(ctx context.Context, update Update) error {
	if strings.TrimSpace(update.Message.Text) == "" {
		return nil
	}
	if update.Message.ChatID != "" && update.Message.ChatID != s.chatID {
		return nil
	}

	text := strings.TrimSpace(update.Message.Text)
	fields := strings.Fields(text)

	// /submit is handled here (not in handleCommand) for task tracking.
	if len(fields) > 0 && fields[0] == "/submit" {
		if update.Message.MessageID > 0 {
			_ = s.bot.SetReaction(ctx, s.chatID, update.Message.MessageID, "👀")
		}
		prompt := strings.TrimSpace(strings.TrimPrefix(text, "/submit"))
		_ = s.bot.SendMessage(ctx, s.chatID, s.taskAcknowledgment(ctx, prompt))
		taskID, err := s.enqueueTaskReturningID(ctx, prompt)
		if err != nil {
			return s.bot.SendMessage(ctx, s.chatID, "⚠️ "+err.Error())
		}
		if s.taskTracker != nil && update.Message.MessageID > 0 {
			s.taskTracker.TrackUserMessage(taskID, update.Message.MessageID)
		}
		return nil
	}

	// Other explicit commands go to command handler.
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		reply, err := s.handleCommand(ctx, text)
		if err != nil {
			reply = "⚠️ " + err.Error()
		}
		return s.bot.SendMessage(ctx, s.chatID, reply)
	}

	// Non-command messages: classify intent if classifier is available.
	if s.classifier != nil && s.chatResponder != nil {
		intent, err := s.classifier.Classify(ctx, s.classifyProvider, text, nil)
		if err != nil {
			s.logger.Warn("intent classification failed, falling back to queue", "error", err)
		} else if isChatIntent(intent) {
			_ = s.bot.SetReaction(ctx, s.chatID, update.Message.MessageID, "👀")
			return s.chatResponder.Respond(ctx, text, update.Message.MessageID)
		}
	}

	// Task intent — enqueue.
	if update.Message.MessageID > 0 {
		_ = s.bot.SetReaction(ctx, s.chatID, update.Message.MessageID, "👀")
	}
	_ = s.bot.SendMessage(ctx, s.chatID, s.taskAcknowledgment(ctx, text))
	taskID, err := s.enqueueTaskReturningID(ctx, text)
	if err != nil {
		return s.bot.SendMessage(ctx, s.chatID, "⚠️ "+err.Error())
	}
	if s.taskTracker != nil && update.Message.MessageID > 0 {
		s.taskTracker.TrackUserMessage(taskID, update.Message.MessageID)
	}
	return nil
}

func (s *Shell) taskAcknowledgment(ctx context.Context, userMessage string) string {
	if s.classifyProvider == nil {
		return "👀"
	}
	resp, err := s.classifyProvider.Chat(ctx, llm.ChatRequest{
		Messages:    []llm.Message{llm.NewUserMessage(userMessage)},
		System:      taskAckPrompt,
		MaxTokens:   40,
		Temperature: 0.9,
	})
	if err != nil || strings.TrimSpace(resp.Content) == "" {
		return "👀"
	}
	return strings.TrimSpace(resp.Content)
}

const taskAckPrompt = `You are a personal AI assistant. The user just gave you a task.
Generate a brief, warm acknowledgment (1 sentence, under 12 words).
Match the user's language. Sound like a real person, not a system.
No task numbers, no technical jargon, no markdown, no HTML, no quotes.`

func isChatIntent(intent conversation.Intent) bool {
	switch intent {
	case conversation.IntentChat, conversation.IntentQuestion, conversation.IntentWikiQuery:
		return true
	default:
		return false
	}
}

// SkipNotifyCompletions disables the shell's own completion polling when
// a TelegramSink is registered on the daemon's delivery router.
func (s *Shell) SkipNotifyCompletions() {
	s.skipNotifyComplete = true
}

func (s *Shell) NotifyCompletions(ctx context.Context) error {
	if s.skipNotifyComplete {
		return nil
	}
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
	case "/submit":
		return s.enqueueNewTask(ctx, text)
	case "/help":
		return "📖 <b>Commands</b>\n" +
			"• <code>/status</code> — task status\n" +
			"• <code>/submit &lt;msg&gt;</code> — new task\n" +
			"• <code>/approvals</code> — pending approvals\n" +
			"• <code>/approve &lt;id&gt;</code> — approve\n" +
			"• <code>/deny &lt;id&gt;</code> — deny\n" +
			"• <code>/followup &lt;sid&gt; &lt;msg&gt;</code> — follow-up\n" +
			"• <i>or just type a message</i>", nil
	default:
		if strings.HasPrefix(fields[0], "/") {
			return "Unknown command. Use /help.", nil
		}
		return s.enqueueNewTask(ctx, text)
	}
}

func (s *Shell) renderStatus(ctx context.Context) (string, error) {
	tasks, err := s.queue.List(ctx)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "📭 No tasks.", nil
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID > tasks[j].ID })
	limit := len(tasks)
	if limit > 5 {
		limit = 5
	}
	lines := []string{"📋 <b>Status</b>"}
	for _, task := range tasks[:limit] {
		icon := statusIcon(task.Status)
		progress := daemon.RenderProgress(task.Progress)
		if progress == "" {
			progress = "-"
		}
		if len(progress) > 60 {
			progress = progress[:57] + "..."
		}
		lines = append(lines, fmt.Sprintf("%s <code>#%d</code> %s\n   <i>%s</i>", icon, task.ID, task.Status, escapeHTML(progress)))
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Shell) renderApprovals(ctx context.Context) (string, error) {
	requests, err := s.approvals.ListPending(ctx)
	if err != nil {
		return "", err
	}
	if len(requests) == 0 {
		return "✅ No pending approvals.", nil
	}
	lines := []string{"⚠️ <b>Pending Approvals</b>"}
	for _, req := range requests {
		input := strings.TrimSpace(req.Input)
		if len(input) > 80 {
			input = input[:77] + "..."
		}
		lines = append(lines, fmt.Sprintf("• <code>#%d</code> <b>%s</b>\n  <code>%s</code>", req.ID, escapeHTML(req.ToolName), escapeHTML(input)))
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
		return fmt.Sprintf("✅ Approved <code>#%d</code>", id), nil
	}
	return fmt.Sprintf("❌ Denied <code>#%d</code>", id), nil
}

func (s *Shell) enqueueTaskReturningID(ctx context.Context, prompt string) (int64, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return 0, fmt.Errorf("usage: /submit <message> or just type your message")
	}
	payload := daemon.TaskPayload{
		Prompt:  prompt,
		Surface: "telegram",
	}
	return s.queue.Enqueue(ctx, daemon.EncodeTaskPayload(payload))
}

func (s *Shell) enqueueNewTask(ctx context.Context, raw string) (string, error) {
	prompt := raw
	if strings.HasPrefix(prompt, "/submit") {
		prompt = strings.TrimSpace(strings.TrimPrefix(prompt, "/submit"))
	}
	id, err := s.enqueueTaskReturningID(ctx, prompt)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("🚀 Task <code>#%d</code> queued", id), nil
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
	return fmt.Sprintf("🔄 Follow-up <code>#%d</code> queued for session <code>%s</code>", id, payload.SessionID[:8]), nil
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

func statusIcon(status daemon.TaskStatus) string {
	switch status {
	case daemon.StatusPending:
		return "⏳"
	case daemon.StatusRunning:
		return "⚡"
	case daemon.StatusDone:
		return "✅"
	case daemon.StatusFailed:
		return "❌"
	default:
		return "•"
	}
}

func emptyFallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
