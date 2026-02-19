package adb

import (
	"testing"
	"time"
)

func TestParseDeviceList_MultipleDevices(t *testing.T) {
	input := `emulator-5554          device product:sdk_gphone64_x86_64 model:sdk_gphone64_x86_64 device:emu64xa transport_id:1
HVA0T18B14001251       device product:flame model:Pixel_4 device:flame transport_id:2
192.168.1.100:5555     offline`

	devices := ParseDeviceList(input)

	if len(devices) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(devices))
	}

	tests := []struct {
		serial    string
		state     DeviceState
		model     string
		product   string
		deviceTag string
		transport string
	}{
		{"emulator-5554", StateDevice, "sdk_gphone64_x86_64", "sdk_gphone64_x86_64", "emu64xa", "1"},
		{"HVA0T18B14001251", StateDevice, "Pixel_4", "flame", "flame", "2"},
		{"192.168.1.100:5555", StateOffline, "", "", "", ""},
	}

	for i, tt := range tests {
		d := devices[i]
		if d.Serial != tt.serial {
			t.Errorf("[%d] serial: got %q, want %q", i, d.Serial, tt.serial)
		}
		if d.State != tt.state {
			t.Errorf("[%d] state: got %q, want %q", i, d.State, tt.state)
		}
		if d.Model != tt.model {
			t.Errorf("[%d] model: got %q, want %q", i, d.Model, tt.model)
		}
		if d.Product != tt.product {
			t.Errorf("[%d] product: got %q, want %q", i, d.Product, tt.product)
		}
		if d.DeviceTag != tt.deviceTag {
			t.Errorf("[%d] device_tag: got %q, want %q", i, d.DeviceTag, tt.deviceTag)
		}
		if d.Transport != tt.transport {
			t.Errorf("[%d] transport: got %q, want %q", i, d.Transport, tt.transport)
		}
	}
}

func TestParseDeviceList_Empty(t *testing.T) {
	devices := ParseDeviceList("")
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices, got %d", len(devices))
	}
}

func TestParseDeviceList_WhitespaceOnly(t *testing.T) {
	devices := ParseDeviceList("  \n  \n  ")
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices, got %d", len(devices))
	}
}

func TestParseState(t *testing.T) {
	tests := []struct {
		input string
		want  DeviceState
	}{
		{"device", StateDevice},
		{"offline", StateOffline},
		{"unauthorized", StateUnauthorized},
		{"bootloader", StateBootloader},
		{"recovery", StateRecovery},
		{"sideload", StateSideload},
		{"foobar", StateUnknown},
	}

	for _, tt := range tests {
		got := parseState(tt.input)
		if got != tt.want {
			t.Errorf("parseState(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDeviceState_IsOnline(t *testing.T) {
	if !StateDevice.IsOnline() {
		t.Error("StateDevice should be online")
	}
	if StateOffline.IsOnline() {
		t.Error("StateOffline should not be online")
	}
	if StateUnauthorized.IsOnline() {
		t.Error("StateUnauthorized should not be online")
	}
}

func TestDevice_String(t *testing.T) {
	d := Device{
		Serial:    "ABC123",
		State:     StateDevice,
		Model:     "Pixel_4",
		FirstSeen: time.Now(),
	}
	s := d.String()
	if s != "ABC123 [device] model=Pixel_4" {
		t.Errorf("unexpected String(): %q", s)
	}
}
