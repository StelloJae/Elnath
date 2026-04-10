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
	"summary":  "✨",
}

var stageActiveNames = map[string]string{
	"plan":     "Planning",
	"code":     "Coding",
	"test":     "Testing",
	"review":   "Reviewing",
	"verify":   "Verifying",
	"research": "Researching",
	"summary":  "Summarizing",
}

var stageCompletedNames = map[string]string{
	"plan":     "Plan",
	"code":     "Code",
	"test":     "Test",
	"review":   "Review",
	"verify":   "Verify",
	"research": "Research",
	"summary":  "Summary",
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
	toolCalls       int
	lastActivity    string
	summaryText     string
	summaryStreamed bool
	heartbeatStop   chan struct{}
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
	if tracked != nil && tracked.heartbeatStop != nil {
		close(tracked.heartbeatStop)
	}
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

	stages := filterStages(tracked)
	stageBar := ""
	if len(stages) > 0 {
		stageBar = renderStageBar(stages, "", 0) + "\n\n"
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

	if summaryText, ok := parseSummaryStream(rendered); ok {
		if !tracked.summaryStreamed && tracked.heartbeatStop != nil {
			close(tracked.heartbeatStop)
			tracked.heartbeatStop = nil
		}
		tracked.summaryText = summaryText
		tracked.summaryStreamed = true
		bar := renderStageBar(filterStages(tracked), "", 0)
		text := fmt.Sprintf("✅ <b>Complete</b> <code>#%d</code>\n\n%s\n\n%s%s",
			taskID, bar, escapeHTML(summaryText), streamingCursor)

		if text == tracked.lastText {
			s.mu.Unlock()
			return
		}
		tracked.lastText = text

		minInterval := 300 * time.Millisecond
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
		return
	}

	stage, isStage := parseStageMarker(rendered)
	if isStage {
		if tracked.heartbeatStop != nil {
			close(tracked.heartbeatStop)
		}
		tracked.heartbeatStop = make(chan struct{})
		tracked.currentStage = stage
		if !containsString(tracked.stages, stage) {
			tracked.stages = append(tracked.stages, stage)
		}
		tracked.toolCalls = 0
		go s.stageHeartbeat(taskID, tracked.heartbeatStop)
	} else {
		tracked.toolCalls++
		preview := rendered
		if len(preview) > 50 {
			preview = preview[:50] + "…"
		}
		tracked.lastActivity = preview
	}

	bar := renderStageBar(tracked.stages, tracked.currentStage, tracked.toolCalls)
	circles := renderStageProgress(tracked.stages, tracked.currentStage)
	text := fmt.Sprintf("⚡ <b>Running</b> <code>#%d</code>\n\n%s\n%s", taskID, circles, bar)

	if text == tracked.lastText {
		s.mu.Unlock()
		return
	}
	tracked.lastText = text

	minInterval := 1500 * time.Millisecond
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

func (s *TelegramSink) stageHeartbeat(taskID int64, stop chan struct{}) {
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.mu.Lock()
			tracked := s.tracking[taskID]
			if tracked == nil || tracked.summaryStreamed {
				s.mu.Unlock()
				return
			}
			tracked.toolCalls++
			stages := filterStages(tracked)
			bar := renderStageBar(stages, tracked.currentStage, tracked.toolCalls)
			circles := renderStageProgress(stages, tracked.currentStage)
			text := fmt.Sprintf("⚡ <b>Running</b> <code>#%d</code>\n\n%s\n%s", taskID, circles, bar)

			if text == tracked.lastText {
				s.mu.Unlock()
				continue
			}
			tracked.lastText = text
			tracked.lastEditAt = time.Now()
			msgID := tracked.messageID
			s.mu.Unlock()

			s.sendOrEdit(taskID, msgID, text)
		}
	}
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

func renderStageBar(stages []string, current string, toolCalls int) string {
	var sb strings.Builder
	for _, stage := range stages {
		icon := stageIcons[stage]
		if icon == "" {
			icon = "▸"
		}
		if current != "" && stage == current {
			name := stageActiveNames[stage]
			if name == "" {
				name = stage
			}
			dots := strings.Repeat(".", (toolCalls%3)+1)
			sb.WriteString(fmt.Sprintf("<b>%s %s %s</b>\n", icon, name, dots))
		} else {
			name := stageCompletedNames[stage]
			if name == "" {
				name = stage
			}
			sb.WriteString(fmt.Sprintf("%s %s ✓\n", icon, name))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func renderStageProgress(stages []string, current string) string {
	completed := 0
	total := 0
	for _, s := range stages {
		if s == "summary" {
			continue
		}
		total++
		if s != current || current == "" {
			completed++
		}
	}
	if total == 0 {
		return ""
	}
	return strings.Repeat("●", completed) + strings.Repeat("○", total-completed)
}

func parseSummaryStream(s string) (string, bool) {
	const prefix = "[summary] "
	if strings.HasPrefix(s, prefix) {
		return strings.TrimPrefix(s, prefix), true
	}
	return "", false
}

func filterStages(tracked *trackedMessage) []string {
	if tracked == nil {
		return nil
	}
	var out []string
	for _, s := range tracked.stages {
		if s != "summary" {
			out = append(out, s)
		}
	}
	return out
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
