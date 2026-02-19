package capture

import (
	"testing"
)

func TestTcpdumpParser_ParseLine_TCP(t *testing.T) {
	p := NewTcpdumpParser("device1")

	tests := []struct {
		name     string
		line     string
		wantNil  bool
		srcIP    string
		srcPort  uint16
		dstIP    string
		dstPort  uint16
		protocol Protocol
	}{
		{
			name:     "basic tcp",
			line:     "12:34:56.789012 IP 10.0.0.1.12345 > 93.184.216.34.80: tcp 100",
			srcIP:    "10.0.0.1",
			srcPort:  12345,
			dstIP:    "93.184.216.34",
			dstPort:  80,
			protocol: ProtoTCP,
		},
		{
			name:     "udp",
			line:     "12:34:56.789000 IP 10.0.0.1.53421 > 8.8.8.8.53: UDP, length 40",
			srcIP:    "10.0.0.1",
			srcPort:  53421,
			dstIP:    "8.8.8.8",
			dstPort:  53,
			protocol: ProtoUDP,
		},
		{
			name:     "with flags",
			line:     "12:34:56.789 IP 192.168.1.100.443 > 10.0.0.5.54321: Flags [P.], seq 1:100, ack 1, win 502, length 99",
			srcIP:    "192.168.1.100",
			srcPort:  443,
			dstIP:    "10.0.0.5",
			dstPort:  54321,
			protocol: ProtoTCP,
		},
		{
			name:    "empty line",
			line:    "",
			wantNil: true,
		},
		{
			name:    "non-packet line",
			line:    "GET /api/users HTTP/1.1",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := p.ParseLine(tt.line)
			if tt.wantNil {
				if pkt != nil {
					t.Errorf("expected nil, got %+v", pkt)
				}
				return
			}
			if pkt == nil {
				t.Fatal("expected packet, got nil")
			}
			if pkt.SrcIP != tt.srcIP {
				t.Errorf("SrcIP: got %q, want %q", pkt.SrcIP, tt.srcIP)
			}
			if pkt.SrcPort != tt.srcPort {
				t.Errorf("SrcPort: got %d, want %d", pkt.SrcPort, tt.srcPort)
			}
			if pkt.DstIP != tt.dstIP {
				t.Errorf("DstIP: got %q, want %q", pkt.DstIP, tt.dstIP)
			}
			if pkt.DstPort != tt.dstPort {
				t.Errorf("DstPort: got %d, want %d", pkt.DstPort, tt.dstPort)
			}
			if pkt.Protocol != tt.protocol {
				t.Errorf("Protocol: got %q, want %q", pkt.Protocol, tt.protocol)
			}
			if pkt.Serial != "device1" {
				t.Errorf("Serial: got %q, want %q", pkt.Serial, "device1")
			}
		})
	}
}

func TestTcpdumpParser_EnrichWithHTTP(t *testing.T) {
	p := NewTcpdumpParser("dev1")
	pkt := &NetworkPacket{}

	p.EnrichWithHTTP(pkt, "GET /api/users HTTP/1.1")
	if pkt.HTTPMethod != "GET" {
		t.Errorf("Method: got %q, want GET", pkt.HTTPMethod)
	}
	if pkt.HTTPPath != "/api/users" {
		t.Errorf("Path: got %q, want /api/users", pkt.HTTPPath)
	}

	p.EnrichWithHTTP(pkt, "Host: example.com")
	if pkt.HTTPHost != "example.com" {
		t.Errorf("Host: got %q, want example.com", pkt.HTTPHost)
	}
}

func TestTcpdumpParser_EnrichWithHTTP_Response(t *testing.T) {
	p := NewTcpdumpParser("dev1")
	pkt := &NetworkPacket{}

	p.EnrichWithHTTP(pkt, "HTTP/1.1 200 OK")
	if pkt.HTTPStatus != 200 {
		t.Errorf("Status: got %d, want 200", pkt.HTTPStatus)
	}
}

func TestTcpdumpParser_EnrichWithHTTP_NilPacket(t *testing.T) {
	p := NewTcpdumpParser("dev1")
	// Should not panic.
	p.EnrichWithHTTP(nil, "GET / HTTP/1.1")
}

func TestTcpdumpParser_ParseFlags(t *testing.T) {
	p := NewTcpdumpParser("dev1")

	tests := []struct {
		rest string
		want string
	}{
		{"Flags [P.], seq 1:100, ack 1, win 502, length 99", "P."},
		{"Flags [S], seq 1000, win 65535, length 0", "S"},
		{"Flags [R.], seq 1, ack 1, win 0, length 0", "R."},
		{"tcp 100", ""},
	}

	for _, tt := range tests {
		got := p.parseFlags(tt.rest)
		if got != tt.want {
			t.Errorf("parseFlags(%q) = %q, want %q", tt.rest, got, tt.want)
		}
	}
}

func TestTcpdumpParser_IDIncrement(t *testing.T) {
	p := NewTcpdumpParser("dev1")
	line := "12:34:56.789012 IP 10.0.0.1.12345 > 93.184.216.34.80: tcp 100"

	pkt1 := p.ParseLine(line)
	pkt2 := p.ParseLine(line)

	if pkt1.ID == pkt2.ID {
		t.Error("packets should have different IDs")
	}
}
