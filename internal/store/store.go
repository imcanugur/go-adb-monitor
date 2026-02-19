package store

import (
	"sync"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/capture"
)

const (
	// DefaultMaxPackets is the default ring buffer capacity for packets.
	DefaultMaxPackets = 50000
	// DefaultMaxConns is the default ring buffer capacity for connections.
	DefaultMaxConns = 10000
)

// Store is a thread-safe, in-memory ring buffer that holds network data.
// It supports both packets (from tcpdump) and connections (from /proc/net).
// Old entries are evicted when capacity is reached.
type Store struct {
	mu sync.RWMutex

	packets    []capture.NetworkPacket
	pktHead    int
	pktCount   int
	pktMaxSize int

	connections    []capture.Connection
	connHead       int
	connCount      int
	connMaxSize    int

	// connMap tracks latest state of each connection by key.
	connMap map[string]*capture.Connection

	// onChange is called (non-blocking) when new data arrives.
	onChange func()
}

// Config configures the store capacity.
type Config struct {
	MaxPackets     int
	MaxConnections int
}

// New creates a new data store.
func New(cfg Config) *Store {
	if cfg.MaxPackets <= 0 {
		cfg.MaxPackets = DefaultMaxPackets
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = DefaultMaxConns
	}

	return &Store{
		packets:    make([]capture.NetworkPacket, cfg.MaxPackets),
		pktMaxSize: cfg.MaxPackets,
		connections: make([]capture.Connection, cfg.MaxConnections),
		connMaxSize: cfg.MaxConnections,
		connMap:     make(map[string]*capture.Connection),
	}
}

// SetOnChange registers a callback invoked when data changes.
func (s *Store) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// AddPacket adds a network packet to the ring buffer.
func (s *Store) AddPacket(pkt capture.NetworkPacket) {
	s.mu.Lock()
	idx := s.pktHead % s.pktMaxSize
	s.packets[idx] = pkt
	s.pktHead++
	if s.pktCount < s.pktMaxSize {
		s.pktCount++
	}
	cb := s.onChange
	s.mu.Unlock()

	if cb != nil {
		cb()
	}
}

// AddConnection adds or updates a connection in the store.
func (s *Store) AddConnection(conn capture.Connection) {
	key := connKey(conn)

	s.mu.Lock()
	if existing, ok := s.connMap[key]; ok {
		existing.LastSeen = conn.LastSeen
		existing.State = conn.State
		s.mu.Unlock()
		return
	}

	idx := s.connHead % s.connMaxSize
	s.connections[idx] = conn
	s.connMap[key] = &s.connections[idx]
	s.connHead++
	if s.connCount < s.connMaxSize {
		s.connCount++
	}
	cb := s.onChange
	s.mu.Unlock()

	if cb != nil {
		cb()
	}
}

// GetRecentPackets returns the N most recent packets, newest first.
func (s *Store) GetRecentPackets(n int) []capture.NetworkPacket {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if n > s.pktCount {
		n = s.pktCount
	}
	if n == 0 {
		return nil
	}

	result := make([]capture.NetworkPacket, n)
	for i := 0; i < n; i++ {
		idx := (s.pktHead - 1 - i)
		if idx < 0 {
			idx += s.pktMaxSize
		}
		idx = idx % s.pktMaxSize
		result[i] = s.packets[idx]
	}
	return result
}

// GetRecentConnections returns the N most recent connections, newest first.
func (s *Store) GetRecentConnections(n int) []capture.Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if n > s.connCount {
		n = s.connCount
	}
	if n == 0 {
		return nil
	}

	result := make([]capture.Connection, n)
	for i := 0; i < n; i++ {
		idx := (s.connHead - 1 - i)
		if idx < 0 {
			idx += s.connMaxSize
		}
		idx = idx % s.connMaxSize
		result[i] = s.connections[idx]
	}
	return result
}

// GetPacketsBySerial returns recent packets for a specific device.
func (s *Store) GetPacketsBySerial(serial string, n int) []capture.NetworkPacket {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []capture.NetworkPacket
	for i := 0; i < s.pktCount && len(result) < n; i++ {
		idx := (s.pktHead - 1 - i)
		if idx < 0 {
			idx += s.pktMaxSize
		}
		idx = idx % s.pktMaxSize
		if s.packets[idx].Serial == serial {
			result = append(result, s.packets[idx])
		}
	}
	return result
}

// GetConnectionsBySerial returns connections for a specific device.
func (s *Store) GetConnectionsBySerial(serial string, n int) []capture.Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []capture.Connection
	for i := 0; i < s.connCount && len(result) < n; i++ {
		idx := (s.connHead - 1 - i)
		if idx < 0 {
			idx += s.connMaxSize
		}
		idx = idx % s.connMaxSize
		if s.connections[idx].Serial == serial {
			result = append(result, s.connections[idx])
		}
	}
	return result
}

// PacketCount returns total stored packets.
func (s *Store) PacketCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pktCount
}

// ConnectionCount returns total stored connections.
func (s *Store) ConnectionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connCount
}

// StoreStats returns current store statistics.
type StoreStats struct {
	PacketCount    int `json:"packet_count"`
	ConnectionCount int `json:"connection_count"`
	PacketCapacity int `json:"packet_capacity"`
	ConnCapacity   int `json:"conn_capacity"`
}

// Stats returns store statistics.
func (s *Store) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StoreStats{
		PacketCount:     s.pktCount,
		ConnectionCount: s.connCount,
		PacketCapacity:  s.pktMaxSize,
		ConnCapacity:    s.connMaxSize,
	}
}

// Clear removes all data from the store.
func (s *Store) Clear() {
	s.mu.Lock()
	s.pktHead = 0
	s.pktCount = 0
	s.connHead = 0
	s.connCount = 0
	s.connMap = make(map[string]*capture.Connection)
	s.mu.Unlock()
}

// ClearDevice removes all data for a specific device.
func (s *Store) ClearDevice(serial string) {
	// For ring buffer, we can't efficiently remove entries.
	// Instead, mark them as empty by zeroing the serial.
	s.mu.Lock()
	for i := range s.packets[:s.pktCount] {
		if s.packets[i].Serial == serial {
			s.packets[i] = capture.NetworkPacket{}
		}
	}
	for key, conn := range s.connMap {
		if conn.Serial == serial {
			delete(s.connMap, key)
		}
	}
	s.mu.Unlock()
}

func connKey(c capture.Connection) string {
	return c.LocalIP + ":" + itoa(int(c.LocalPort)) + "->" +
		c.RemoteIP + ":" + itoa(int(c.RemotePort))
}

func itoa(i int) string {
	if i < 0 {
		return "-" + uitoa(uint(-i))
	}
	return uitoa(uint(i))
}

func uitoa(u uint) string {
	if u == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte(u%10) + '0'
		u /= 10
	}
	return string(buf[i:])
}

// Unused import guard.
var _ = time.Now
