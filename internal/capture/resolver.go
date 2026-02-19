package capture

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
)

type Resolver struct {
	client *adb.Client
	log    *slog.Logger
	serial string

	// DNS cache: IP → hostname
	dnsMu    sync.RWMutex
	dnsCache map[string]string
	dnsPend  map[string]struct{} // IPs currently being resolved

	// UID→package cache
	uidMu    sync.RWMutex
	uidCache map[int]string // uid → package name
	uidReady bool

	// Background resolver
	dnsQueue chan string

	// Logcat snooper for DNS/URL intelligence.
	snooper *LogcatSnooper
}

// NewResolver creates a resolver for the given device.
func NewResolver(client *adb.Client, log *slog.Logger, serial string) *Resolver {
	return &Resolver{
		client:   client,
		log:      log.With("component", "resolver", "serial", serial),
		serial:   serial,
		dnsCache: make(map[string]string),
		dnsPend:  make(map[string]struct{}),
		uidCache: make(map[int]string),
		dnsQueue: make(chan string, 256),
		snooper:  NewLogcatSnooper(client, log, serial),
	}
}

// Snooper returns the logcat snooper instance (used by engine for URL captures).
func (r *Resolver) Snooper() *LogcatSnooper {
	return r.snooper
}

// Start begins background resolution workers. Call once.
func (r *Resolver) Start(ctx context.Context) {
	// Load UID → package mapping from device.
	go r.loadUIDMap(ctx)

	// Start DNS resolver workers (3 concurrent lookups).
	for i := 0; i < 3; i++ {
		go r.dnsWorker(ctx)
	}

	// Start logcat snooper for passive DNS + URL capture.
	go func() {
		if err := r.snooper.Run(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn("logcat snooper stopped", "error", err)
		}
	}()

	// Periodically refresh UID map (apps can be installed/uninstalled).
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.loadUIDMap(ctx)
			}
		}
	}()
}

// ResolveHostname returns cached hostname for an IP, or empty string.
// It checks: 1) local cache, 2) logcat DNS snooper, then queues async resolution.
func (r *Resolver) ResolveHostname(ip string) string {
	if ip == "" || ip == "0.0.0.0" || ip == "::" {
		return ""
	}
	// Skip private/local IPs.
	if isPrivateIP(ip) {
		return ""
	}

	r.dnsMu.RLock()
	host, found := r.dnsCache[ip]
	r.dnsMu.RUnlock()

	if found {
		return host
	}

	// Check logcat snooper's DNS cache (populated from device DNS queries).
	if r.snooper != nil {
		if snoopHost := r.snooper.LookupIP(ip); snoopHost != "" {
			// Cache it locally too.
			r.dnsMu.Lock()
			r.dnsCache[ip] = snoopHost
			r.dnsMu.Unlock()
			return snoopHost
		}
	}

	// Queue for async resolution (non-blocking).
	r.dnsMu.Lock()
	if _, pending := r.dnsPend[ip]; !pending {
		r.dnsPend[ip] = struct{}{}
		r.dnsMu.Unlock()
		select {
		case r.dnsQueue <- ip:
		default:
			// Queue full, skip.
		}
	} else {
		r.dnsMu.Unlock()
	}

	return ""
}

// ResolvePackageName returns the app package name for a UID, or empty string.
func (r *Resolver) ResolvePackageName(uid int) string {
	if uid <= 0 {
		return ""
	}

	r.uidMu.RLock()
	pkg := r.uidCache[uid]
	r.uidMu.RUnlock()

	return pkg
}

// dnsWorker processes DNS resolution requests.
func (r *Resolver) dnsWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ip := <-r.dnsQueue:
			host := r.doReverseDNS(ip)

			r.dnsMu.Lock()
			r.dnsCache[ip] = host
			delete(r.dnsPend, ip)
			r.dnsMu.Unlock()
		}
	}
}

// doReverseDNS performs the actual DNS lookup with multiple fallbacks:
// 1. Check logcat snooper cache (again, may have been populated since queueing)
// 2. Go net.LookupAddr (standard reverse DNS)
// 3. Device-side nslookup/host command (device may have cached forward lookup)
func (r *Resolver) doReverseDNS(ip string) string {
	// Check snooper cache once more (may have been populated while queued).
	if r.snooper != nil {
		if host := r.snooper.LookupIP(ip); host != "" {
			return host
		}
	}

	// Standard reverse DNS lookup.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resolver := &net.Resolver{}
	names, err := resolver.LookupAddr(ctx, ip)
	if err == nil && len(names) > 0 {
		host := strings.TrimSuffix(names[0], ".")
		return host
	}

	// Fallback: run nslookup/host on the device itself.
	// The device may have the forward DNS cached.
	if r.snooper != nil {
		nslookupCtx, nslookupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer nslookupCancel()
		if host := r.snooper.DeviceNslookup(nslookupCtx, ip); host != "" {
			return host
		}
	}

	return ""
}

// loadUIDMap loads UID→package name mapping from the device.
func (r *Resolver) loadUIDMap(ctx context.Context) {
	shellCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// `pm list packages -U` outputs: "package:com.example.app uid:10123"
	out, err := r.client.Shell(shellCtx, r.serial, "pm list packages -U 2>/dev/null")
	if err != nil {
		r.log.Debug("failed to get package list", "error", err)
		return
	}

	newMap := make(map[int]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "package:") {
			continue
		}

		// Format: "package:com.example.app uid:10123"
		uidIdx := strings.LastIndex(line, " uid:")
		if uidIdx < 0 {
			continue
		}

		pkg := strings.TrimPrefix(line[:uidIdx], "package:")
		uidStr := strings.TrimPrefix(line[uidIdx:], " uid:")
		uid, err := strconv.Atoi(strings.TrimSpace(uidStr))
		if err != nil {
			continue
		}

		newMap[uid] = pkg
	}

	if len(newMap) > 0 {
		r.uidMu.Lock()
		r.uidCache = newMap
		r.uidReady = true
		r.uidMu.Unlock()
		r.log.Debug("loaded UID map", "packages", len(newMap))
	}
}

// GetDNSCacheSize returns the number of resolved IPs.
func (r *Resolver) GetDNSCacheSize() int {
	r.dnsMu.RLock()
	defer r.dnsMu.RUnlock()
	return len(r.dnsCache)
}

// isPrivateIP checks if an IP is in a private/reserved range.
func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	// RFC1918 + loopback + link-local
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fe80::/10",
		"fc00::/7",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// EnrichPacket adds resolved hostname to a packet (in-place modification not safe, returns copy).
func (r *Resolver) EnrichPacket(pkt *NetworkPacket) {
	if pkt.HTTPHost == "" {
		host := r.ResolveHostname(pkt.DstIP)
		if host != "" {
			pkt.HTTPHost = host
		}
	}
}

// EnrichConnection adds resolved hostname and package name to a connection.
func (r *Resolver) EnrichConnection(conn *Connection) {
	host := r.ResolveHostname(conn.RemoteIP)
	if host != "" {
		conn.Hostname = host
	}
	pkg := r.ResolvePackageName(conn.UID)
	if pkg != "" {
		conn.AppName = pkg
	}
}

// Snapshot returns current DNS + UID cache stats as a formatted string.
func (r *Resolver) Snapshot() string {
	r.dnsMu.RLock()
	dnsSize := len(r.dnsCache)
	r.dnsMu.RUnlock()

	r.uidMu.RLock()
	uidSize := len(r.uidCache)
	r.uidMu.RUnlock()

	return fmt.Sprintf("DNS: %d cached, UID: %d packages", dnsSize, uidSize)
}
