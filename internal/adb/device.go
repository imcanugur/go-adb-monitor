package adb

import (
	"fmt"
	"strings"
	"time"
)

// DeviceState represents the state of an Android device as reported by ADB.
type DeviceState string

const (
	StateDevice       DeviceState = "device"
	StateOffline      DeviceState = "offline"
	StateUnauthorized DeviceState = "unauthorized"
	StateBootloader   DeviceState = "bootloader"
	StateRecovery     DeviceState = "recovery"
	StateSideload     DeviceState = "sideload"
	StateNoPermission DeviceState = "no permissions"
	StateUnknown      DeviceState = "unknown"
)

// IsOnline returns true if the device is in a usable state.
func (s DeviceState) IsOnline() bool {
	return s == StateDevice
}

// Device represents a connected Android device.
type Device struct {
	Serial    string      `json:"serial"`
	State     DeviceState `json:"state"`
	Product   string      `json:"product,omitempty"`
	Model     string      `json:"model,omitempty"`
	DeviceTag string      `json:"device_tag,omitempty"`
	Transport string      `json:"transport,omitempty"`
	FirstSeen time.Time   `json:"first_seen"`
	LastSeen  time.Time   `json:"last_seen"`
}

// String returns a human-readable representation of the device.
func (d Device) String() string {
	return fmt.Sprintf("%s [%s] model=%s", d.Serial, d.State, d.Model)
}

// ParseDeviceList parses the output format of ADB's track-devices-l or devices -l.
// Each line: <serial>\t<state>\t<properties...>
// Properties are key:value pairs separated by spaces.
func ParseDeviceList(data string) []Device {
	var devices []Device
	now := time.Now()
	lines := strings.Split(strings.TrimSpace(data), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		dev := parseDeviceLine(line, now)
		if dev.Serial != "" {
			devices = append(devices, dev)
		}
	}

	return devices
}

func parseDeviceLine(line string, now time.Time) Device {
	// Format: serial\tstate [key:value ...]
	// Or: serial\tstate\tkey:value key:value ...
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return Device{}
	}

	dev := Device{
		Serial:    parts[0],
		State:     parseState(parts[1]),
		FirstSeen: now,
		LastSeen:  now,
	}

	// Parse optional key:value properties.
	for _, kv := range parts[2:] {
		key, value, ok := strings.Cut(kv, ":")
		if !ok {
			continue
		}
		switch key {
		case "product":
			dev.Product = value
		case "model":
			dev.Model = value
		case "device":
			dev.DeviceTag = value
		case "transport_id":
			dev.Transport = value
		}
	}

	return dev
}

func parseState(s string) DeviceState {
	switch DeviceState(s) {
	case StateDevice, StateOffline, StateUnauthorized,
		StateBootloader, StateRecovery, StateSideload:
		return DeviceState(s)
	default:
		if strings.Contains(s, "no permissions") {
			return StateNoPermission
		}
		return StateUnknown
	}
}
