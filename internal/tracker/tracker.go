package tracker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
	"github.com/imcanugur/go-adb-monitor/internal/event"
)

const (
	// reconnectBaseDelay is the initial delay before reconnecting after failure.
	reconnectBaseDelay = 1 * time.Second
	// reconnectMaxDelay caps the exponential backoff.
	reconnectMaxDelay = 30 * time.Second
)

// Tracker streams device connect/disconnect events from the ADB server
// using the track-devices protocol (push-based, not polling).
type Tracker struct {
	client *adb.Client
	bus    *event.Bus
	log    *slog.Logger

	// known tracks the last-known state of all devices by serial.
	known map[string]adb.Device
}

// New creates a new device tracker.
func New(client *adb.Client, bus *event.Bus, log *slog.Logger) *Tracker {
	return &Tracker{
		client: client,
		bus:    bus,
		log:    log.With("component", "tracker"),
		known:  make(map[string]adb.Device),
	}
}

// Run starts the tracker loop. It blocks until the context is cancelled.
// On connection failure it reconnects with exponential backoff.
func (t *Tracker) Run(ctx context.Context) error {
	delay := reconnectBaseDelay

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := t.stream(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		t.log.Warn("tracking connection lost, reconnecting",
			"error", err,
			"delay", delay,
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		delay = min(delay*2, reconnectMaxDelay)
	}
}

// stream opens a track-devices connection and processes state updates until
// the connection is closed or an error occurs.
func (t *Tracker) stream(ctx context.Context) error {
	conn, err := t.client.TrackDevices(ctx)
	if err != nil {
		return fmt.Errorf("opening track-devices stream: %w", err)
	}
	defer conn.Close()

	t.log.Info("track-devices stream established", "addr", t.client.Addr())

	// Watch for context cancellation and close the connection.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		payload, err := adb.ReadLengthPrefixed(conn)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err == io.EOF || isClosedErr(err) {
				return fmt.Errorf("%w: stream terminated", adb.ErrConnectionClosed)
			}
			return fmt.Errorf("reading device update: %w", err)
		}

		devices := adb.ParseDeviceList(payload)
		t.diffAndEmit(devices)
	}
}

// diffAndEmit compares the new device list against known state and emits
// appropriate events for changes.
func (t *Tracker) diffAndEmit(current []adb.Device) {
	now := time.Now()
	seen := make(map[string]struct{}, len(current))

	for _, dev := range current {
		seen[dev.Serial] = struct{}{}
		prev, existed := t.known[dev.Serial]

		if !existed {
			// New device.
			dev.FirstSeen = now
			dev.LastSeen = now
			t.known[dev.Serial] = dev

			t.log.Info("device connected",
				"serial", dev.Serial,
				"state", dev.State,
				"model", dev.Model,
			)
			t.bus.Publish(event.Event{
				Type:      event.DeviceConnected,
				Serial:    dev.Serial,
				Device:    &dev,
				NewState:  dev.State,
				Timestamp: now,
			})
			continue
		}

		// Existing device — check for state change.
		dev.FirstSeen = prev.FirstSeen
		dev.LastSeen = now
		t.known[dev.Serial] = dev

		if prev.State != dev.State {
			t.log.Info("device state changed",
				"serial", dev.Serial,
				"old_state", prev.State,
				"new_state", dev.State,
			)
			t.bus.Publish(event.Event{
				Type:      event.DeviceStateChanged,
				Serial:    dev.Serial,
				Device:    &dev,
				OldState:  prev.State,
				NewState:  dev.State,
				Timestamp: now,
			})
		}
	}

	// Detect disconnected devices.
	for serial, dev := range t.known {
		if _, stillHere := seen[serial]; !stillHere {
			t.log.Info("device disconnected",
				"serial", serial,
				"last_state", dev.State,
			)
			t.bus.Publish(event.Event{
				Type:      event.DeviceDisconnected,
				Serial:    serial,
				Device:    &dev,
				OldState:  dev.State,
				Timestamp: now,
			})
			delete(t.known, serial)
		}
	}
}

// isClosedErr checks if an error indicates a closed connection.
func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "use of closed network connection" ||
		err.Error() == "read/write on closed pipe"
}
