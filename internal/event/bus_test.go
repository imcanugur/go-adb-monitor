package event

import (
	"sync"
	"testing"
	"time"
)

func TestBus_PublishSubscribe(t *testing.T) {
	bus := NewBus(16)
	defer bus.Close()

	var mu sync.Mutex
	var received []Event

	bus.Subscribe("test", func(e Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})

	bus.Publish(Event{
		Type:      DeviceConnected,
		Serial:    "ABC123",
		Timestamp: time.Now(),
	})

	// Wait for event dispatch.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].Serial != "ABC123" {
		t.Errorf("serial: got %q, want %q", received[0].Serial, "ABC123")
	}
	if received[0].Type != DeviceConnected {
		t.Errorf("type: got %q, want %q", received[0].Type, DeviceConnected)
	}
}

func TestBus_MultipleSubscribers(t *testing.T) {
	bus := NewBus(16)
	defer bus.Close()

	var count1, count2 int
	var mu sync.Mutex

	bus.Subscribe("sub1", func(e Event) {
		mu.Lock()
		count1++
		mu.Unlock()
	})
	bus.Subscribe("sub2", func(e Event) {
		mu.Lock()
		count2++
		mu.Unlock()
	})

	bus.Publish(Event{Type: DeviceConnected, Serial: "X"})
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if count1 != 1 || count2 != 1 {
		t.Errorf("counts: sub1=%d sub2=%d, want both 1", count1, count2)
	}
}

func TestBus_Unsubscribe(t *testing.T) {
	bus := NewBus(16)
	defer bus.Close()

	var count int
	var mu sync.Mutex

	unsub := bus.Subscribe("test", func(e Event) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	bus.Publish(Event{Type: DeviceConnected, Serial: "A"})
	time.Sleep(50 * time.Millisecond)

	unsub()

	bus.Publish(Event{Type: DeviceDisconnected, Serial: "A"})
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 event after unsub, got %d", count)
	}
}

func TestBus_Close(t *testing.T) {
	bus := NewBus(16)
	bus.Close()
	// Double close should not panic.
	bus.Close()
}
