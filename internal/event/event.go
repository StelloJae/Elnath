package event

import "time"

// Event is the base interface every typed event must satisfy.
type Event interface {
	EventType() string
	Timestamp() time.Time
}

// Sink accepts emitted events (e.g. a renderer, logger, or bus).
type Sink interface {
	Emit(Event)
}

// Observer receives events pushed to it.
type Observer interface {
	OnEvent(Event)
}

// Base carries the fields common to every concrete event.
type Base struct {
	ts        time.Time
	sessionID string
}

func (b Base) Timestamp() time.Time { return b.ts }
func (b Base) SessionID() string    { return b.sessionID }

// NewBase returns a Base stamped with the current time.
func NewBase() Base { return Base{ts: time.Now()} }

// NewBaseWith returns a Base with an explicit timestamp and session ID.
func NewBaseWith(ts time.Time, sessionID string) Base {
	return Base{ts: ts, sessionID: sessionID}
}
