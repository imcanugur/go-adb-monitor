package monitor

import (
	"testing"
)

func TestParseBattery(t *testing.T) {
	input := `Current Battery Service state:
  AC powered: false
  USB powered: true
  Wireless powered: false
  Max charging current: 500000
  Max charging voltage: 5000000
  Charge counter: 2621440
  status: 2
  health: 2
  present: true
  level: 85
  scale: 100
  voltage: 4250
  temperature: 255
  technology: Li-ion`

	props := make(map[string]string)
	parseBattery(input, props)

	tests := map[string]string{
		"battery.level":       "85",
		"battery.status":      "2",
		"battery.temperature": "255",
		"battery.usb_powered": "true",
		"battery.ac_powered":  "false",
		"battery.health":      "2",
	}

	for key, want := range tests {
		got, ok := props[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", key, got, want)
		}
	}
}

func TestParseBattery_Empty(t *testing.T) {
	props := make(map[string]string)
	parseBattery("", props)
	if len(props) != 0 {
		t.Errorf("expected 0 props from empty input, got %d", len(props))
	}
}

func TestSplitLines(t *testing.T) {
	lines := splitLines("a\nb\nc")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestParseKeyValue(t *testing.T) {
	tests := []struct {
		input   string
		key     string
		value   string
		wantOK  bool
	}{
		{"  level: 85", "level", "85", true},
		{"  AC powered: false", "AC powered", "false", true},
		{"no colon here", "", "", false},
		{": empty key", "", "", false},
	}

	for _, tt := range tests {
		key, value, ok := parseKeyValue(tt.input)
		if ok != tt.wantOK {
			t.Errorf("parseKeyValue(%q): ok=%v, want %v", tt.input, ok, tt.wantOK)
			continue
		}
		if ok {
			if key != tt.key {
				t.Errorf("parseKeyValue(%q): key=%q, want %q", tt.input, key, tt.key)
			}
			if value != tt.value {
				t.Errorf("parseKeyValue(%q): value=%q, want %q", tt.input, value, tt.value)
			}
		}
	}
}

func TestTrimSpace(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  hello  ", "hello"},
		{"\thello\t", "hello"},
		{"hello", "hello"},
		{"", ""},
		{"   ", ""},
	}

	for _, tt := range tests {
		got := trimSpace(tt.input)
		if got != tt.want {
			t.Errorf("trimSpace(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
