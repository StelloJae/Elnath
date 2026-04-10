package telegram

import (
	"context"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	streamEditInterval    = 300 * time.Millisecond
	streamBufferThreshold = 40
	streamCursor          = " ▉"
	streamMaxLen          = 3800
)

type StreamConsumer struct {
	bot    BotClient
	chatID string
	logger *slog.Logger

	ch        chan string
	seg       chan struct{}
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once

	mu          sync.Mutex
	alreadySent bool
}

func NewStreamConsumer(bot BotClient, chatID string, logger *slog.Logger) *StreamConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &StreamConsumer{
		bot:    bot,
		chatID: chatID,
		logger: logger,
		ch:     make(chan string, 256),
		seg:    make(chan struct{}, 8),
		done:   make(chan struct{}),
	}
}

func (sc *StreamConsumer) Send(text string) {
	sc.ch <- text
}

func (sc *StreamConsumer) NewSegment() {
	sc.seg <- struct{}{}
}

func (sc *StreamConsumer) Finish() {
	sc.closeOnce.Do(func() { close(sc.done) })
}

func (sc *StreamConsumer) Wait() {
	sc.wg.Wait()
}

func (sc *StreamConsumer) AlreadySent() bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.alreadySent
}

func (sc *StreamConsumer) Run() {
	sc.wg.Add(1)
	go sc.loop()
}

func (sc *StreamConsumer) loop() {
	defer sc.wg.Done()

	var (
		buf      []byte
		msgID    int64
		lastSent string
		lastTime time.Time
		finished bool
		segment  bool
	)

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sc.done:
			finished = true
		case <-sc.seg:
			segment = true
		case delta := <-sc.ch:
			buf = append(buf, delta...)
			continue
		case <-ticker.C:
		}

		// Drain all pending deltas.
		for {
			select {
			case delta := <-sc.ch:
				buf = append(buf, delta...)
			default:
				goto drained
			}
		}
	drained:

		// Check for pending segment signals.
		for {
			select {
			case <-sc.seg:
				segment = true
			default:
				goto segDrained
			}
		}
	segDrained:

		// Check for done signal.
		select {
		case <-sc.done:
			finished = true
		default:
		}

		shouldFlush := finished ||
			segment ||
			(len(buf) > 0 && time.Since(lastTime) >= streamEditInterval) ||
			utf8.RuneCount(buf) >= streamBufferThreshold

		if !shouldFlush {
			continue
		}

		if len(buf) == 0 && !finished && !segment {
			continue
		}

		text := string(buf)
		if runes := []rune(text); len(runes) > streamMaxLen {
			text = string(runes[:streamMaxLen])
		}

		if segment {
			sc.flush(text, &msgID, &lastSent)
			msgID = 0
			buf = buf[:0]
			lastSent = ""
			lastTime = time.Now()
			segment = false
			continue
		}

		if finished {
			sc.flush(text, &msgID, &lastSent)
			return
		}

		sc.flush(text+streamCursor, &msgID, &lastSent)
		lastTime = time.Now()
	}
}

func (sc *StreamConsumer) flush(text string, msgID *int64, lastSent *string) {
	if text == "" {
		return
	}
	if text == *lastSent {
		return
	}

	ctx := context.Background()

	if *msgID > 0 {
		if err := sc.bot.EditMessage(ctx, sc.chatID, *msgID, text); err != nil {
			if !isMessageNotModifiedError(err) {
				sc.logger.Warn("stream consumer: edit failed", "error", err, "msg_id", *msgID)
			}
			return
		}
	} else {
		newID, err := sc.bot.SendMessageReturningID(ctx, sc.chatID, text)
		if err != nil {
			sc.logger.Warn("stream consumer: send failed", "error", err)
			return
		}
		*msgID = newID
	}

	*lastSent = text
	sc.mu.Lock()
	sc.alreadySent = true
	sc.mu.Unlock()
}
