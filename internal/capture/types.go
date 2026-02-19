package capture

import (
	"time"
)

// Mode determines how network data is captured from a device.
type Mode int

const (
	// ModeAuto selects the best available capture mode.
	ModeAuto Mode = iota
	// ModeTcpdump uses tcpdump (requires root on device).
	ModeTcpdump
	// ModeProcNet polls /proc/net/tcp for connection tracking (no root needed).
	ModeProcNet
)

func (m Mode) String() string {
	switch m {
	case ModeTcpdump:
		return "tcpdump"
	case ModeProcNet:
		return "procnet"
	default:
		return "auto"
	}
}

// Protocol represents a network protocol.
type Protocol string

const (
	ProtoTCP  Protocol = "TCP"
	ProtoUDP  Protocol = "UDP"
	ProtoICMP Protocol = "ICMP"
)

// ConnState represents a TCP connection state.
type ConnState string

const (
	ConnEstablished ConnState = "ESTABLISHED"
	ConnSynSent     ConnState = "SYN_SENT"
	ConnSynRecv     ConnState = "SYN_RECV"
	ConnFinWait1    ConnState = "FIN_WAIT1"
	ConnFinWait2    ConnState = "FIN_WAIT2"
	ConnTimeWait    ConnState = "TIME_WAIT"
	ConnClose       ConnState = "CLOSE"
	ConnCloseWait   ConnState = "CLOSE_WAIT"
	ConnLastAck     ConnState = "LAST_ACK"
	ConnListen      ConnState = "LISTEN"
	ConnClosing     ConnState = "CLOSING"
)

// NetworkPacket represents a single captured network packet from tcpdump.
type NetworkPacket struct {
	ID        string    `json:"id"`
	Serial    string    `json:"serial"`
	Timestamp time.Time `json:"timestamp"`
	SrcIP     string    `json:"src_ip"`
	SrcPort   uint16    `json:"src_port"`
	DstIP     string    `json:"dst_ip"`
	DstPort   uint16    `json:"dst_port"`
	Protocol  Protocol  `json:"protocol"`
	Length    int       `json:"length"`
	Flags     string    `json:"flags,omitempty"`

	// HTTP fields, populated when protocol is HTTP.
	HTTPMethod string `json:"http_method,omitempty"`
	HTTPPath   string `json:"http_path,omitempty"`
	HTTPHost   string `json:"http_host,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`

	Raw string `json:"raw,omitempty"`
}

// Connection represents an active TCP/UDP connection from /proc/net/tcp.
type Connection struct {
	ID        string    `json:"id"`
	Serial    string    `json:"serial"`
	LocalIP   string    `json:"local_ip"`
	LocalPort uint16    `json:"local_port"`
	RemoteIP  string    `json:"remote_ip"`
	RemotePort uint16   `json:"remote_port"`
	State     ConnState `json:"state"`
	Protocol  Protocol  `json:"protocol"`
	UID       int       `json:"uid"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// IsHTTPPort returns true if the port typically serves HTTP(S) traffic.
func IsHTTPPort(port uint16) bool {
	switch port {
	case 80, 443, 8080, 8443, 3000, 5000, 8000, 8888, 9090:
		return true
	default:
		return false
	}
}

// CaptureStats holds statistics for a device's capture session.
type CaptureStats struct {
	Serial       string    `json:"serial"`
	Mode         string    `json:"mode"`
	PacketCount  int64     `json:"packet_count"`
	ConnCount    int       `json:"conn_count"`
	BytesRead    int64     `json:"bytes_read"`
	StartedAt    time.Time `json:"started_at"`
	LastActivity time.Time `json:"last_activity"`
	Errors       int64     `json:"errors"`
}
