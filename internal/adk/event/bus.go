package event

import (
	"reflect"
	"sync"
)

type Handler func(Event)

type Bus struct {
	mu       sync.RWMutex
	handlers map[Type][]Handler
}

func NewBus() *Bus {
	return &Bus{
		handlers: make(map[Type][]Handler),
	}
}

func (b *Bus) Subscribe(t Type, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[t] = append(b.handlers[t], handler)
}

func (b *Bus) Unsubscribe(t Type, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	handlers := b.handlers[t]
	target := reflect.ValueOf(handler).Pointer()
	for i, h := range handlers {
		if reflect.ValueOf(h).Pointer() == target {
			b.handlers[t] = append(handlers[:i], handlers[i+1:]...)
			return
		}
	}
}

func (b *Bus) Publish(evt Event) {
	b.mu.RLock()
	handlers := make([]Handler, len(b.handlers[evt.Type]))
	copy(handlers, b.handlers[evt.Type])
	b.mu.RUnlock()

	for _, h := range handlers {
		safeInvoke(h, evt)
	}
}

func safeInvoke(h Handler, evt Event) {
	defer func() {
		_ = recover()
	}()
	h(evt)
}

func (b *Bus) PublishAll(evts []Event) {
	for _, evt := range evts {
		b.Publish(evt)
	}
}
