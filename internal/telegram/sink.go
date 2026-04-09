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

const streamingCursor = " ▍"
const maxMessageLen = 4000

var stageIcons = map[string]string{
	"plan":     "📋",
	"code":     "💻",
	"test":     "🧪",
	"review":   "📝",
	"verify":   "✔️",
	"research": "🔍",
}

type TelegramSink struct {
	bot    BotClient
	chatID string
	logger *slog.Logger

	mu       sync.Mutex
	tracking map[int64]*trackedMessage
}

type trackedMessage struct {
	messageID     int64
	userMessageID int64
	lastText      string
	lastEditAt    time.Time
	editPending   bool
	stages        []string
	currentStage  string
	toolCalls     int
}

func NewTelegramSink(bot BotClient, chatID string, logger *slog.Logger) *TelegramSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &TelegramSink{
		bot:      bot,
		chatID:   chatID,
		logger:   logger,
		tracking: make(map[int64]*trackedMessage),
	}
}

func (s *TelegramSink) TrackUserMessage(taskID int64, userMessageID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tracked := s.tracking[taskID]
	if tracked == nil {
		tracked = &trackedMessage{}
		s.tracking[taskID] = tracked
	}
	tracked.userMessageID = userMessageID
}

func (s *TelegramSink) NotifyCompletion(_ context.Context, c daemon.TaskCompletion) error {
	s.mu.Lock()
	tracked := s.tracking[c.TaskID]
	delete(s.tracking, c.TaskID)
	s.mu.Unlock()

	icon := "✅"
	label := "Complete"
	if c.Status == daemon.StatusFailed {
		icon = "❌"
		label = "Failed"
	}

	summary := condenseSummary(emptyFallback(c.Summary, "-"))

	elapsed := ""
	if !c.StartedAt.IsZero() && !c.CompletedAt.IsZero() {
		d := c.CompletedAt.Sub(c.StartedAt)
		if d >= time.Minute {
			elapsed = fmt.Sprintf(" (%dm%ds)", int(d.Minutes()), int(d.Seconds())%60)
		} else {
			elapsed = fmt.Sprintf(" (%ds)", int(d.Seconds()))
		}
	}

	stageBar := ""
	if tracked != nil && len(tracked.stages) > 0 {
		stageBar = renderStageBar(tracked.stages, "") + "\n\n"
	}

	text := fmt.Sprintf("%s <b>%s</b>%s <code>#%d</code>\n\n%s%s", icon, label, elapsed, c.TaskID, stageBar, summary)

	ctx := context.Background()

	if tracked != nil && tracked.userMessageID > 0 {
		emoji := "✅"
		if c.Status == daemon.StatusFailed {
			emoji = "❌"
		}
		_ = s.bot.SetReaction(ctx, s.chatID, tracked.userMessageID, emoji)
	}

	if tracked != nil && tracked.messageID > 0 {
		return s.bot.EditMessage(ctx, s.chatID, tracked.messageID, text)
	}
	return s.bot.SendMessage(ctx, s.chatID, text)
}

func (s *TelegramSink) OnProgress(taskID int64, progress string) {
	rendered := daemon.RenderProgress(progress)
	if rendered == "" {
		return
	}

	s.mu.Lock()
	tracked := s.tracking[taskID]
	if tracked == nil {
		tracked = &trackedMessage{}
		s.tracking[taskID] = tracked
	}

	stage, isStage := parseStageMarker(rendered)
	if isStage {
		tracked.currentStage = stage
		if !containsString(tracked.stages, stage) {
			tracked.stages = append(tracked.stages, stage)
		}
		tracked.toolCalls = 0
	} else {
		tracked.toolCalls++
	}

	bar := renderStageBar(tracked.stages, tracked.currentStage)
	activity := renderActivity(tracked.toolCalls)
	text := fmt.Sprintf("⚡ <b>Running</b> <code>#%d</code>\n\n%s\n%s", taskID, bar, activity)

	if text == tracked.lastText {
		s.mu.Unlock()
		return
	}
	tracked.lastText = text

	minInterval := time.Second
	if time.Since(tracked.lastEditAt) < minInterval {
		if !tracked.editPending {
			tracked.editPending = true
			delay := minInterval - time.Since(tracked.lastEditAt)
			go s.deferredEdit(taskID, delay)
		}
		s.mu.Unlock()
		return
	}

	tracked.lastEditAt = time.Now()
	msgID := tracked.messageID
	s.mu.Unlock()

	s.sendOrEdit(taskID, msgID, text)
}

func (s *TelegramSink) deferredEdit(taskID int64, delay time.Duration) {
	time.Sleep(delay)

	s.mu.Lock()
	tracked := s.tracking[taskID]
	if tracked == nil {
		s.mu.Unlock()
		return
	}
	tracked.editPending = false
	tracked.lastEditAt = time.Now()
	text := tracked.lastText
	msgID := tracked.messageID
	s.mu.Unlock()

	s.sendOrEdit(taskID, msgID, text)
}

func (s *TelegramSink) sendOrEdit(taskID, msgID int64, text string) {
	ctx := context.Background()
	if msgID > 0 {
		if err := s.bot.EditMessage(ctx, s.chatID, msgID, text); err != nil {
			if !isMessageNotModified(err) {
				s.logger.Warn("telegram sink: edit message", "task_id", taskID, "error", err)
			}
		}
		return
	}

	newID, err := s.bot.SendMessageReturningID(ctx, s.chatID, text)
	if err != nil {
		s.logger.Warn("telegram sink: send message", "task_id", taskID, "error", err)
		return
	}

	s.mu.Lock()
	if tracked := s.tracking[taskID]; tracked != nil {
		tracked.messageID = newID
	}
	s.mu.Unlock()
}

func (s *TelegramSink) String() string {
	return "TelegramSink"
}

func renderStageBar(stages []string, current string) string {
	var sb strings.Builder
	for i, stage := range stages {
		icon, ok := stageIcons[stage]
		if !ok {
			icon = "▸"
		}
		if stage == current {
			sb.WriteString(fmt.Sprintf("<b>%s %s</b>", icon, stage))
		} else {
			sb.WriteString(fmt.Sprintf("<s>%s %s</s>", icon, stage))
		}
		if i < len(stages)-1 {
			sb.WriteString("  →  ")
		}
	}
	return sb.String()
}

func renderActivity(toolCalls int) string {
	dots := toolCalls % 4
	animation := strings.Repeat("●", dots+1) + strings.Repeat("○", 3-dots)
	return fmt.Sprintf("<i>%s working%s</i>", animation, streamingCursor)
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

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func condenseSummary(raw string) string {
	raw = strings.TrimSpace(raw)
	lines := strings.Split(raw, "\n")

	var result []string
	totalLen := 0
	const maxSummaryLen = 500

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

func isMessageNotModified(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

func escapeHTML(s string) string {
	return htmlReplacer.Replace(s)
}
