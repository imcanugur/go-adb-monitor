package capture

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
)

const (
	// tcpdumpCmd is the command to stream network packets in text mode with ASCII dump.
	tcpdumpCmd = "tcpdump -i any -n -l -s 256 -q 2>/dev/null"

	// tcpdumpHTTPCmd captures with ASCII dump for HTTP header inspection.
	tcpdumpHTTPCmd = "tcpdump -i any -n -l -s 512 -A 'port 80 or port 443 or port 8080 or port 8443' 2>/dev/null"

	// procNetPollInterval is the interval for polling /proc/net/tcp.
	procNetPollInterval = 2 * time.Second

	// packetChannelBuffer is the buffer size for the per-device packet channel.
	packetChannelBuffer = 512
)

// Engine manages network capture for a single device.
// It selects the best capture mode (tcpdump vs procnet) and streams data.
type Engine struct {
	client   *adb.Client
	log      *slog.Logger
	serial   string
	mode     Mode
	resolver *Resolver

	packetCh chan NetworkPacket
	connCh   chan Connection

	stats atomic.Pointer[CaptureStats]

	mu      sync.Mutex
	stopped bool
}

// NewEngine creates a capture engine for the given device.
func NewEngine(client *adb.Client, log *slog.Logger, serial string, mode Mode) *Engine {
	e := &Engine{
		client:   client,
		log:      log.With("component", "capture", "serial", serial),
		serial:   serial,
		mode:     mode,
		resolver: NewResolver(client, log, serial),
		packetCh: make(chan NetworkPacket, packetChannelBuffer),
		connCh:   make(chan Connection, packetChannelBuffer),
	}
	initialStats := &CaptureStats{Serial: serial, Mode: mode.String()}
	e.stats.Store(initialStats)
	return e
}

// Packets returns the channel that delivers captured packets (tcpdump mode).
func (e *Engine) Packets() <-chan NetworkPacket {
	return e.packetCh
}

// Connections returns the channel that delivers connection snapshots (procnet mode).
func (e *Engine) Connections() <-chan Connection {
	return e.connCh
}

// Stats returns current capture statistics.
func (e *Engine) Stats() CaptureStats {
	return *e.stats.Load()
}

// Run starts the capture engine. Blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	mode := e.mode
	if mode == ModeAuto {
		mode = e.detectMode(ctx)
	}

	s := &CaptureStats{
		Serial:    e.serial,
		Mode:      mode.String(),
		StartedAt: time.Now(),
	}
	e.stats.Store(s)
	e.log.Info("capture engine starting", "mode", mode)

	// Start the resolver for DNS + UID lookups.
	e.resolver.Start(ctx)

	switch mode {
	case ModeTcpdump:
		return e.runTcpdump(ctx)
	case ModeProcNet:
		return e.runProcNet(ctx)
	default:
		return e.runProcNet(ctx) // safe fallback
	}
}

// detectMode checks if tcpdump is available on the device.
func (e *Engine) detectMode(ctx context.Context) Mode {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := e.client.Shell(checkCtx, e.serial, "which tcpdump 2>/dev/null || command -v tcpdump 2>/dev/null")
	if err == nil && strings.TrimSpace(out) != "" {
		e.log.Info("tcpdump available on device", "path", strings.TrimSpace(out))
		return ModeTcpdump
	}

	e.log.Info("tcpdump not available, falling back to /proc/net/tcp")
	return ModeProcNet
}

// runTcpdump streams tcpdump output from the device.
func (e *Engine) runTcpdump(ctx context.Context) error {
	stream, err := e.client.OpenShellStream(ctx, e.serial, tcpdumpCmd)
	if err != nil {
		return fmt.Errorf("opening tcpdump stream: %w", err)
	}
	defer stream.Close()

	parser := NewTcpdumpParser(e.serial)
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 4096), 64*1024)

	done := ctx.Done()

	for scanner.Scan() {
		select {
		case <-done:
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		pkt := parser.ParseLine(line)
		if pkt == nil {
			continue
		}

		// Update stats.
		s := e.Stats()
		s.PacketCount++
		s.LastActivity = time.Now()
		e.stats.Store(&s)

		select {
		case e.packetCh <- *pkt:
		default:
			// Channel full, drop packet to avoid blocking.
			s2 := e.Stats()
			s2.Errors++
			e.stats.Store(&s2)
		}
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("reading tcpdump: %w", err)
	}

	return nil
}

