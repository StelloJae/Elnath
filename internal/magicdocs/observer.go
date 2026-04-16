package magicdocs

import (
	"log/slog"
	"sync"
	"time"

	"github.com/stello/elnath/internal/event"
)

type AccumulatorObserver struct {
	mu        sync.Mutex
	buffer    []event.Event
	extractCh chan<- ExtractionRequest
	sessionID string
	logger    *slog.Logger
}

func NewAccumulatorObserver(ch chan<- ExtractionRequest, sessionID string, logger *slog.Logger) *AccumulatorObserver {
	return &AccumulatorObserver{
		extractCh: ch,
		sessionID: sessionID,
		logger:    logger,
	}
}

func (a *AccumulatorObserver) OnEvent(e event.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.buffer = append(a.buffer, e)

	if isTrigger(e) {
		snapshot := make([]event.Event, len(a.buffer))
		copy(snapshot, a.buffer)
		a.buffer = a.buffer[:0]

		select {
		case a.extractCh <- ExtractionRequest{
			Events:    snapshot,
			SessionID: a.sessionID,
			Trigger:   e.EventType(),
			Timestamp: time.Now(),
		}:
		default:
			a.logger.Warn("magic-docs extraction channel full, dropping request",
				"trigger", e.EventType(),
				"buffered_events", len(snapshot),
			)
		}
	}
}

func isTrigger(e event.Event) bool {
	switch ev := e.(type) {
	case event.AgentFinishEvent:
		return true
	case event.ResearchProgressEvent:
		return ev.Phase == "conclusion" || ev.Phase == "synthesis"
	case event.SkillExecuteEvent:
		return ev.Status == "done"
	case event.DaemonTaskEvent:
		return ev.Status == "done"
	default:
		return false
	}
}
