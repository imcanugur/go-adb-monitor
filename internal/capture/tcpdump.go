package capture

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// tcpdump -i any -n -l -s 256 -q output format:
// 12:34:56.789012 IP 10.0.0.1.12345 > 93.184.216.34.80: tcp 100
// 12:34:56.789012 IP 10.0.0.1.12345 > 8.8.8.8.53: UDP, length 40
// 12:34:56.789012 IP6 ::1.12345 > ::1.80: tcp 100

// With -A (ASCII dump), HTTP headers follow:
// GET /api/users HTTP/1.1
// Host: example.com

var (
	// Matches: HH:MM:SS.ffffff IP src.port > dst.port: proto info
	rePacketLine = regexp.MustCompile(
		`^(\d{2}:\d{2}:\d{2}\.\d+)\s+` + // timestamp
			`(IP6?)\s+` + // IP version
			`(\S+)\.(\d+)\s+>\s+` + // src.port
			`(\S+)\.(\d+):\s+` + // dst.port:
			`(.+)$`) // rest (protocol, flags, length)

	reHTTPRequest  = regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|CONNECT)\s+(\S+)\s+HTTP/`)
	reHTTPResponse = regexp.MustCompile(`^HTTP/[\d.]+\s+(\d{3})`)
	reHTTPHost     = regexp.MustCompile(`(?i)^Host:\s*(\S+)`)
)

// TcpdumpParser parses tcpdump text output into NetworkPacket structs.
type TcpdumpParser struct {
	serial string
	nextID uint64
}

// NewTcpdumpParser creates a parser for the given device serial.
func NewTcpdumpParser(serial string) *TcpdumpParser {
	return &TcpdumpParser{serial: serial}
}

// ParseLine parses a single line of tcpdump output.
// Returns nil if the line doesn't match the expected format.
func (p *TcpdumpParser) ParseLine(line string) *NetworkPacket {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	m := rePacketLine.FindStringSubmatch(line)
	if m == nil {
		return nil
	}

	ts := p.parseTimestamp(m[1])
	srcIP := m[3]
	srcPort := p.parsePort(m[4])
	dstIP := m[5]
	dstPort := p.parsePort(m[6])
	rest := m[7]

	proto := p.parseProtocol(rest)
	length := p.parseLength(rest)
	flags := p.parseFlags(rest)

	p.nextID++
	pkt := &NetworkPacket{
		ID:        fmt.Sprintf("%s-%d", p.serial, p.nextID),
		Serial:    p.serial,
		Timestamp: ts,
		SrcIP:     srcIP,
		SrcPort:   srcPort,
		DstIP:     dstIP,
		DstPort:   dstPort,
		Protocol:  proto,
		Length:    length,
		Flags:     flags,
		Raw:       line,
	}

	return pkt
}

// EnrichWithHTTP checks for HTTP content in subsequent lines after a packet header.
// Call this with lines that follow a packet line (the ASCII dump from -A mode).
func (p *TcpdumpParser) EnrichWithHTTP(pkt *NetworkPacket, line string) {
	if pkt == nil {
		return
	}
	line = strings.TrimSpace(line)

	if m := reHTTPRequest.FindStringSubmatch(line); m != nil {
		pkt.HTTPMethod = m[1]
		pkt.HTTPPath = m[2]
		return
	}

	if m := reHTTPResponse.FindStringSubmatch(line); m != nil {
		status, _ := strconv.Atoi(m[1])
		pkt.HTTPStatus = status
		return
	}

	if m := reHTTPHost.FindStringSubmatch(line); m != nil {
		pkt.HTTPHost = m[1]
		return
	}
}

// ParseStream reads lines from a scanner and sends parsed packets to the output channel.
// It handles both packet header lines and HTTP enrichment from ASCII dumps.
func (p *TcpdumpParser) ParseStream(scanner *bufio.Scanner, out chan<- NetworkPacket, done <-chan struct{}) {
	var currentPkt *NetworkPacket

	for scanner.Scan() {
		select {
		case <-done:
			return
		default:
		}

		line := scanner.Text()
		pkt := p.ParseLine(line)

		if pkt != nil {
			// Emit the previous packet before starting a new one.
			if currentPkt != nil {
				select {
				case out <- *currentPkt:
				case <-done:
					return
				}
			}
			currentPkt = pkt
		} else if currentPkt != nil {
			// This is an ASCII dump line; try HTTP enrichment.
			p.EnrichWithHTTP(currentPkt, line)
		}
	}

	// Emit the last packet.
	if currentPkt != nil {
		select {
		case out <- *currentPkt:
		case <-done:
		}
	}
}

func (p *TcpdumpParser) parseTimestamp(s string) time.Time {
	now := time.Now()
	t, err := time.Parse("15:04:05.000000", s)
	if err != nil {
		// Try shorter format.
		t, err = time.Parse("15:04:05.000", s)
		if err != nil {
			return now
		}
	}
	return time.Date(now.Year(), now.Month(), now.Day(),
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), now.Location())
}

func (p *TcpdumpParser) parsePort(s string) uint16 {
	n, _ := strconv.ParseUint(s, 10, 16)
	return uint16(n)
}

func (p *TcpdumpParser) parseProtocol(rest string) Protocol {
	lower := strings.ToLower(rest)
	if strings.Contains(lower, "udp") {
		return ProtoUDP
	}
	if strings.Contains(lower, "icmp") {
		return ProtoICMP
	}
	return ProtoTCP
}

func (p *TcpdumpParser) parseLength(rest string) int {
	// Look for "length N" or "tcp N" patterns.
	parts := strings.Fields(rest)
	for i, part := range parts {
		if part == "length" && i+1 < len(parts) {
			n, _ := strconv.Atoi(parts[i+1])
			return n
		}
	}
	// tcpdump: "tcp 100" at end
	if len(parts) >= 2 {
		n, err := strconv.Atoi(parts[len(parts)-1])
		if err == nil {
			return n
		}
	}
	return 0
}

func (p *TcpdumpParser) parseFlags(rest string) string {
	// Extract TCP flags like [S], [P.], [F.], [R.]
	idx := strings.Index(rest, "Flags [")
	if idx == -1 {
		return ""
	}
	end := strings.Index(rest[idx:], "]")
	if end == -1 {
		return ""
	}
	return rest[idx+7 : idx+end]
}
