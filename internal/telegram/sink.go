package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

const maxMessageLen = 4000

type activeTask struct {
	userMsgID    int64
	progress     *ProgressReporter
	stream       *StreamConsumer
	reactionSent bool
}

type TelegramSink struct {
	bot    BotClient
	chatID string
	logger *slog.Logger

	mu     sync.Mutex
	active map[int64]*activeTask
}

func NewTelegramSink(bot BotClient, chatID string, logger *slog.Logger) *TelegramSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &TelegramSink{
		bot:    bot,
		chatID: chatID,
		logger: logger,
		active: make(map[int64]*activeTask),
	}
}

func (s *TelegramSink) TrackUserMessage(taskID, userMsgID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.ensureTask(taskID)
	task.userMsgID = userMsgID
}

func (s *TelegramSink) OnToolProgress(taskID int64, toolName, preview string) {
	s.mu.Lock()
	task := s.ensureTask(taskID)
	pr := task.progress
	s.mu.Unlock()

	pr.ReportTool(toolName, preview)
}

func (s *TelegramSink) OnStreamDelta(taskID int64, text string) {
	s.mu.Lock()
	task := s.ensureTask(taskID)
	sc := task.stream
	s.mu.Unlock()

	sc.Send(text)
}

func (s *TelegramSink) OnStreamDone(taskID int64) {
	s.mu.Lock()
	task := s.active[taskID]
	s.mu.Unlock()

	if task != nil && task.stream != nil {
		task.stream.Finish()
		task.stream.Wait()
	}
}

// OnProgress implements daemon.ProgressObserver. Parses the legacy progress
// format and routes to either ProgressReporter or StreamConsumer.
func (s *TelegramSink) OnProgress(taskID int64, progress string) {
	rendered := daemon.RenderProgress(progress)
	if rendered == "" {
		return
	}

	// Ensure task exists on first progress so NotifyCompletion can find it.
	s.mu.Lock()
	s.ensureTask(taskID)
	s.mu.Unlock()

	s.maybeSetWorkingReaction(taskID)

	if text, ok := parseSummaryStream(rendered); ok {
		s.OnStreamDelta(taskID, text)
		return
	}

	if stage, ok := parseStageMarker(rendered); ok {
		s.mu.Lock()
		task := s.ensureTask(taskID)
		pr := task.progress
		s.mu.Unlock()

		pr.ReportStage(stage)
		return
	}

	// Unrecognized text (LLM response tokens, workflow routing, etc.)
	// is intentionally dropped — only summary and stage markers are
	// routed. Actual tool call events require structured progress
	// events which will be added in a future iteration.
}

func (s *TelegramSink) maybeSetWorkingReaction(taskID int64) {
	s.mu.Lock()
	task := s.active[taskID]
	if task == nil || task.reactionSent || task.userMsgID == 0 {
		s.mu.Unlock()
		return
	}
	task.reactionSent = true
	userMsgID := task.userMsgID
	s.mu.Unlock()

	_ = s.bot.SetReaction(context.Background(), s.chatID, userMsgID, "✍")
}

func (s *TelegramSink) NotifyCompletion(_ context.Context, c daemon.TaskCompletion) error {
	s.mu.Lock()
	task := s.active[c.TaskID]
	delete(s.active, c.TaskID)
	s.mu.Unlock()

	if task == nil {
		return nil
	}

	task.progress.Finish()
	task.progress.Wait()

	ctx := context.Background()
	if task.userMsgID > 0 {
		emoji := "👍"
		if c.Status == daemon.StatusFailed {
			emoji = "😢"
		}
		_ = s.bot.SetReaction(ctx, s.chatID, task.userMsgID, emoji)
	}

	icon := "✅"
	label := "Complete"
	if c.Status == daemon.StatusFailed {
		icon = "❌"
		label = "Failed"
	}

	elapsed := formatElapsed(c.StartedAt, c.CompletedAt)
	header := fmt.Sprintf("%s <b>%s</b>%s <code>#%d</code>", icon, label, elapsed, c.TaskID)
	if prMsgID := task.progress.MessageID(); prMsgID > 0 {
		_ = s.bot.EditMessage(ctx, s.chatID, prMsgID, header)
	} else {
		_ = s.bot.SendMessage(ctx, s.chatID, header)
	}

	if !task.stream.AlreadySent() {
		summary := condenseSummary(emptyFallback(c.Summary, "-"))
		if summary != "" {
			_ = s.bot.SendMessage(ctx, s.chatID, summary)
		}
	} else {
		task.stream.Finish()
		task.stream.Wait()
	}

	return nil
}

func (s *TelegramSink) String() string {
	return "TelegramSink"
}

// ensureTask must be called with s.mu held.
func (s *TelegramSink) ensureTask(taskID int64) *activeTask {
	task := s.active[taskID]
	if task != nil {
		return task
	}

	task = &activeTask{
		progress: NewProgressReporter(s.bot, s.chatID, s.logger),
		stream:   NewStreamConsumer(s.bot, s.chatID, s.logger),
	}
	task.progress.Run()
	task.stream.Run()
	s.active[taskID] = task
	return task
}

// --- Utilities ---

func parseSummaryStream(s string) (string, bool) {
	const prefix = "[summary] "
	if strings.HasPrefix(s, prefix) {
		return strings.TrimPrefix(s, prefix), true
	}
	return "", false
}

func parseStageMarker(s string) (string, bool) {
	for _, prefix := range []string{"[autopilot] stage: ", "[team] ", "[research] "} {
		if strings.HasPrefix(s, prefix) {
			stage := strings.TrimSpace(strings.TrimPrefix(s, prefix))
			stage = strings.TrimSuffix(stage, "\n")
			if stage != "" {
				return stage, true
			}
		}
	}
	return "", false
}

func parseToolProgress(s string) (name, preview string) {
	s = strings.TrimSpace(s)
	if len(s) > 50 {
		s = s[:50]
	}
	return "tool", s
}

func formatElapsed(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	d := end.Sub(start)
	if d >= time.Minute {
		return fmt.Sprintf(" (%dm%ds)", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf(" (%ds)", int(d.Seconds()))
}

func condenseSummary(raw string) string {
	raw = strings.TrimSpace(raw)
	lines := strings.Split(raw, "\n")

	var result []string
	totalLen := 0
	const maxSummaryLen = 3500

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "##") || strings.HasPrefix(line, "---") {
			continue
		}
		escaped := escapeHTML(line)
		if totalLen+len(escaped) > maxSummaryLen {
			result = append(result, escaped[:min(len(escaped), maxSummaryLen-totalLen)]+"…")
			break
		}
		result = append(result, escaped)
		totalLen += len(escaped) + 1
	}

	if len(result) == 0 {
		return escapeHTML(raw[:min(len(raw), maxSummaryLen)])
	}
	return strings.Join(result, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

func escapeHTML(s string) string {
	return htmlReplacer.Replace(s)
}
