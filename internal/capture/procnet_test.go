package capture

import (
	"testing"
)

func TestProcNetParser_ParseProcNet_TCP(t *testing.T) {
	// Real /proc/net/tcp output (modified IPs for testing).
	input := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:13AD 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0101A8C0:D4F2 220ED8AE:01BB 01 00000000:00000000 00:00000000 00000000  1000        0 54321 1 0000000000000000 100 0 0 10 0
   2: 0101A8C0:C350 4E46C8AC:0050 01 00000000:00000000 00:00000000 00000000  1000        0 54322 1 0000000000000000 100 0 0 10 0`

	p := NewProcNetParser("device1")
	conns := p.ParseProcNet(input, ProtoTCP)

	// First line is 127.0.0.1 LISTEN → skipped
	// Second: 192.168.1.1:54514 -> 174.216.14.34:443 ESTABLISHED
	// Third: 192.168.1.1:50000 -> 172.200.70.78:80 ESTABLISHED

	if len(conns) != 2 {
		t.Fatalf("expected 2 connections, got %d", len(conns))
	}

	c := conns[0]
	if c.LocalIP != "192.168.1.1" {
		t.Errorf("LocalIP: got %q, want 192.168.1.1", c.LocalIP)
	}
	if c.LocalPort != 54514 {
		t.Errorf("LocalPort: got %d, want 54514", c.LocalPort)
	}
	if c.RemotePort != 443 {
		t.Errorf("RemotePort: got %d, want 443", c.RemotePort)
	}
	if c.State != ConnEstablished {
		t.Errorf("State: got %q, want ESTABLISHED", c.State)
	}
	if c.UID != 1000 {
		t.Errorf("UID: got %d, want 1000", c.UID)
	}
	if c.Serial != "device1" {
		t.Errorf("Serial: got %q, want device1", c.Serial)
	}

	c2 := conns[1]
	if c2.RemotePort != 80 {
		t.Errorf("Conn2 RemotePort: got %d, want 80", c2.RemotePort)
	}
}

func TestProcNetParser_Empty(t *testing.T) {
	p := NewProcNetParser("dev1")
	conns := p.ParseProcNet("", ProtoTCP)
	if len(conns) != 0 {
		t.Errorf("expected 0 connections from empty input, got %d", len(conns))
	}
}

func TestProcNetParser_HeaderOnly(t *testing.T) {
	p := NewProcNetParser("dev1")
	conns := p.ParseProcNet("  sl  local_address rem_address   st", ProtoTCP)
	if len(conns) != 0 {
		t.Errorf("expected 0 connections from header-only input, got %d", len(conns))
	}
}

func TestParseHexIP_IPv4(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"0100007F", "127.0.0.1"},
		{"00000000", "0.0.0.0"},
		{"0101A8C0", "192.168.1.1"},
	}

	for _, tt := range tests {
		got, err := parseHexIP(tt.input)
		if err != nil {
			t.Errorf("parseHexIP(%q): %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseHexIP(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseHexAddr(t *testing.T) {
	ip, port, err := parseHexAddr("0101A8C0:01BB")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "192.168.1.1" {
		t.Errorf("IP: got %q, want 192.168.1.1", ip)
	}
	if port != 443 {
		t.Errorf("Port: got %d, want 443", port)
	}
}

func TestParseConnState(t *testing.T) {
	tests := []struct {
		hex  string
		want ConnState
	}{
		{"01", ConnEstablished},
		{"02", ConnSynSent},
		{"06", ConnTimeWait},
		{"0A", ConnListen},
		{"08", ConnCloseWait},
	}

	for _, tt := range tests {
		got := parseConnState(tt.hex)
		if got != tt.want {
			t.Errorf("parseConnState(%q) = %q, want %q", tt.hex, got, tt.want)
		}
	}
}

func TestIsHTTPPort(t *testing.T) {
	if !IsHTTPPort(80) {
		t.Error("80 should be HTTP port")
	}
	if !IsHTTPPort(443) {
		t.Error("443 should be HTTP port")
	}
	if IsHTTPPort(22) {
		t.Error("22 should not be HTTP port")
	}
}
