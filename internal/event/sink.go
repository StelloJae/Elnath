package event

import "sync"

// NopSink discards all events.
type NopSink struct{}

func (NopSink) Emit(Event) {}

// RecorderSink records all emitted events. Safe for concurrent use.
type RecorderSink struct {
	mu     sync.Mutex
	Events []Event
}

func (r *RecorderSink) Emit(e Event) {
	r.mu.Lock()
	r.Events = append(r.Events, e)
	r.mu.Unlock()
}

// EventsOfType returns all events in r that are of type T.
func EventsOfType[T Event](r *RecorderSink) []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []T
	for _, e := range r.Events {
		if typed, ok := e.(T); ok {
			out = append(out, typed)
		}
	}
	return out
}
