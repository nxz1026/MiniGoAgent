package event

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewBus(t *testing.T) {
	b := NewBus()
	if b == nil {
		t.Fatal("NewBus returned nil")
	}
}

func TestSubscribePublish(t *testing.T) {
	b := NewBus()
	var count atomic.Int32

	b.Subscribe(AgentStart, func(evt Event) {
		count.Add(1)
	})
	b.Subscribe(AgentStart, func(evt Event) {
		count.Add(1)
	})

	b.Publish(Event{Type: AgentStart})
	if count.Load() != 2 {
		t.Fatalf("expected 2 handlers called, got %d", count.Load())
	}
}

func TestPublishDifferentType(t *testing.T) {
	b := NewBus()
	var count atomic.Int32

	b.Subscribe(AgentStart, func(evt Event) {
		count.Add(1)
	})

	b.Publish(Event{Type: StepStart})
	if count.Load() != 0 {
		t.Fatalf("expected 0 handlers called for different type, got %d", count.Load())
	}
}

func TestUnsubscribe(t *testing.T) {
	b := NewBus()
	var count atomic.Int32

	h := func(evt Event) {
		count.Add(1)
	}

	b.Subscribe(AgentStart, h)
	b.Publish(Event{Type: AgentStart})
	if count.Load() != 1 {
		t.Fatalf("expected 1 after first publish, got %d", count.Load())
	}

	b.Unsubscribe(AgentStart, h)
	b.Publish(Event{Type: AgentStart})
	if count.Load() != 1 {
		t.Fatalf("expected 1 after unsubscribe+publish, got %d", count.Load())
	}
}

func TestPublishAll(t *testing.T) {
	b := NewBus()
	var mu sync.Mutex
	var events []Event

	b.Subscribe(AgentStart, func(evt Event) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})

	evts := []Event{
		{Type: AgentStart, SessionID: "s1"},
		{Type: AgentStart, SessionID: "s2"},
		{Type: AgentStart, SessionID: "s3"},
	}
	b.PublishAll(evts)

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
}

func TestPublishToUnsubscribedType(t *testing.T) {
	b := NewBus()
	b.Publish(Event{Type: AgentStart})
}

func TestConcurrentPublish(t *testing.T) {
	b := NewBus()
	var count atomic.Int32

	b.Subscribe(AgentStart, func(evt Event) {
		count.Add(1)
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Publish(Event{Type: AgentStart})
		}()
	}
	wg.Wait()

	if count.Load() != 100 {
		t.Fatalf("expected 100, got %d", count.Load())
	}
}

func TestConcurrentSubscribePublish(t *testing.T) {
	b := NewBus()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Subscribe(AgentStart, func(evt Event) {})
		}()
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Publish(Event{Type: AgentStart})
		}()
	}
	wg.Wait()
}

func TestPublishHandlerPanicIsolation(t *testing.T) {
	b := NewBus()
	var secondCalled bool
	b.Subscribe(AgentStart, func(evt Event) { panic("boom") })
	b.Subscribe(AgentStart, func(evt Event) { secondCalled = true })

	b.Publish(Event{Type: AgentStart})

	if !secondCalled {
		t.Fatal("panic in first handler prevented second handler from running")
	}
}
