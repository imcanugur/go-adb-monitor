package event

import (
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
)

// Type classifies device events.
type Type string

const (
	DeviceConnected    Type = "device_connected"
	DeviceDisconnected Type = "device_disconnected"
	DeviceStateChanged Type = "device_state_changed"
	DeviceProperties   Type = "device_properties"
)

// Event represents a device lifecycle or property event.
type Event struct {
	Type      Type            `json:"type"`
	Serial    string          `json:"serial"`
	Device    *adb.Device     `json:"device,omitempty"`
	OldState  adb.DeviceState `json:"old_state,omitempty"`
	NewState  adb.DeviceState `json:"new_state,omitempty"`
	Props     map[string]string `json:"props,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}
