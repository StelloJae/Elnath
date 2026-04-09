package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/stello/elnath/internal/daemon"
)

// TelegramSink delivers task completions and streams progress updates to a
// Telegram chat. It implements daemon.CompletionSink and daemon.ProgressObserver.
type TelegramSink struct {
	bot    BotClient
	chatID string
	logger *slog.Logger

	mu       sync.Mutex
	tracking map[int64]*trackedMessage
}

type trackedMessage struct {
	messageID   int64
	lastText    string
	lastEditAt  time.Time
	editPending bool
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

func (s *TelegramSink) NotifyCompletion(_ context.Context, c daemon.TaskCompletion) error {
	s.mu.Lock()
	tracked := s.tracking[c.TaskID]
	delete(s.tracking, c.TaskID)
	s.mu.Unlock()

	text := fmt.Sprintf("Task #%d %s\nsummary: %s",
		c.TaskID, c.Status, emptyFallback(c.Summary, "-"))

	if tracked != nil && tracked.messageID > 0 {
		return s.bot.EditMessage(context.Background(), s.chatID, tracked.messageID, text)
	}
	return s.bot.SendMessage(context.Background(), s.chatID, text)
}

func (s *TelegramSink) OnProgress(taskID int64, progress string) {
	rendered := daemon.RenderProgress(progress)
	if rendered == "" {
		return
	}
	if len(rendered) > 4000 {
		rendered = rendered[:3997] + "..."
	}
	text := fmt.Sprintf("Task #%d running\n%s", taskID, rendered)

	s.mu.Lock()
	tracked := s.tracking[taskID]
	if tracked == nil {
		tracked = &trackedMessage{}
		s.tracking[taskID] = tracked
	}

	if text == tracked.lastText {
		s.mu.Unlock()
		return
	}
	tracked.lastText = text

	minInterval := 2 * time.Second
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
			s.logger.Warn("telegram sink: edit message", "task_id", taskID, "error", err)
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
