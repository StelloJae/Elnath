package event

import (
	"sync"
	"testing"
)

type recordingObserver struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordingObserver) OnEvent(e Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recordingObserver) received() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

func TestBusEmitToSingleObserver(t *testing.T) {
	b := NewBus()
	obs := &recordingObserver{}
	b.Subscribe(obs)

	e := TextDeltaEvent{Base: NewBase(), Content: "hello"}
	b.Emit(e)

	got := obs.received()
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	delta, ok := got[0].(TextDeltaEvent)
	if !ok || delta.Content != "hello" {
		t.Fatalf("unexpected event: %+v", got[0])
	}
}

func TestBusEmitToMultipleObservers(t *testing.T) {
	b := NewBus()
	obs1 := &recordingObserver{}
	obs2 := &recordingObserver{}
	b.Subscribe(obs1)
	b.Subscribe(obs2)

	e := TextDeltaEvent{Base: NewBase(), Content: "world"}
	b.Emit(e)

	for i, obs := range []*recordingObserver{obs1, obs2} {
		got := obs.received()
		if len(got) != 1 {
			t.Fatalf("observer %d: expected 1 event, got %d", i, len(got))
		}
	}
}

func TestBusObserverPanicDoesNotAffectOthers(t *testing.T) {
	b := NewBus()

	b.Subscribe(ObserverFunc(func(Event) {
		panic("intentional panic")
	}))

	obs2 := &recordingObserver{}
	b.Subscribe(obs2)

	b.Emit(TextDeltaEvent{Base: NewBase(), Content: "safe"})

	got := obs2.received()
	if len(got) != 1 {
		t.Fatalf("expected second observer to receive event despite panic, got %d events", len(got))
	}
}

func TestBusConcurrentEmitSubscribe(t *testing.T) {
	b := NewBus()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Subscribe(&recordingObserver{})
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Emit(TextDeltaEvent{Base: NewBase(), Content: "concurrent"})
		}()
	}

	wg.Wait()
}

func TestBusSubscribeDuringEmit(t *testing.T) {
	b := NewBus()
	inner := &recordingObserver{}

	b.Subscribe(ObserverFunc(func(e Event) {
		b.Subscribe(inner)
	}))

	// Must not deadlock.
	b.Emit(TextDeltaEvent{Base: NewBase(), Content: "trigger"})
}
