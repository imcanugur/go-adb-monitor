package monitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
	"github.com/imcanugur/go-adb-monitor/internal/event"
)

// properties to collect from each online device.
var defaultProps = []string{
	"ro.product.model",
	"ro.product.manufacturer",
	"ro.build.version.release",
	"ro.build.version.sdk",
	"ro.build.display.id",
	"ro.serialno",
	"ro.hardware",
	"persist.sys.timezone",
}

// batteryProps are collected via dumpsys battery.
const batteryCmd = "dumpsys battery"

// DeviceMonitor collects properties from a single online device on an interval.
type DeviceMonitor struct {
	client   *adb.Client
	bus      *event.Bus
	log      *slog.Logger
	serial   string
	interval time.Duration
}

// NewDeviceMonitor creates a monitor for a specific device.
func NewDeviceMonitor(client *adb.Client, bus *event.Bus, log *slog.Logger, serial string, interval time.Duration) *DeviceMonitor {
	return &DeviceMonitor{
		client:   client,
		bus:      bus,
		log:      log.With("component", "device_monitor", "serial", serial),
		serial:   serial,
		interval: interval,
	}
}

// Run collects device properties on the configured interval until ctx is cancelled.
func (dm *DeviceMonitor) Run(ctx context.Context) {
	dm.log.Info("starting device monitor", "interval", dm.interval)

	// Collect immediately, then on interval.
	dm.collect(ctx)

	ticker := time.NewTicker(dm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			dm.log.Info("device monitor stopped")
			return
		case <-ticker.C:
			dm.collect(ctx)
		}
	}
}

func (dm *DeviceMonitor) collect(ctx context.Context) {
	props := make(map[string]string, len(defaultProps)+5)

	// Collect system properties.
	for _, prop := range defaultProps {
		val, err := dm.client.GetDeviceProp(ctx, dm.serial, prop)
		if err != nil {
			dm.log.Debug("failed to get property",
				"prop", prop,
				"error", err,
			)
			continue
		}
		if val != "" {
			props[prop] = val
		}
	}

	// Collect battery info.
	batteryOut, err := dm.client.Shell(ctx, dm.serial, batteryCmd)
	if err != nil {
		dm.log.Debug("failed to get battery info", "error", err)
	} else {
		parseBattery(batteryOut, props)
	}

	if len(props) == 0 {
		return
	}

	dm.bus.Publish(event.Event{
		Type:      event.DeviceProperties,
		Serial:    dm.serial,
		Props:     props,
		Timestamp: time.Now(),
	})

	dm.log.Debug("properties collected", "count", len(props))
}

// parseBattery extracts key battery metrics from dumpsys battery output.
func parseBattery(output string, props map[string]string) {
	// dumpsys battery output format:
	// Current Battery Service state:
	//   AC powered: false
	//   USB powered: true
	//   level: 85
	//   temperature: 250
	//   ...
	lines := splitLines(output)
	for _, line := range lines {
		key, value, ok := parseKeyValue(line)
		if !ok {
			continue
		}
		switch key {
		case "level":
			props["battery.level"] = value
		case "status":
			props["battery.status"] = value
		case "temperature":
			props["battery.temperature"] = value
		case "USB powered":
			props["battery.usb_powered"] = value
		case "AC powered":
			props["battery.ac_powered"] = value
		case "health":
			props["battery.health"] = value
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func parseKeyValue(line string) (string, string, bool) {
	// Find the colon separator.
	for i := 0; i < len(line); i++ {
		if line[i] == ':' {
			key := trimSpace(line[:i])
			value := trimSpace(line[i+1:])
			if key == "" {
				return "", "", false
			}
			return key, value, true
		}
	}
	return "", "", false
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
