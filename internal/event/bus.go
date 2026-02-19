package event

import (
	"sync"
)

// Handler is a function that processes events.
type Handler func(Event)

// Bus is a publish-subscribe event bus for device events.
// It is safe for concurrent use.
type Bus struct {
	mu       sync.RWMutex
	subs     map[string]Handler
	nextID   int
	bufSize  int
	eventCh  chan Event
	done     chan struct{}
	stopOnce sync.Once
}

// NewBus creates a new event bus with the given internal buffer size.
func NewBus(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = 256
	}
	b := &Bus{
		subs:    make(map[string]Handler),
		bufSize: bufSize,
		eventCh: make(chan Event, bufSize),
		done:    make(chan struct{}),
	}
	go b.dispatch()
	return b
}

// Subscribe registers a handler and returns an unsubscribe function.
func (b *Bus) Subscribe(name string, h Handler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	key := name
	if _, exists := b.subs[key]; exists {
		key = name + "_" + string(rune(b.nextID))
	}
	b.subs[key] = h

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, key)
	}
}

// Publish sends an event to all subscribers asynchronously.
// It does not block if the buffer is full; the event is dropped.
func (b *Bus) Publish(e Event) {
	select {
	case b.eventCh <- e:
	default:
		// Buffer full, drop event. In production, increment a counter.
	}
}

// Close shuts down the event bus dispatcher.
func (b *Bus) Close() {
	b.stopOnce.Do(func() {
		close(b.done)
	})
}

func (b *Bus) dispatch() {
	for {
		select {
		case <-b.done:
			return
		case e := <-b.eventCh:
			b.mu.RLock()
			handlers := make([]Handler, 0, len(b.subs))
			for _, h := range b.subs {
				handlers = append(handlers, h)
			}
			b.mu.RUnlock()

			for _, h := range handlers {
				h(e)
			}
		}
	}
}
