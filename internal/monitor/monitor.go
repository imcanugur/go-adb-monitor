package monitor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
	"github.com/imcanugur/go-adb-monitor/internal/event"
)

const (
	// DefaultPropInterval is the default interval for collecting device properties.
	DefaultPropInterval = 30 * time.Second
)

// Monitor orchestrates per-device monitors. It subscribes to device events
// from the tracker and spins up/tears down DeviceMonitors as devices
// connect and disconnect.
type Monitor struct {
	client       *adb.Client
	bus          *event.Bus
	log          *slog.Logger
	propInterval time.Duration

	mu          sync.Mutex
	devices     map[string]context.CancelFunc // serial → cancel per-device monitor
	unsub       func()
}

// Config holds Monitor configuration.
type Config struct {
	PropInterval time.Duration
}

// New creates a new Monitor orchestrator.
func New(client *adb.Client, bus *event.Bus, log *slog.Logger, cfg Config) *Monitor {
	interval := cfg.PropInterval
	if interval <= 0 {
		interval = DefaultPropInterval
	}

	return &Monitor{
		client:       client,
		bus:          bus,
		log:          log.With("component", "monitor"),
		propInterval: interval,
		devices:      make(map[string]context.CancelFunc),
	}
}

// Run starts the monitor orchestrator. It listens for device events and
// manages per-device monitors. Blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) error {
	m.unsub = m.bus.Subscribe("monitor", func(e event.Event) {
		switch e.Type {
		case event.DeviceConnected:
			if e.Device != nil && e.Device.State.IsOnline() {
				m.startDevice(ctx, e.Serial)
			}
		case event.DeviceStateChanged:
			if e.NewState.IsOnline() {
				m.startDevice(ctx, e.Serial)
			} else {
				m.stopDevice(e.Serial)
			}
		case event.DeviceDisconnected:
			m.stopDevice(e.Serial)
		}
	})

	m.log.Info("monitor orchestrator started")

	<-ctx.Done()

	m.shutdown()
	return ctx.Err()
}

// startDevice launches a DeviceMonitor goroutine for the given serial,
// if one isn't already running.
func (m *Monitor) startDevice(parentCtx context.Context, serial string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, running := m.devices[serial]; running {
		return
	}

	ctx, cancel := context.WithCancel(parentCtx)
	m.devices[serial] = cancel

	dm := NewDeviceMonitor(m.client, m.bus, m.log, serial, m.propInterval)
	go dm.Run(ctx)

	m.log.Info("started per-device monitor", "serial", serial)
}

// stopDevice stops the DeviceMonitor for the given serial.
func (m *Monitor) stopDevice(serial string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cancel, ok := m.devices[serial]; ok {
		cancel()
		delete(m.devices, serial)
		m.log.Info("stopped per-device monitor", "serial", serial)
	}
}

// shutdown stops all running device monitors.
func (m *Monitor) shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for serial, cancel := range m.devices {
		cancel()
		m.log.Debug("shutdown: stopped device monitor", "serial", serial)
	}
	m.devices = make(map[string]context.CancelFunc)

	if m.unsub != nil {
		m.unsub()
	}

	m.log.Info("monitor orchestrator stopped")
}
