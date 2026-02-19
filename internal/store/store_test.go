package store

import (
	"testing"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/capture"
)

func TestStore_AddAndGetPackets(t *testing.T) {
	s := New(Config{MaxPackets: 100, MaxConnections: 100})

	for i := 0; i < 10; i++ {
		s.AddPacket(capture.NetworkPacket{
			ID:     "pkt-" + itoa(i),
			Serial: "dev1",
			SrcIP:  "10.0.0.1",
			DstIP:  "93.184.216.34",
			DstPort: 80,
		})
	}

	if s.PacketCount() != 10 {
		t.Fatalf("expected 10 packets, got %d", s.PacketCount())
	}

	recent := s.GetRecentPackets(5)
	if len(recent) != 5 {
		t.Fatalf("expected 5 recent packets, got %d", len(recent))
	}

	// Most recent should be last added.
	if recent[0].ID != "pkt-9" {
		t.Errorf("most recent: got %q, want pkt-9", recent[0].ID)
	}
}

func TestStore_RingBufferOverflow(t *testing.T) {
	s := New(Config{MaxPackets: 5, MaxConnections: 5})

	for i := 0; i < 10; i++ {
		s.AddPacket(capture.NetworkPacket{
			ID:     "pkt-" + itoa(i),
			Serial: "dev1",
		})
	}

	// Should only hold 5 (ring buffer).
	if s.PacketCount() != 5 {
		t.Fatalf("expected 5 packets (ring buffer), got %d", s.PacketCount())
	}

	recent := s.GetRecentPackets(5)
	// Should have packets 5-9.
	if recent[0].ID != "pkt-9" {
		t.Errorf("most recent: got %q, want pkt-9", recent[0].ID)
	}
	if recent[4].ID != "pkt-5" {
		t.Errorf("oldest: got %q, want pkt-5", recent[4].ID)
	}
}

func TestStore_AddAndGetConnections(t *testing.T) {
	s := New(Config{MaxPackets: 100, MaxConnections: 100})

	s.AddConnection(capture.Connection{
		ID:         "c1",
		Serial:     "dev1",
		LocalIP:    "10.0.0.1",
		LocalPort:  12345,
		RemoteIP:   "93.184.216.34",
		RemotePort: 443,
		State:      capture.ConnEstablished,
	})

	if s.ConnectionCount() != 1 {
		t.Fatalf("expected 1 connection, got %d", s.ConnectionCount())
	}

	conns := s.GetRecentConnections(10)
	if len(conns) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(conns))
	}
	if conns[0].RemotePort != 443 {
		t.Errorf("RemotePort: got %d, want 443", conns[0].RemotePort)
	}
}

func TestStore_GetPacketsBySerial(t *testing.T) {
	s := New(Config{MaxPackets: 100, MaxConnections: 100})

	s.AddPacket(capture.NetworkPacket{ID: "a1", Serial: "dev1"})
	s.AddPacket(capture.NetworkPacket{ID: "b1", Serial: "dev2"})
	s.AddPacket(capture.NetworkPacket{ID: "a2", Serial: "dev1"})

	dev1Pkts := s.GetPacketsBySerial("dev1", 10)
	if len(dev1Pkts) != 2 {
		t.Fatalf("expected 2 packets for dev1, got %d", len(dev1Pkts))
	}
}

func TestStore_Clear(t *testing.T) {
	s := New(Config{MaxPackets: 100, MaxConnections: 100})

	s.AddPacket(capture.NetworkPacket{ID: "p1", Serial: "dev1"})
	s.AddConnection(capture.Connection{
		ID: "c1", Serial: "dev1",
		LocalIP: "1.1.1.1", LocalPort: 1, RemoteIP: "2.2.2.2", RemotePort: 2,
	})

	s.Clear()

	if s.PacketCount() != 0 {
		t.Errorf("packets after clear: %d", s.PacketCount())
	}
	if s.ConnectionCount() != 0 {
		t.Errorf("connections after clear: %d", s.ConnectionCount())
	}
}

func TestStore_Stats(t *testing.T) {
	s := New(Config{MaxPackets: 50, MaxConnections: 30})

	stats := s.Stats()
	if stats.PacketCapacity != 50 {
		t.Errorf("PacketCapacity: got %d, want 50", stats.PacketCapacity)
	}
	if stats.ConnCapacity != 30 {
		t.Errorf("ConnCapacity: got %d, want 30", stats.ConnCapacity)
	}
}

func TestStore_OnChange(t *testing.T) {
	s := New(Config{MaxPackets: 100, MaxConnections: 100})

	called := 0
	s.SetOnChange(func() { called++ })

	s.AddPacket(capture.NetworkPacket{ID: "p1", Serial: "dev1"})
	s.AddConnection(capture.Connection{
		ID: "c1", Serial: "dev1",
		LocalIP: "1.1.1.1", LocalPort: 1, RemoteIP: "2.2.2.2", RemotePort: 2,
	})

	if called != 2 {
		t.Errorf("onChange called %d times, want 2", called)
	}
}

// Ensure unused import.
var _ = time.Now