// runProcNet periodically reads /proc/net/tcp to track connections.
func (e *Engine) runProcNet(ctx context.Context) error {
	parser := NewProcNetParser(e.serial)
	ticker := time.NewTicker(procNetPollInterval)
	defer ticker.Stop()

	// Known connections for diffing.
	known := make(map[string]Connection)

	// Read immediately, then on interval.
	e.readAndDiffProcNet(ctx, parser, known)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			e.readAndDiffProcNet(ctx, parser, known)
		}
	}
}

func (e *Engine) readAndDiffProcNet(ctx context.Context, parser *ProcNetParser, known map[string]Connection) {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var conns []Connection

	// Read TCP connections.
	tcpOut, err := e.client.Shell(readCtx, e.serial, "cat /proc/net/tcp 2>/dev/null")
	if err != nil {
		e.log.Debug("failed to read /proc/net/tcp", "error", err)
		return
	}
	conns = append(conns, parser.ParseProcNet(tcpOut, ProtoTCP)...)

	// Read TCP6 connections.
	tcp6Out, err := e.client.Shell(readCtx, e.serial, "cat /proc/net/tcp6 2>/dev/null")
	if err == nil {
		conns = append(conns, parser.ParseProcNet(tcp6Out, ProtoTCP)...)
	}

	// Read UDP connections.
	udpOut, err := e.client.Shell(readCtx, e.serial, "cat /proc/net/udp 2>/dev/null")
	if err == nil {
		conns = append(conns, parser.ParseProcNet(udpOut, ProtoUDP)...)
	}

	// Read UDP6 connections.
	udp6Out, err := e.client.Shell(readCtx, e.serial, "cat /proc/net/udp6 2>/dev/null")
	if err == nil {
		conns = append(conns, parser.ParseProcNet(udp6Out, ProtoUDP)...)
	}

	// Diff to find new/changed connections.
	now := time.Now()
	seen := make(map[string]struct{}, len(conns))

	for _, c := range conns {
		key := connKey(c)
		seen[key] = struct{}{}

		if prev, exists := known[key]; exists {
			c.FirstSeen = prev.FirstSeen
			c.LastSeen = now
			known[key] = c
			continue
		}

		// New connection — enrich and emit on both channels.
		c.FirstSeen = now
		c.LastSeen = now
		e.resolver.EnrichConnection(&c)
		known[key] = c

		s := e.Stats()
		s.ConnCount++
		s.PacketCount++
		s.LastActivity = now
		e.stats.Store(&s)

		select {
		case e.connCh <- c:
		default:
		}

		// Also emit as a NetworkPacket so the Packets tab has data.
		pkt := connToPacket(c)
		select {
		case e.packetCh <- pkt:
		default:
		}
	}

	// Remove stale connections.
	for key := range known {
		if _, ok := seen[key]; !ok {
			delete(known, key)
		}
	}
}

func connKey(c Connection) string {
	return fmt.Sprintf("%s:%d->%s:%d/%s",
		c.LocalIP, c.LocalPort, c.RemoteIP, c.RemotePort, c.State)
}

// connToPacket converts a Connection to a NetworkPacket for the Packets tab.
// Note: procnet data has no HTTP layer — we only set network-level fields.
func connToPacket(c Connection) NetworkPacket {
	host := c.Hostname // resolved by DNS if available

	return NetworkPacket{
		ID:        c.ID + "-pkt",
		Serial:    c.Serial,
		Timestamp: c.FirstSeen,
		SrcIP:     c.LocalIP,
		SrcPort:   c.LocalPort,
		DstIP:     c.RemoteIP,
		DstPort:   c.RemotePort,
		Protocol:  c.Protocol,
		Flags:     string(c.State),
		HTTPHost:  host,
		Raw:       fmt.Sprintf("%s %s:%d -> %s:%d [%s]", c.Protocol, c.LocalIP, c.LocalPort, c.RemoteIP, c.RemotePort, c.State),
	}
}
