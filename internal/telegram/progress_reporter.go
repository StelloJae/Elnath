package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const progressEditInterval = 1500 * time.Millisecond

var toolEmojis = map[string]string{
	"bash":       "🔧",
	"file_write": "📝",
	"file_read":  "📖",
	"file_edit":  "✏️",
	"git":        "📦",
	"web_search": "🔍",
	"wiki":       "📚",
}

var prStageIcons = map[string]string{
	"plan": "📋", "code": "💻", "test": "🧪",
	"review": "📝", "verify": "✔️", "research": "🔍", "summary": "✨",
}

var prStageNames = map[string]string{
	"plan": "Planning", "code": "Coding", "test": "Testing",
	"review": "Reviewing", "verify": "Verifying", "research": "Researching", "summary": "Summarizing",
}

type toolEvent struct {
	name    string
	preview string
}

type stageEvent struct {
	stage string
}

type ProgressReporter struct {
	bot    BotClient
	chatID string
	logger *slog.Logger

	toolCh  chan toolEvent
	stageCh chan stageEvent
	done    chan struct{}
	wg      sync.WaitGroup
	closeOnce sync.Once

	mu    sync.Mutex
	msgID int64
}

func NewProgressReporter(bot BotClient, chatID string, logger *slog.Logger) *ProgressReporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProgressReporter{
		bot:    bot,
		chatID: chatID,
		logger: logger,
		toolCh:  make(chan toolEvent, 256),
		stageCh: make(chan stageEvent, 32),
		done:    make(chan struct{}),
	}
}

func (pr *ProgressReporter) ReportTool(name, preview string) {
	pr.toolCh <- toolEvent{name: name, preview: preview}
}

func (pr *ProgressReporter) ReportStage(stage string) {
	pr.stageCh <- stageEvent{stage: stage}
}

func (pr *ProgressReporter) Finish() {
	pr.closeOnce.Do(func() { close(pr.done) })
}

func (pr *ProgressReporter) Wait() {
	pr.wg.Wait()
}

func (pr *ProgressReporter) MessageID() int64 {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	return pr.msgID
}

func (pr *ProgressReporter) Run() {
	pr.wg.Add(1)
	go pr.loop()
}

func (pr *ProgressReporter) loop() {
	defer pr.wg.Done()

	var (
		lines     []string
		dirty     bool
		lastFlush time.Time
		finished  bool
	)

	for {
		// Step 1: Wait for a new event (blocking) when nothing is pending.
		if !dirty && !finished {
			select {
			case ev := <-pr.toolCh:
				appendToolLine(&lines, ev.name, ev.preview)
				dirty = true
			case ev := <-pr.stageCh:
				appendStageLine(&lines, ev.stage)
				dirty = true
			case <-pr.done:
				finished = true
			}
		}

		// Step 2: Drain all queued events (non-blocking).
		pr.drain(&lines, &dirty, &finished)

		if !dirty && finished {
			return
		}

		// Step 3: Throttle — if less than progressEditInterval since last flush,
		// wait but keep accepting new events.
		if elapsed := time.Since(lastFlush); elapsed < progressEditInterval && !finished {
			remaining := progressEditInterval - elapsed
			timer := time.NewTimer(remaining)
		waitLoop:
			for {
				select {
				case ev := <-pr.toolCh:
					appendToolLine(&lines, ev.name, ev.preview)
					dirty = true
				case ev := <-pr.stageCh:
					appendStageLine(&lines, ev.stage)
					dirty = true
				case <-pr.done:
					finished = true
					timer.Stop()
					break waitLoop
				case <-timer.C:
					break waitLoop
				}
			}
			pr.drain(&lines, &dirty, &finished)
		}

		// Step 4: Flush.
		if dirty {
			text := strings.Join(lines, "\n")
			pr.flush(text)
			lastFlush = time.Now()
			dirty = false
		}

		if finished {
			return
		}
	}
}

func (pr *ProgressReporter) drain(lines *[]string, dirty, finished *bool) {
	for {
		select {
		case ev := <-pr.toolCh:
			appendToolLine(lines, ev.name, ev.preview)
			*dirty = true
		case ev := <-pr.stageCh:
			appendStageLine(lines, ev.stage)
			*dirty = true
		default:
			// Check done non-blockingly only if not already finished,
			// to avoid spinning on a closed channel.
			if !*finished {
				select {
				case <-pr.done:
					*finished = true
				default:
				}
			}
			return
		}
	}
}

func (pr *ProgressReporter) flush(text string) {
	ctx := context.Background()

	pr.mu.Lock()
	id := pr.msgID
	pr.mu.Unlock()

	if id > 0 {
		if err := pr.bot.EditMessage(ctx, pr.chatID, id, text); err != nil {
			if !isMessageNotModifiedError(err) {
				pr.logger.Warn("progress reporter: edit failed", "error", err, "msg_id", id)
			}
		}
		return
	}

	newID, err := pr.bot.SendMessageReturningID(ctx, pr.chatID, text)
	if err != nil {
		pr.logger.Warn("progress reporter: send failed", "error", err)
		return
	}

	pr.mu.Lock()
	pr.msgID = newID
	pr.mu.Unlock()
}

func appendToolLine(lines *[]string, name, preview string) {
	emoji := toolEmojis[name]
	if emoji == "" {
		emoji = "⚙️"
	}

	preview = escapeHTML(preview)
	if len([]rune(preview)) > 40 {
		preview = string([]rune(preview)[:40])
	}

	line := fmt.Sprintf("%s %s: \"%s\"", emoji, name, preview)

	if len(*lines) > 0 {
		last := (*lines)[len(*lines)-1]
		base, count := parseDedup(last)
		if base == line {
			(*lines)[len(*lines)-1] = fmt.Sprintf("%s (×%d)", base, count+1)
			return
		}
	}

	*lines = append(*lines, line)
}

func appendStageLine(lines *[]string, stage string) {
	icon := prStageIcons[stage]
	if icon == "" {
		icon = "▸"
	}
	name := prStageNames[stage]
	if name == "" {
		name = stage
	}
	*lines = append(*lines, fmt.Sprintf("<b>%s %s</b>", icon, name))
}

func parseDedup(line string) (base string, count int) {
	if !strings.HasSuffix(line, ")") {
		return line, 1
	}
	idx := strings.LastIndex(line, " (×")
	if idx < 0 {
		return line, 1
	}
	numStr := line[idx+len(" (×") : len(line)-1]
	n := 0
	for _, c := range numStr {
		if c < '0' || c > '9' {
			return line, 1
		}
		n = n*10 + int(c-'0')
	}
	if n < 2 {
		return line, 1
	}
	return line[:idx], n
}
