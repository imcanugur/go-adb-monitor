package capture

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// /proc/net/tcp format:
//   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
//    0: 0100007F:13AD 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345

// ProcNetParser parses /proc/net/tcp and /proc/net/tcp6 output.
type ProcNetParser struct {
	serial string
	nextID uint64
}

// NewProcNetParser creates a new parser for the given device serial.
func NewProcNetParser(serial string) *ProcNetParser {
	return &ProcNetParser{serial: serial}
}

// ParseProcNet parses the full output of "cat /proc/net/tcp /proc/net/tcp6".
func (p *ProcNetParser) ParseProcNet(output string, proto Protocol) []Connection {
	var conns []Connection
	now := time.Now()

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "sl") {
			continue // skip header
		}

		conn := p.parseLine(line, proto, now)
		if conn != nil {
			conns = append(conns, *conn)
		}
	}

	return conns
}

func (p *ProcNetParser) parseLine(line string, proto Protocol, now time.Time) *Connection {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return nil
	}

	localAddr := fields[1]
	remoteAddr := fields[2]
	stateHex := fields[3]
	uidStr := fields[7]

	localIP, localPort, err := parseHexAddr(localAddr)
	if err != nil {
		return nil
	}

	remoteIP, remotePort, err := parseHexAddr(remoteAddr)
	if err != nil {
		return nil
	}

	state := parseConnState(stateHex)

	uid, _ := strconv.Atoi(uidStr)

	// Skip loopback and LISTEN sockets for connection tracking.
	if localIP == "127.0.0.1" && remoteIP == "127.0.0.1" {
		return nil
	}
	if remoteIP == "0.0.0.0" && state == ConnListen {
		return nil
	}

	p.nextID++
	return &Connection{
		ID:         fmt.Sprintf("%s-conn-%d", p.serial, p.nextID),
		Serial:     p.serial,
		LocalIP:    localIP,
		LocalPort:  localPort,
		RemoteIP:   remoteIP,
		RemotePort: remotePort,
		State:      state,
		Protocol:   proto,
		UID:        uid,
		FirstSeen:  now,
		LastSeen:   now,
	}
}

// parseHexAddr parses "AABBCCDD:PORT" where IP is little-endian hex.
func parseHexAddr(addr string) (string, uint16, error) {
	parts := strings.SplitN(addr, ":", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid addr format: %s", addr)
	}

	ip, err := parseHexIP(parts[0])
	if err != nil {
		return "", 0, err
	}

	port, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port: %s", parts[1])
	}

	return ip, uint16(port), nil
}

// parseHexIP converts a hex-encoded IP to dotted notation.
// /proc/net/tcp uses little-endian 32-bit for IPv4.
func parseHexIP(h string) (string, error) {
	if len(h) == 8 {
		// IPv4: little-endian 32-bit
		b, err := hex.DecodeString(h)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d.%d.%d.%d", b[3], b[2], b[1], b[0]), nil
	}

	if len(h) == 32 {
		// IPv6: four 32-bit words, each little-endian
		b, err := hex.DecodeString(h)
		if err != nil {
			return "", err
		}
		// Convert each 4-byte group from little-endian to network order.
		parts := make([]string, 8)
		for i := 0; i < 4; i++ {
			off := i * 4
			w := uint32(b[off+3])<<24 | uint32(b[off+2])<<16 | uint32(b[off+1])<<8 | uint32(b[off])
			parts[i*2] = fmt.Sprintf("%x", w>>16)
			parts[i*2+1] = fmt.Sprintf("%x", w&0xFFFF)
		}
		return strings.Join(parts, ":"), nil
	}

	return "", fmt.Errorf("unknown IP hex length: %d", len(h))
}

func parseConnState(hexState string) ConnState {
	v, _ := strconv.ParseUint(hexState, 16, 8)
	switch v {
	case 0x01:
		return ConnEstablished
	case 0x02:
		return ConnSynSent
	case 0x03:
		return ConnSynRecv
	case 0x04:
		return ConnFinWait1
	case 0x05:
		return ConnFinWait2
	case 0x06:
		return ConnTimeWait
	case 0x07:
		return ConnClose
	case 0x08:
		return ConnCloseWait
	case 0x09:
		return ConnLastAck
	case 0x0A:
		return ConnListen
	case 0x0B:
		return ConnClosing
	default:
		return ConnState(fmt.Sprintf("UNKNOWN_%02X", v))
	}
}
