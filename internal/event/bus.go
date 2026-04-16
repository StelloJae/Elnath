package event

import "sync"

// ObserverFunc adapts a plain function to the Observer interface.
type ObserverFunc func(Event)

func (f ObserverFunc) OnEvent(e Event) { f(e) }

// Bus fans out events to registered observers. Safe for concurrent use.
type Bus struct {
	observers []Observer
	mu        sync.RWMutex
}

func NewBus() *Bus {
	return &Bus{}
}

// Subscribe registers an observer.
func (b *Bus) Subscribe(o Observer) {
	b.mu.Lock()
	b.observers = append(b.observers, o)
	b.mu.Unlock()
}

// Emit sends an event to all registered observers.
// Uses copy-on-read: copies observer slice under RLock, releases lock,
// then iterates the copy. Prevents deadlock if observer calls Subscribe.
// Each observer is wrapped in panic recovery.
func (b *Bus) Emit(e Event) {
	b.mu.RLock()
	snapshot := make([]Observer, len(b.observers))
	copy(snapshot, b.observers)
	b.mu.RUnlock()

	for _, o := range snapshot {
		func() {
			defer func() { recover() }()
			o.OnEvent(e)
		}()
	}
}
