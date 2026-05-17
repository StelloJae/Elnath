package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/stello/elnath/internal/agent"
	"github.com/stello/elnath/internal/conversation"
	"github.com/stello/elnath/internal/daemon"
	"github.com/stello/elnath/internal/identity"
	"github.com/stello/elnath/internal/learning"
	"github.com/stello/elnath/internal/llm"
	"github.com/stello/elnath/internal/routing"
	"github.com/stello/elnath/internal/skill"
	"github.com/stello/elnath/internal/wiki"
)

type Update struct {
	ID            int64          `json:"update_id"`
	Message       Message        `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Message struct {
	ChatID    string
	UserID    string
	MessageID int64
	Text      string
}

type CallbackQuery struct {
	ID      string
	FromID  string
	Data    string
	Message Message
}

type TelegramButton struct {
	Text string
	Data string
}

type BotClient interface {
	SendMessage(ctx context.Context, chatID, text string) error
	SendMessageReturningID(ctx context.Context, chatID, text string) (int64, error)
	EditMessage(ctx context.Context, chatID string, messageID int64, text string) error
	SetReaction(ctx context.Context, chatID string, messageID int64, emoji string) error
	GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error)
}

type buttonMessageSender interface {
	SendMessageWithButtons(ctx context.Context, chatID, text string, buttons [][]TelegramButton) error
}

type callbackAcknowledger interface {
	AnswerCallback(ctx context.Context, callbackID, text string) error
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

type ChatBindingTracker interface {
	TrackChatBinding(taskID int64, userID string)
}

// WithTaskTracker registers a sink that tracks user message IDs for reactions.
func WithTaskTracker(tracker TaskTracker) ShellOption {
	return func(s *Shell) { s.taskTracker = tracker }
}

func WithWorkDir(workDir string) ShellOption {
	return func(s *Shell) { s.workDir = strings.TrimSpace(workDir) }
}

func WithShellDataDir(dataDir string) ShellOption {
	return func(s *Shell) { s.dataDir = strings.TrimSpace(dataDir) }
}

func WithChatSessionBinder(binder *ChatSessionBinder) ShellOption {
	return func(s *Shell) { s.binder = binder }
}

// defaultClassifierHistoryTurns caps how many prior turns are injected into
// the intent classifier. Follow-up classification only needs the last few
// exchanges for reference resolution ("그거 다시 해줘" → prior task);
// keeping the window small protects classifier latency and token cost.
const defaultClassifierHistoryTurns = 10

// WithChatHistoryLoader injects the history source used to hydrate the
// intent classifier with recent session turns. Without this, the classifier
// sees every message in isolation — follow-ups referencing prior turns are
// routed incorrectly (GAP-TG-03).
func WithChatHistoryLoader(loader ChatHistoryLoader) ShellOption {
	return func(s *Shell) { s.historyLoader = loader }
}

func WithSkillCreator(creator *skill.Creator) ShellOption {
	return func(s *Shell) { s.skillCreator = creator }
}

func WithLearningStore(store *learning.Store) ShellOption {
	return func(s *Shell) { s.learningStore = store }
}

func WithShellOutcomeStore(store *learning.OutcomeStore) ShellOption {
	return func(s *Shell) { s.outcomeStore = store }
}

func WithWikiStore(store *wiki.Store) ShellOption {
	return func(s *Shell) { s.wikiStore = store }
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
	workDir            string
	dataDir            string
	binder             *ChatSessionBinder
	historyLoader      ChatHistoryLoader
	skillReg           *skill.Registry
	skillCreator       *skill.Creator
	learningStore      *learning.Store
	outcomeStore       *learning.OutcomeStore
	wikiStore          *wiki.Store
}

type shellState struct {
	NotifiedCompletionIDs []int64 `json:"notified_completion_ids"`
	NextUpdateOffset      int64   `json:"next_update_offset,omitempty"`
}

func NewShell(queue *daemon.Queue, approvals *daemon.ApprovalStore, bot BotClient, chatID, statePath string, skillReg *skill.Registry, opts ...ShellOption) (*Shell, error) {
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
		skillReg:  skillReg,
	}
	if cwd, err := os.Getwd(); err == nil {
		s.workDir = cwd
	} else {
		s.logger.Debug("telegram shell: os.Getwd failed, workDir empty until WithWorkDir override", "error", err)
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

func (s *Shell) HandleUpdate(ctx context.Context, update Update) error {
	if update.CallbackQuery != nil && strings.TrimSpace(update.CallbackQuery.Data) != "" {
		return s.handleCallbackQuery(ctx, *update.CallbackQuery)
	}
	if strings.TrimSpace(update.Message.Text) == "" {
		return nil
	}
	if update.Message.ChatID != "" && update.Message.ChatID != s.chatID {
		return nil
	}

	text := strings.TrimSpace(update.Message.Text)
	fields := strings.Fields(text)
	principal := s.principalForMessage(update.Message)

	// /submit is handled here (not in handleCommand) for task tracking.
	if len(fields) > 0 && fields[0] == "/submit" {
		if update.Message.MessageID > 0 {
			_ = s.bot.SetReaction(ctx, s.chatID, update.Message.MessageID, "👀")
		}
		prompt := strings.TrimSpace(strings.TrimPrefix(text, "/submit"))
		taskID, existed, err := s.enqueueTaskReturningID(ctx, prompt, principal)
		if err != nil {
			return s.bot.SendMessage(ctx, s.chatID, "⚠️ "+err.Error())
		}
		if existed {
			return s.bot.SendMessage(ctx, s.chatID, dedupMessage(taskID))
		}
		_ = s.bot.SendMessage(ctx, s.chatID, s.taskAcknowledgment(ctx, prompt))
		s.trackEnqueuedTask(taskID, update.Message.MessageID, principal.UserID)
		return nil
	}

	// Other explicit commands go to command handler.
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		reply, err := s.handleCommand(ctx, text, principal, update.Message.MessageID)
		if err != nil {
			reply = "⚠️ " + err.Error()
		}
		if err == nil && (fields[0] == "/questions" || fields[0] == "/pending-questions") {
			if sent, sendErr := s.trySendPendingQuestionsWithButtons(ctx, principal, reply); sent {
				return sendErr
			}
		}
		return s.bot.SendMessage(ctx, s.chatID, reply)
	}

	if handled, reply, err := s.handlePendingQuestionText(ctx, text, principal); handled {
		if err != nil {
			reply = "⚠️ " + err.Error()
		}
		return s.bot.SendMessage(ctx, s.chatID, reply)
	}

	// Non-command messages: classify intent if classifier is available.
	if s.classifier != nil && s.chatResponder != nil {
		classifyHistory := s.loadClassifierHistory(ctx, principal.UserID)
		intent, err := s.classifier.Classify(ctx, s.classifyProvider, text, classifyHistory)
		if err != nil {
			s.logger.Warn("intent classification failed, falling back to queue", "error", err)
		} else if isChatIntent(intent) && !s.hasPinnedWorkflow(intent, principal.ProjectID) {
			_ = s.bot.SetReaction(ctx, s.chatID, update.Message.MessageID, "👀")
			return s.chatResponder.Respond(ctx, principal, text, update.Message.MessageID)
		}
	}

	// Task intent — enqueue.
	if update.Message.MessageID > 0 {
		_ = s.bot.SetReaction(ctx, s.chatID, update.Message.MessageID, "👀")
	}
	taskID, existed, err := s.enqueueTaskReturningID(ctx, text, principal)
	if err != nil {
		return s.bot.SendMessage(ctx, s.chatID, "⚠️ "+err.Error())
	}
	if existed {
		return s.bot.SendMessage(ctx, s.chatID, dedupMessage(taskID))
	}
	_ = s.bot.SendMessage(ctx, s.chatID, s.taskAcknowledgment(ctx, text))
	s.trackEnqueuedTask(taskID, update.Message.MessageID, principal.UserID)
	return nil
}

// loadClassifierHistory hydrates the bound session's recent turns for intent
// classification. A nil slice is returned (and Classify falls back to
// single-message behavior) when no binder/loader is wired, when the session
// is unbound, or when history retrieval fails. Load errors are warned but
// never propagated — classifier must still run.
func (s *Shell) loadClassifierHistory(ctx context.Context, userID string) []llm.Message {
	if s.binder == nil || s.historyLoader == nil {
		return nil
	}
	sid, ok := s.binder.Lookup(s.chatID, userID)
	if !ok {
		return nil
	}
	hist, err := s.historyLoader.GetHistory(ctx, sid)
	if err != nil {
		s.logger.Warn("classifier history load failed, continuing with nil history",
			"session_id", sid,
			"error", err,
		)
		return nil
	}
	return trimClassifierHistory(hist, defaultClassifierHistoryTurns)
}

func trimClassifierHistory(msgs []llm.Message, maxTurns int) []llm.Message {
	if maxTurns <= 0 || len(msgs) <= maxTurns {
		return msgs
	}
	return msgs[len(msgs)-maxTurns:]
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

// hasPinnedWorkflow returns true when the project has a preferred workflow
// mapped for this intent. Chat-intent messages defer to the queue+router when
// true so pinned (via /override) or learned (via RoutingAdvisor) preferences
// apply to chat-like intents too, not only workflow-typed ones.
func (s *Shell) hasPinnedWorkflow(intent conversation.Intent, projectID string) bool {
	if s.wikiStore == nil || strings.TrimSpace(projectID) == "" {
		return false
	}
	pref, err := wiki.LoadWorkflowPreference(s.wikiStore, projectID)
	if err != nil {
		s.logger.Warn("telegram: routing preference lookup failed, staying on chat path", "error", err)
		return false
	}
	if pref == nil {
		return false
	}
	return pref.PreferredWorkflow(string(intent)) != ""
}

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
		if handoff := telegramHandoffCommand(task.ID, task.Completion.SessionID); handoff != "" {
			text += "\nhandoff: " + handoff
		}
		if err := s.bot.SendMessage(ctx, s.chatID, text); err != nil {
			return err
		}
		if s.binder != nil && task.Completion.SessionID != "" {
			payload := daemon.ParseTaskPayload(task.Payload)
			if payload.Surface == "telegram" && payload.Principal.UserID != "" {
				if err := s.binder.Remember(s.chatID, payload.Principal.UserID, task.Completion.SessionID); err != nil {
					s.logger.Warn("telegram: binding remember failed (poll)", "task_id", task.ID, "error", err)
				}
			}
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

func (s *Shell) handleCommand(ctx context.Context, text string, principal identity.Principal, userMsgID int64) (string, error) {
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
		return s.resolveApproval(ctx, fields, true, principal)
	case "/deny":
		return s.resolveApproval(ctx, fields, false, principal)
	case "/followup", "/resume":
		return s.enqueueFollowUp(ctx, text, principal, userMsgID)
	case "/handoff":
		return s.handleHandoff(ctx, fields, principal)
	case "/submit":
		return s.enqueueNewTask(ctx, text, principal)
	case "/questions", "/pending-questions":
		return s.renderPendingQuestions(principal)
	case "/answer":
		return s.answerPendingQuestion(ctx, text, principal)
	case "/cancel-question":
		return s.cancelPendingQuestion(ctx, text)
	case "/skill-list":
		return s.handleSkillList(), nil
	case "/skill-create":
		if len(fields) < 2 {
			return "Usage: /skill-create <name>", nil
		}
		return s.handleSkillCreate(fields[1])
	case "/remember":
		text := strings.Join(fields[1:], " ")
		if text == "" {
			return "Usage: /remember <lesson text>", nil
		}
		if s.learningStore == nil {
			return "Learning store unavailable.", nil
		}
		lesson := learning.Lesson{
			Text:       text,
			Source:     "user:telegram",
			Confidence: "high",
			Topic:      identity.ResolveProjectID(s.workDir, ""),
		}
		if err := s.learningStore.Append(lesson); err != nil {
			return fmt.Sprintf("Failed: %v", err), nil
		}
		// Derive ID the same way learning.Store does: sha256(text)[:8].
		sum := sha256.Sum256([]byte(text))
		id := hex.EncodeToString(sum[:])[:8]
		return fmt.Sprintf("Remembered (ID: %s)", id), nil

	case "/forget":
		if len(fields) < 2 {
			return "Usage: /forget <lesson-id-prefix>", nil
		}
		if s.learningStore == nil {
			return "Learning store unavailable.", nil
		}
		n, err := s.learningStore.Delete(fields[1])
		if err != nil {
			return fmt.Sprintf("Failed: %v", err), nil
		}
		return fmt.Sprintf("Forgot %d lesson(s).", n), nil

	case "/override":
		if len(fields) < 2 {
			return "Usage: /override <intent> <workflow> | /override clear", nil
		}
		projectID := identity.ResolveProjectID(s.workDir, "")
		if projectID == "" {
			return "No active project context.", nil
		}
		if s.wikiStore == nil {
			return "Wiki store unavailable.", nil
		}
		if fields[1] == "clear" {
			// Delete the routing-preferences page so the advisor starts fresh.
			// Ignore not-found: if the page is already absent the result is the same.
			relPath := "projects/" + projectID + "/routing-preferences.md"
			_ = s.wikiStore.Delete(relPath)
			return fmt.Sprintf("Override cleared for project %s. Advisor manages routing again.", projectID), nil
		}
		if len(fields) < 3 {
			return "Usage: /override <intent> <workflow>", nil
		}
		intent, workflow := fields[1], fields[2]
		validWorkflows := map[string]bool{
			"single": true, "team": true, "autopilot": true, "ralph": true, "research": true,
		}
		if !validWorkflows[workflow] {
			return fmt.Sprintf("Unknown workflow %q. Valid: single, team, autopilot, ralph, research.", workflow), nil
		}
		pref := &routing.WorkflowPreference{
			PreferredWorkflows: map[string]string{intent: workflow},
		}
		if err := wiki.SaveUserWorkflowPreference(s.wikiStore, projectID, pref); err != nil {
			return fmt.Sprintf("Failed: %v", err), nil
		}
		return fmt.Sprintf("Override set: %s -> %s for project %s.", intent, workflow, projectID), nil

	case "/undo":
		taskID, cancelled, err := s.queue.CancelPendingTask(ctx, "cancelled by user via /undo")
		if err != nil {
			return fmt.Sprintf("Failed: %v", err), nil
		}
		if !cancelled {
			return "No pending task to cancel.", nil
		}
		return fmt.Sprintf("Task #%d cancelled.", taskID), nil

	case "/help":
		help := "📖 <b>Commands</b>\n" +
			"• <code>/status</code> — task status\n" +
			"• <code>/submit &lt;msg&gt;</code> — new task\n" +
			"• <code>/approvals</code> — pending approvals\n" +
			"• <code>/approve &lt;id&gt;</code> — approve\n" +
			"• <code>/deny &lt;id&gt;</code> — deny\n" +
			"• <code>/remember &lt;text&gt;</code> — save a lesson\n" +
			"• <code>/forget &lt;id&gt;</code> — delete a lesson by ID prefix\n" +
			"• <code>/override &lt;intent&gt; &lt;workflow&gt;</code> — pin routing\n" +
			"• <code>/override clear</code> — remove routing pin\n" +
			"• <code>/undo</code> — cancel last pending task\n" +
			"• <code>/questions</code> — pending user questions\n" +
			"• <code>/answer &lt;sid&gt; &lt;rid&gt; &lt;text&gt;</code> — answer pending question\n" +
			"• <code>/cancel-question &lt;sid&gt; &lt;rid&gt; [reason]</code> — cancel pending question\n" +
			"• <code>/handoff &lt;task&gt; [state] [reason]</code> — session handoff recap/state\n" +
			"• <code>/skill-list</code> — registered skills\n" +
			"• <code>/skill-create</code> — create draft skill\n" +
			"• <code>/followup &lt;sid&gt; &lt;msg&gt;</code> — follow-up\n" +
			"• <i>or just type a message</i>"
		if s.skillReg != nil {
			skills := s.skillReg.List()
			if len(skills) > 0 {
				help += "\n\n🛠 <b>Skills</b>"
				for _, sk := range skills {
					help += fmt.Sprintf("\n• <code>/%s</code> — %s", sk.Name, sk.Description)
				}
			}
		}
		return help, nil
	default:
		if strings.HasPrefix(fields[0], "/") {
			if s.skillReg != nil {
				skillName := strings.TrimPrefix(fields[0], "/")
				if _, ok := s.skillReg.Get(skillName); ok {
					if userMsgID > 0 {
						_ = s.bot.SetReaction(ctx, s.chatID, userMsgID, "👀")
					}
					skillPrompt := fmt.Sprintf("[Skill: %s] %s", skillName, text)
					taskID, existed, err := s.enqueueTaskReturningID(ctx, skillPrompt, principal)
					if err != nil {
						return "", err
					}
					if existed {
						return dedupMessage(taskID), nil
					}
					s.trackEnqueuedTask(taskID, userMsgID, principal.UserID)
					return fmt.Sprintf("🚀 Task <code>#%d</code> queued", taskID), nil
				}
			}
			return "Unknown command. Use /help.", nil
		}
		return s.enqueueNewTask(ctx, text, principal)
	}
}

func (s *Shell) handleSkillList() string {
	// Prefer the wiki-backed listing so draft skills created via
	// /skill-create are visible to the user. The in-memory registry
	// intentionally omits drafts (they are not executable yet), but the
	// list UI must reveal them so the author can find and promote them.
	var skills []*skill.Skill
	if s.wikiStore != nil {
		if all, err := skill.ListAllFromStore(s.wikiStore); err == nil {
			skills = all
		}
	}
	if skills == nil && s.skillReg != nil {
		skills = s.skillReg.List()
	}
	if len(skills) == 0 {
		return "No skills found."
	}
	lines := []string{"🛠 <b>Skills</b>"}
	for _, sk := range skills {
		label := fmt.Sprintf("• <code>/%s</code> — %s", sk.Name, escapeHTML(sk.Description))
		if sk.Status != "" && sk.Status != "active" {
			label = label + fmt.Sprintf(" <i>(%s)</i>", escapeHTML(sk.Status))
		}
		lines = append(lines, label)
	}
	return strings.Join(lines, "\n")
}

func (s *Shell) handleSkillCreate(name string) (string, error) {
	if s.skillCreator == nil {
		return "Skill creation is unavailable in this shell.", nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "Usage: /skill-create <name>", nil
	}
	if _, err := s.skillCreator.Create(skill.CreateParams{
		Name:        name,
		Description: "Draft skill scaffold created from Telegram",
		Trigger:     "/" + name,
		Prompt:      "TODO: replace this draft skill prompt.",
		Status:      "draft",
		Source:      "user",
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created draft skill /%s. Edit the wiki entry to finish it.", name), nil
}

func (s *Shell) handleHandoff(ctx context.Context, fields []string, principal identity.Principal) (string, error) {
	if strings.TrimSpace(s.dataDir) == "" {
		return "Handoff unavailable: data dir is not configured.", nil
	}
	if len(fields) < 2 {
		return "Usage: /handoff <task_id> [requested|claimed|running|completed|failed] [reason]", nil
	}
	taskID, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || taskID <= 0 {
		return "Usage: /handoff <task_id> [requested|claimed|running|completed|failed] [reason]", nil
	}
	if len(fields) >= 3 {
		state := strings.TrimSpace(fields[2])
		reason := strings.TrimSpace(strings.Join(fields[3:], " "))
		if state == "request" {
			state = "requested"
		}
		if err := s.recordHandoffState(ctx, taskID, state, reason, principal); err != nil {
			return "", err
		}
	}
	return s.renderTaskHandoff(ctx, taskID)
}

func (s *Shell) recordHandoffState(ctx context.Context, taskID int64, state, reason string, principal identity.Principal) error {
	sessionID, err := s.taskSessionID(ctx, taskID)
	if err != nil {
		return err
	}
	sess, err := agent.LoadSession(s.dataDir, sessionID)
	if err != nil {
		return fmt.Errorf("telegram handoff: load session %s: %w", sessionID, err)
	}
	return sess.RecordHandoff(state, "telegram", principal, reason)
}

func (s *Shell) renderTaskHandoff(ctx context.Context, taskID int64) (string, error) {
	task, err := s.queue.Get(ctx, taskID)
	if err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(task.SessionID)
	if task.Completion != nil && strings.TrimSpace(task.Completion.SessionID) != "" {
		sessionID = strings.TrimSpace(task.Completion.SessionID)
	}
	if sessionID == "" {
		return fmt.Sprintf("Task #%d has no session bound.", taskID), nil
	}
	sess, err := agent.LoadSession(s.dataDir, sessionID)
	if err != nil {
		return "", fmt.Errorf("telegram handoff: load session %s: %w", sessionID, err)
	}
	handoff, err := agent.LoadSessionHandoffStatus(s.dataDir, sessionID)
	if err != nil {
		return "", fmt.Errorf("telegram handoff: load handoff status %s: %w", sessionID, err)
	}
	messages := sess.SnapshotMessages()
	lines := []string{
		fmt.Sprintf("🔁 <b>Task handoff</b> <code>#%d</code>", task.ID),
		fmt.Sprintf("Status: <code>%s</code>", escapeHTML(string(task.Status))),
		fmt.Sprintf("Session: <code>%s</code>", escapeHTML(truncateSessionID(sessionID))),
		fmt.Sprintf("Resume: <code>elnath task resume %d</code>", task.ID),
	}
	if task.Summary != "" {
		lines = append(lines, "Summary: "+escapeHTML(truncateTelegramText(strings.TrimSpace(task.Summary), 160)))
	}
	if handoff != nil {
		stateLine := fmt.Sprintf("Handoff: <code>%s</code>", escapeHTML(handoff.State))
		if handoff.Surface != "" {
			stateLine += " via <code>" + escapeHTML(handoff.Surface) + "</code>"
		}
		if handoff.Reason != "" {
			stateLine += " — " + escapeHTML(truncateTelegramText(handoff.Reason, 120))
		}
		lines = append(lines, stateLine)
	}
	tail := telegramHandoffMessages(messages, 3)
	if len(tail) > 0 {
		lines = append(lines, "Last messages:")
		lines = append(lines, tail...)
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Shell) taskSessionID(ctx context.Context, taskID int64) (string, error) {
	task, err := s.queue.Get(ctx, taskID)
	if err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(task.SessionID)
	if task.Completion != nil && strings.TrimSpace(task.Completion.SessionID) != "" {
		sessionID = strings.TrimSpace(task.Completion.SessionID)
	}
	if sessionID == "" {
		return "", fmt.Errorf("telegram handoff: task %d has no session bound", taskID)
	}
	return sessionID, nil
}

func telegramHandoffMessages(messages []llm.Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, len(messages)-start)
	for _, msg := range messages[start:] {
		text := truncateTelegramText(strings.TrimSpace(msg.TextContent()), 180)
		if text == "" {
			continue
		}
		out = append(out, fmt.Sprintf("• <code>%s</code>: %s", escapeHTML(msg.Role), escapeHTML(text)))
	}
	return out
}

func truncateTelegramText(value string, max int) string {
	value = strings.TrimSpace(value)
	if value == "" || max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func truncateSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8]
}

func (s *Shell) renderPendingQuestions(principal identity.Principal) (string, error) {
	if s.outcomeStore == nil {
		return "Pending questions are unavailable in this shell.", nil
	}
	records, err := s.outcomeStore.Recent(0)
	if err != nil {
		return "", err
	}
	directReplySessionID, directReplyAllowed := s.directReplyPendingQuestionSession(records, principal)
	pending := learning.PendingUserQuestions(records, "", 10)
	if len(pending) == 0 {
		return "No pending user questions.", nil
	}
	lines := []string{"❓ <b>Pending questions</b>"}
	for _, q := range pending {
		lines = append(lines, fmt.Sprintf("• <code>%s</code> session=<code>%s</code>", escapeHTML(q.RequestID), escapeHTML(emptyFallback(q.SessionID, "-"))))
		if q.Question != "" {
			lines = append(lines, "  "+escapeHTML(q.Question))
		}
		if len(q.Options) > 0 {
			choices := make([]string, 0, len(q.Options))
			for i, opt := range q.Options {
				choices = append(choices, fmt.Sprintf("<code>%d. %s</code>", i+1, escapeHTML(opt)))
			}
			lines = append(lines, "  choices: "+strings.Join(choices, ", "))
		}
		if q.TimeoutSeconds > 0 {
			lines = append(lines, fmt.Sprintf("  timeout: %ds", q.TimeoutSeconds))
		}
		lines = append(lines, renderPendingQuestionHandoffLines(q, directReplyAllowed && q.SessionID == directReplySessionID)...)
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Shell) trySendPendingQuestionsWithButtons(ctx context.Context, principal identity.Principal, text string) (bool, error) {
	sender, ok := s.bot.(buttonMessageSender)
	if !ok {
		return false, nil
	}
	buttons, err := s.pendingQuestionButtonRows(principal)
	if err != nil {
		return false, err
	}
	if len(buttons) == 0 {
		return false, nil
	}
	if err := sender.SendMessageWithButtons(ctx, s.chatID, text, buttons); err != nil {
		return true, s.bot.SendMessage(ctx, s.chatID, text)
	}
	return true, nil
}

func (s *Shell) pendingQuestionButtonRows(principal identity.Principal) ([][]TelegramButton, error) {
	if s.outcomeStore == nil {
		return nil, nil
	}
	records, err := s.outcomeStore.Recent(0)
	if err != nil {
		return nil, err
	}
	directReplySessionID, directReplyAllowed := s.directReplyPendingQuestionSession(records, principal)
	pending := learning.PendingUserQuestions(records, "", 10)
	rows := make([][]TelegramButton, 0)
	for _, question := range pending {
		if !question.Answerable || len(question.Options) == 0 {
			continue
		}
		if directReplyAllowed && question.SessionID != directReplySessionID {
			continue
		}
		for i, option := range question.Options {
			label := fmt.Sprintf("%d. %s", i+1, strings.TrimSpace(option))
			rows = append(rows, []TelegramButton{{
				Text: truncateTelegramButtonLabel(label),
				Data: fmt.Sprintf("uq:%s:%d", question.RequestID, i+1),
			}})
		}
	}
	return rows, nil
}

func truncateTelegramButtonLabel(label string) string {
	label = strings.TrimSpace(label)
	if len([]rune(label)) <= 60 {
		return label
	}
	runes := []rune(label)
	return string(runes[:57]) + "..."
}

func (s *Shell) directReplyPendingQuestionSession(records []learning.OutcomeRecord, principal identity.Principal) (string, bool) {
	if s.binder == nil {
		return "", false
	}
	sessionID, ok := s.binder.Lookup(s.chatID, principal.UserID)
	if !ok {
		return "", false
	}
	pending := learning.PendingUserQuestions(records, sessionID, 2)
	return sessionID, len(pending) == 1
}

func (s *Shell) handleCallbackQuery(ctx context.Context, callback CallbackQuery) error {
	if callback.Message.ChatID != "" && callback.Message.ChatID != s.chatID {
		return nil
	}
	data := strings.TrimSpace(callback.Data)
	if !strings.HasPrefix(data, "uq:") {
		return nil
	}
	parts := strings.Split(data, ":")
	if len(parts) != 3 {
		return s.answerCallbackOrMessage(ctx, callback, "Invalid question choice.")
	}
	requestID := strings.TrimSpace(parts[1])
	choice, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || choice <= 0 {
		return s.answerCallbackOrMessage(ctx, callback, "Invalid question choice.")
	}
	question, ok, err := s.findPendingQuestionByRequest(ctx, requestID)
	if err != nil {
		return err
	}
	if !ok || choice > len(question.Options) {
		return s.answerCallbackOrMessage(ctx, callback, "Question is no longer pending.")
	}
	answer := strings.TrimSpace(question.Options[choice-1])
	principal := identity.Principal{
		UserID:    strings.TrimSpace(callback.FromID),
		ProjectID: "elnath",
		Surface:   "telegram",
	}
	output, err := s.enqueuePendingQuestionAnswer(ctx, question.SessionID, question.RequestID, answer, principal)
	if err != nil {
		return err
	}
	if ack, ok := s.bot.(callbackAcknowledger); ok {
		_ = ack.AnswerCallback(ctx, callback.ID, "Answer queued")
	}
	return s.bot.SendMessage(ctx, s.chatID, renderTelegramQuestionAnswerQueued(question.RequestID, output))
}

func (s *Shell) answerCallbackOrMessage(ctx context.Context, callback CallbackQuery, text string) error {
	if ack, ok := s.bot.(callbackAcknowledger); ok {
		_ = ack.AnswerCallback(ctx, callback.ID, text)
	}
	return s.bot.SendMessage(ctx, s.chatID, "⚠️ "+text)
}

func renderPendingQuestionHandoffLines(q learning.PendingUserQuestion, directReplyAllowed bool) []string {
	if !q.Answerable {
		return []string{"  not answerable: missing session binding; inspect the receipt or ask again from a bound session."}
	}
	sessionID := escapeHTML(q.SessionID)
	requestID := escapeHTML(q.RequestID)
	lines := make([]string, 0, len(q.Options)+2)
	if directReplyAllowed {
		if len(q.Options) > 0 && !q.AllowFreeText {
			choices := make([]string, 0, len(q.Options))
			for i, opt := range q.Options {
				choices = append(choices, fmt.Sprintf("<code>%d. %s</code>", i+1, escapeHTML(opt)))
			}
			lines = append(lines, "  reply directly: "+strings.Join(choices, ", "))
		} else {
			lines = append(lines, "  reply directly with your answer in this chat.")
		}
	}
	for i, opt := range q.Options {
		lines = append(lines, fmt.Sprintf("  choose <code>%s</code>: <code>/answer %s %s %s</code>", escapeHTML(opt), sessionID, requestID, escapeHTML(opt)))
		lines = append(lines, fmt.Sprintf("  choose <code>%d</code>: <code>/answer %s %s %d</code>", i+1, sessionID, requestID, i+1))
	}
	if q.AllowFreeText || len(q.Options) == 0 {
		lines = append(lines, fmt.Sprintf("  free text: <code>/answer %s %s ANSWER_TEXT</code>", sessionID, requestID))
	}
	lines = append(lines, fmt.Sprintf("  cancel: <code>/cancel-question %s %s REASON</code>", sessionID, requestID))
	return lines
}

func (s *Shell) answerPendingQuestion(ctx context.Context, raw string, principal identity.Principal) (string, error) {
	if s.outcomeStore == nil {
		return "Question answers are unavailable in this shell.", nil
	}
	parts := strings.SplitN(raw, " ", 4)
	if len(parts) < 4 || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" || strings.TrimSpace(parts[3]) == "" {
		return "", fmt.Errorf("usage: /answer <session_id> <request_id> <answer>")
	}
	sessionID := strings.TrimSpace(parts[1])
	requestID := strings.TrimSpace(parts[2])
	answer := strings.TrimSpace(parts[3])
	if question, ok, err := s.findPendingQuestion(ctx, sessionID, requestID); err != nil {
		return "", err
	} else if ok {
		if normalized, allowed := normalizePendingQuestionAnswer(question, answer); allowed {
			answer = normalized
		}
	}
	output, err := s.enqueuePendingQuestionAnswer(ctx, sessionID, requestID, answer, principal)
	if err != nil {
		return "", err
	}
	return renderTelegramQuestionAnswerQueued(requestID, output), nil
}

func (s *Shell) handlePendingQuestionText(ctx context.Context, text string, principal identity.Principal) (bool, string, error) {
	if s.outcomeStore == nil || s.binder == nil {
		return false, "", nil
	}
	answer := strings.TrimSpace(text)
	if answer == "" {
		return false, "", nil
	}
	sessionID, ok := s.binder.Lookup(s.chatID, principal.UserID)
	if !ok {
		return false, "", nil
	}
	records, err := s.outcomeStore.Recent(0)
	if err != nil {
		return true, "", err
	}
	pending := learning.PendingUserQuestions(records, sessionID, 2)
	if len(pending) == 0 {
		return false, "", nil
	}
	if len(pending) > 1 {
		return true, "⚠️ Multiple pending questions for this session. Use /questions and answer with /answer <session_id> <request_id> <answer>.", nil
	}
	question := pending[0]
	normalizedAnswer, allowed := normalizePendingQuestionAnswer(question, answer)
	if !allowed {
		return true, fmt.Sprintf("⚠️ Answer does not match pending choices for <code>%s</code>. Use /questions.", escapeHTML(question.RequestID)), nil
	}
	output, err := s.enqueuePendingQuestionAnswer(ctx, question.SessionID, question.RequestID, normalizedAnswer, principal)
	if err != nil {
		return true, "", err
	}
	return true, renderTelegramQuestionAnswerQueued(question.RequestID, output), nil
}

func normalizePendingQuestionAnswer(question learning.PendingUserQuestion, answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}
	for _, option := range question.Options {
		if answer == strings.TrimSpace(option) {
			return strings.TrimSpace(option), true
		}
	}
	if idx, err := strconv.Atoi(answer); err == nil && idx >= 1 && idx <= len(question.Options) {
		return strings.TrimSpace(question.Options[idx-1]), true
	}
	if question.AllowFreeText || len(question.Options) == 0 {
		return answer, true
	}
	return "", false
}

func (s *Shell) findPendingQuestion(ctx context.Context, sessionID, requestID string) (learning.PendingUserQuestion, bool, error) {
	if s.outcomeStore == nil {
		return learning.PendingUserQuestion{}, false, nil
	}
	records, err := s.outcomeStore.Recent(0)
	if err != nil {
		return learning.PendingUserQuestion{}, false, err
	}
	question, ok := learning.FindPendingUserQuestion(records, sessionID, requestID)
	return question, ok, nil
}

func (s *Shell) findPendingQuestionByRequest(ctx context.Context, requestID string) (learning.PendingUserQuestion, bool, error) {
	if s.outcomeStore == nil {
		return learning.PendingUserQuestion{}, false, nil
	}
	records, err := s.outcomeStore.Recent(0)
	if err != nil {
		return learning.PendingUserQuestion{}, false, err
	}
	requestID = strings.TrimSpace(requestID)
	for _, question := range learning.PendingUserQuestions(records, "", 0) {
		if question.RequestID == requestID {
			return question, true, nil
		}
	}
	return learning.PendingUserQuestion{}, false, nil
}

func (s *Shell) enqueuePendingQuestionAnswer(ctx context.Context, sessionID, requestID, answer string, principal identity.Principal) (telegramQuestionAnswerOutput, error) {
	if s.outcomeStore == nil {
		return telegramQuestionAnswerOutput{}, fmt.Errorf("question answers are unavailable in this shell")
	}
	params := map[string]any{
		"session_id":      sessionID,
		"request_id":      requestID,
		"answer":          answer,
		"surface":         "telegram",
		"idempotency_key": identity.KeyFor(principal, "user-question-answer:"+sessionID+":"+requestID+":"+answer),
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		return telegramQuestionAnswerOutput{}, err
	}
	result, err := daemon.NewUserQuestionAnswerToolWithValidator(s.queue, telegramPendingQuestionValidator{store: s.outcomeStore}).Execute(ctx, rawParams)
	if err != nil {
		return telegramQuestionAnswerOutput{}, err
	}
	if result == nil {
		return telegramQuestionAnswerOutput{}, fmt.Errorf("telegram answer: empty tool result")
	}
	if result.IsError {
		return telegramQuestionAnswerOutput{}, fmt.Errorf("%s", result.Output)
	}
	var output telegramQuestionAnswerOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		return telegramQuestionAnswerOutput{}, fmt.Errorf("telegram answer: parse output: %w", err)
	}
	if err := s.recordQuestionAnswerOutcome(principal, output); err != nil {
		return telegramQuestionAnswerOutput{}, err
	}
	return output, nil
}

func renderTelegramQuestionAnswerQueued(requestID string, output telegramQuestionAnswerOutput) string {
	return fmt.Sprintf("✅ Answer queued for <code>%s</code> as task <code>#%d</code> (%d chars).", escapeHTML(requestID), output.TaskID, output.AnswerChars)
}

func (s *Shell) cancelPendingQuestion(ctx context.Context, raw string) (string, error) {
	if s.outcomeStore == nil {
		return "Question cancellation is unavailable in this shell.", nil
	}
	parts := strings.SplitN(raw, " ", 4)
	if len(parts) < 3 || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return "", fmt.Errorf("usage: /cancel-question <session_id> <request_id> [reason]")
	}
	reason := "telegram operator cancelled question"
	if len(parts) == 4 && strings.TrimSpace(parts[3]) != "" {
		reason = strings.TrimSpace(parts[3])
	}
	params := map[string]any{
		"session_id": strings.TrimSpace(parts[1]),
		"request_id": strings.TrimSpace(parts[2]),
		"reason":     reason,
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	result, err := learning.NewUserQuestionCancelTool(s.outcomeStore).Execute(ctx, rawParams)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", fmt.Errorf("telegram cancel-question: empty tool result")
	}
	if result.IsError {
		return "", fmt.Errorf("%s", result.Output)
	}
	var output telegramQuestionCancelOutput
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		return "", fmt.Errorf("telegram cancel-question: parse output: %w", err)
	}
	return fmt.Sprintf("✅ Question cancelled: <code>%s</code>.", escapeHTML(output.RequestID)), nil
}

type telegramPendingQuestionValidator struct {
	store *learning.OutcomeStore
}

func (v telegramPendingQuestionValidator) ValidateUserQuestionAnswer(_ context.Context, sessionID, requestID string) (daemon.UserQuestionAnswerValidation, error) {
	if v.store == nil {
		return daemon.UserQuestionAnswerValidation{}, fmt.Errorf("outcome store unavailable")
	}
	records, err := v.store.Recent(0)
	if err != nil {
		return daemon.UserQuestionAnswerValidation{}, err
	}
	question, ok := learning.FindPendingUserQuestion(records, sessionID, requestID)
	if !ok {
		return daemon.UserQuestionAnswerValidation{}, nil
	}
	return daemon.UserQuestionAnswerValidation{
		Found:         true,
		Question:      question.Question,
		QuestionChars: question.QuestionChars,
		Options:       append([]string(nil), question.Options...),
		AllowFreeText: question.AllowFreeText,
	}, nil
}

type telegramQuestionAnswerOutput struct {
	TaskID      int64                       `json:"task_id"`
	Status      string                      `json:"status"`
	RequestID   string                      `json:"request_id"`
	SessionID   string                      `json:"session_id"`
	AnswerChars int                         `json:"answer_chars"`
	Receipt     learning.ControlToolReceipt `json:"receipt"`
}

type telegramQuestionCancelOutput struct {
	Status    string                      `json:"status"`
	RequestID string                      `json:"request_id"`
	SessionID string                      `json:"session_id"`
	Reason    string                      `json:"reason"`
	Receipt   learning.ControlToolReceipt `json:"receipt"`
}

func (s *Shell) recordQuestionAnswerOutcome(principal identity.Principal, output telegramQuestionAnswerOutput) error {
	if s.outcomeStore == nil {
		return fmt.Errorf("telegram answer: outcome store unavailable")
	}
	projectID := strings.TrimSpace(principal.ProjectID)
	if projectID == "" {
		projectID = "elnath"
	}
	return s.outcomeStore.Append(learning.OutcomeRecord{
		ProjectID:           projectID,
		Intent:              "user_input_answer",
		Workflow:            "telegram_answer",
		FinishReason:        "stop",
		Success:             true,
		SessionID:           output.SessionID,
		ControlToolReceipts: []learning.ControlToolReceipt{output.Receipt},
	})
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
		entry := fmt.Sprintf("• <code>#%d</code> <b>%s</b>\n  <code>%s</code>", req.ID, escapeHTML(req.ToolName), escapeHTML(input))
		var provenance []string
		if req.TaskID != 0 {
			provenance = append(provenance, fmt.Sprintf("Task #%d", req.TaskID))
		}
		if req.PolicyDecisionID != 0 {
			provenance = append(provenance, fmt.Sprintf("Policy #%d", req.PolicyDecisionID))
		}
		if req.RiskLevel != "" {
			provenance = append(provenance, "Risk: "+escapeHTML(req.RiskLevel))
		}
		if req.Reason != "" {
			provenance = append(provenance, escapeHTML(req.Reason))
		}
		if len(provenance) > 0 {
			entry += "\n  " + strings.Join(provenance, " · ")
		}
		lines = append(lines, entry)
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Shell) resolveApproval(ctx context.Context, fields []string, approved bool, principal identity.Principal) (string, error) {
	if len(fields) < 2 {
		return "", fmt.Errorf("usage: %s <id>", fields[0])
	}
	id, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid approval id %q", fields[1])
	}
	if err := s.approvals.DecideBy(ctx, id, approved, approvalDecider(principal)); err != nil {
		return "", err
	}
	if approved {
		return fmt.Sprintf("✅ Approved <code>#%d</code>", id), nil
	}
	return fmt.Sprintf("❌ Denied <code>#%d</code>", id), nil
}

func approvalDecider(principal identity.Principal) string {
	if principal.UserID == "" {
		return "telegram"
	}
	return "telegram:" + principal.UserID
}

func (s *Shell) enqueueTaskReturningID(ctx context.Context, prompt string, principal identity.Principal) (int64, bool, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return 0, false, fmt.Errorf("usage: /submit <message> or just type your message")
	}
	payload := daemon.TaskPayload{
		Prompt:    prompt,
		Surface:   "telegram",
		Principal: principal,
	}
	if s.binder != nil {
		if sessionID, ok := s.binder.Lookup(s.chatID, principal.UserID); ok {
			payload.SessionID = sessionID
		}
	}
	encoded := daemon.EncodeTaskPayload(payload)
	parsed := daemon.ParseTaskPayload(encoded)
	idemKey := identity.KeyFor(payload.Principal, parsed.Prompt)
	id, existed, err := s.queue.Enqueue(ctx, encoded, idemKey)
	if err != nil {
		return 0, false, err
	}
	if existed {
		s.logger.Info("telegram: enqueue deduplicated", "task_id", id, "user_id", payload.Principal.UserID)
	}
	return id, existed, nil
}

func (s *Shell) enqueueNewTask(ctx context.Context, raw string, principal identity.Principal) (string, error) {
	prompt := raw
	if strings.HasPrefix(prompt, "/submit") {
		prompt = strings.TrimSpace(strings.TrimPrefix(prompt, "/submit"))
	}
	id, existed, err := s.enqueueTaskReturningID(ctx, prompt, principal)
	if err != nil {
		return "", err
	}
	if existed {
		return dedupMessage(id), nil
	}
	return fmt.Sprintf("🚀 Task <code>#%d</code> queued", id), nil
}

func (s *Shell) enqueueFollowUp(ctx context.Context, raw string, principal identity.Principal, userMsgID int64) (string, error) {
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) < 3 {
		return "", fmt.Errorf("usage: /followup <session_id> <message>")
	}
	payload := daemon.TaskPayload{
		Prompt:    strings.TrimSpace(parts[2]),
		SessionID: strings.TrimSpace(parts[1]),
		Surface:   "telegram",
		Principal: principal,
	}
	if payload.Prompt == "" || payload.SessionID == "" {
		return "", fmt.Errorf("usage: /followup <session_id> <message>")
	}
	encoded := daemon.EncodeTaskPayload(payload)
	parsed := daemon.ParseTaskPayload(encoded)
	idemKey := identity.KeyFor(payload.Principal, parsed.Prompt)
	id, existed, err := s.queue.Enqueue(ctx, encoded, idemKey)
	if err != nil {
		return "", err
	}
	if existed {
		return dedupMessage(id), nil
	}
	s.trackEnqueuedTask(id, userMsgID, principal.UserID)
	return fmt.Sprintf("🔄 Follow-up <code>#%d</code> queued for session <code>%s</code>", id, payload.SessionID[:8]), nil
}

func (s *Shell) trackEnqueuedTask(taskID, userMsgID int64, userID string) {
	if s.taskTracker == nil {
		return
	}
	if userMsgID > 0 {
		s.taskTracker.TrackUserMessage(taskID, userMsgID)
	}
	if tracker, ok := s.taskTracker.(ChatBindingTracker); ok {
		tracker.TrackChatBinding(taskID, userID)
	}
}

func dedupMessage(taskID int64) string {
	return fmt.Sprintf("이미 처리 중입니다 (#%d)", taskID)
}

func (s *Shell) principalForMessage(message Message) identity.Principal {
	fromID, _ := strconv.ParseInt(strings.TrimSpace(message.UserID), 10, 64)
	return identity.ResolveTelegramPrincipal(fromID, s.workDir)
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

func telegramHandoffCommand(taskID int64, sessionID string) string {
	if taskID <= 0 || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	return fmt.Sprintf("/handoff %d", taskID)
}
