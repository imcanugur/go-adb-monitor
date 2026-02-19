package capture

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imcanugur/go-adb-monitor/internal/adb"
)

// LogcatSnooper streams device logcat and extracts
type LogcatSnooper struct {
	client *adb.Client
	log    *slog.Logger
	serial string

	// DNS domain→IP map (populated from logcat DNS events)
	dnsMu    sync.RWMutex
	dnsMap   map[string]string // domain → IP
	ipMap    map[string]string // IP → domain (reverse index)

	// Captured URLs from logcat
	urlCh chan URLCapture

	// Stats
	dnsHits  atomic.Int64
	urlHits  atomic.Int64
	linesRead atomic.Int64
}

// URLCapture represents a URL found in logcat output.
type URLCapture struct {
	Timestamp time.Time
	Tag       string // logcat tag (OkHttp, Retrofit, etc.)
	Method    string // GET, POST, etc.
	URL       string // full URL
	AppPkg    string // package name if available
}

// logcat command: stream all tags that commonly log network/DNS/HTTP activity.
// -v threadtime gives timestamp, priority, tag, PID/TID, message.
const logcatCmd = `logcat -v brief -s \
DnsResolver:* \
netd:* \
NetworkMonitor:* \
OkHttp:* \
Retrofit:* \
Volley:* \
HttpEngine:* \
chromium:* \
System.out:* \
ConnectivityService:* \
NetworkSecurityConfig:* \
NativeCrypto:* \
conscrypt:* \
HttpURLConnection:* \
2>/dev/null`

// Regex patterns for extracting DNS and URL information.
var (
	// DNS resolution patterns (varies by Android version)
	// "DnsResolver: DNS query for example.com returned 1.2.3.4"
	// "netd: resolv_cache_lookup: name = example.com"
	reDNSQuery  = regexp.MustCompile(`(?i)(?:dns|resolv|lookup|query|resolved?).*?(?:for|name\s*=)\s*([a-zA-Z0-9][-a-zA-Z0-9.]*\.[a-zA-Z]{2,})`)
	reDNSResult = regexp.MustCompile(`(?:returned?|result|answer|->|=)\s*((?:\d{1,3}\.){3}\d{1,3})`)

	// URL patterns — capture full URL with method if present
	reHTTPURL = regexp.MustCompile(`((?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\s+)?(https?://[^\s"'<>{}\x00-\x1f]+)`)

	// OkHttp specific: "--> POST https://api.example.com/path"
	reOkHTTP = regexp.MustCompile(`-->\s+(GET|POST|PUT|DELETE|PATCH|HEAD)\s+(https?://[^\s]+)`)

	// Retrofit: "@GET("/path")" or actual request logs
	reRetrofit = regexp.MustCompile(`(?:@(?:GET|POST|PUT|DELETE|PATCH)\s*\(\s*"([^"]+)"\s*\))`)

	// "Connecting to host:port" patterns
	reConnecting = regexp.MustCompile(`(?i)connect(?:ing|ed)?\s+(?:to\s+)?([a-zA-Z0-9][-a-zA-Z0-9.]*\.[a-zA-Z]{2,})(?::(\d+))?`)

	// IP address pattern
	reIPAddr = regexp.MustCompile(`((?:\d{1,3}\.){3}\d{1,3})`)
)

// NewLogcatSnooper creates a new logcat snooper for a device.
func NewLogcatSnooper(client *adb.Client, log *slog.Logger, serial string) *LogcatSnooper {
	return &LogcatSnooper{
		client: client,
		log:    log.With("component", "logcat-snooper", "serial", serial),
		serial: serial,
		dnsMap: make(map[string]string),
		ipMap:  make(map[string]string),
		urlCh:  make(chan URLCapture, 256),
	}
}

// URLs returns the channel that delivers captured URLs from logcat.
func (s *LogcatSnooper) URLs() <-chan URLCapture {
	return s.urlCh
}

// LookupIP returns the domain name for an IP address from the DNS cache.
func (s *LogcatSnooper) LookupIP(ip string) string {
	s.dnsMu.RLock()
	defer s.dnsMu.RUnlock()
	return s.ipMap[ip]
}

// LookupDomain returns the IP for a domain from the DNS cache.
func (s *LogcatSnooper) LookupDomain(domain string) string {
	s.dnsMu.RLock()
	defer s.dnsMu.RUnlock()
	return s.dnsMap[domain]
}

// Stats returns snooper statistics.
func (s *LogcatSnooper) Stats() (dnsHits, urlHits, lines int64) {
	return s.dnsHits.Load(), s.urlHits.Load(), s.linesRead.Load()
}

// Run starts streaming logcat. Blocks until ctx is cancelled.
func (s *LogcatSnooper) Run(ctx context.Context) error {
	// First, flush old logcat content to avoid replaying stale data.
	flushCtx, flushCancel := context.WithTimeout(ctx, 3*time.Second)
	_, _ = s.client.Shell(flushCtx, s.serial, "logcat -c 2>/dev/null")
	flushCancel()

	// Also do an initial DNS cache dump from the device.
	go s.loadDeviceDNSCache(ctx)

	stream, err := s.client.OpenShellStream(ctx, s.serial, logcatCmd)
	if err != nil {
		return fmt.Errorf("opening logcat stream: %w", err)
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 4096), 64*1024)

	s.log.Info("logcat snooper started")

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		s.linesRead.Add(1)
		s.parseLine(line)
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("reading logcat: %w", err)
	}

	return nil
}

// parseLine extracts DNS and URL information from a logcat line.
func (s *LogcatSnooper) parseLine(line string) {
	if len(line) < 5 {
		return
	}

	// Extract tag from logcat brief format: "I/TagName( 1234): message"
	tag := ""
	msgStart := strings.Index(line, "): ")
	if msgStart > 0 {
		tagStart := strings.Index(line, "/")
		if tagStart >= 0 && tagStart < msgStart {
			parenIdx := strings.Index(line[tagStart:], "(")
			if parenIdx > 0 {
				tag = strings.TrimSpace(line[tagStart+1 : tagStart+parenIdx])
			}
		}
		line = line[msgStart+3:]
	}

	// Try to parse DNS information.
	s.parseDNS(line, tag)

	// Try to parse HTTP URLs.
	s.parseURLs(line, tag)
}

// parseDNS extracts domain→IP mappings from DNS-related log lines.
func (s *LogcatSnooper) parseDNS(line, tag string) {
	// Check if line looks DNS-related.
	lower := strings.ToLower(line)
	isDNS := tag == "DnsResolver" || tag == "netd" || tag == "NetworkMonitor" ||
		strings.Contains(lower, "dns") ||
		strings.Contains(lower, "resolv") ||
		strings.Contains(lower, "lookup")

	if !isDNS {
		return
	}

	// Try to extract domain name.
	domainMatch := reDNSQuery.FindStringSubmatch(line)
	if domainMatch == nil {
		return
	}
	domain := strings.ToLower(domainMatch[1])

	// Try to extract resulting IP.
	ipMatch := reDNSResult.FindStringSubmatch(line)
	if ipMatch == nil {
		// Also look for any IP address in the line.
		ipMatch = reIPAddr.FindStringSubmatch(line)
	}

	if ipMatch != nil {
		ip := ipMatch[1]
		if net.ParseIP(ip) != nil && !isPrivateIP(ip) {
			s.addDNSMapping(domain, ip)
		}
	}
}

// parseURLs extracts HTTP/HTTPS URLs from logcat lines.
func (s *LogcatSnooper) parseURLs(line, tag string) {
	// OkHttp specific format: "--> POST https://..."
	if matches := reOkHTTP.FindStringSubmatch(line); matches != nil {
		s.emitURL(tag, matches[1], matches[2])
		return
	}

	// General HTTP URL pattern.
	if matches := reHTTPURL.FindStringSubmatch(line); matches != nil {
		method := strings.TrimSpace(matches[1])
		url := matches[2]
		// Skip if it's just a comment or documentation.
		if strings.Contains(url, "schemas.android.com") ||
			strings.Contains(url, "www.w3.org") ||
			strings.Contains(url, "schemas.xmlsoap.org") ||
			strings.Contains(url, "xmlns") {
			return
		}
		s.emitURL(tag, method, url)
		return
	}

	// "Connecting to" patterns — extract domain name.
	if matches := reConnecting.FindStringSubmatch(line); matches != nil {
		domain := strings.ToLower(matches[1])
		// Queue domain for forward lookup.
		s.addDNSMapping(domain, "")
	}
}

// addDNSMapping stores a domain→IP mapping.
func (s *LogcatSnooper) addDNSMapping(domain, ip string) {
	// Validate domain.
	if len(domain) < 4 || !strings.Contains(domain, ".") {
		return
	}
	// Skip local/invalid domains.
	if strings.HasSuffix(domain, ".local") || strings.HasSuffix(domain, ".internal") {
		return
	}

	s.dnsMu.Lock()
	defer s.dnsMu.Unlock()

	if ip != "" {
		s.dnsMap[domain] = ip
		// Only set IP→domain if not already set (first domain wins).
		if _, exists := s.ipMap[ip]; !exists {
			s.ipMap[ip] = domain
			s.dnsHits.Add(1)
			s.log.Debug("DNS mapping", "domain", domain, "ip", ip)
		}
	} else if _, exists := s.dnsMap[domain]; !exists {
		// Domain without IP — try to resolve it.
		go s.forwardResolve(domain)
	}
}

// forwardResolve does a DNS lookup for a domain and stores the result.
func (s *LogcatSnooper) forwardResolve(domain string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupHost(ctx, domain)
	if err != nil || len(ips) == 0 {
		return
	}

	s.dnsMu.Lock()
	defer s.dnsMu.Unlock()

	for _, ip := range ips {
		if net.ParseIP(ip) != nil && !isPrivateIP(ip) {
			s.dnsMap[domain] = ip
			if _, exists := s.ipMap[ip]; !exists {
				s.ipMap[ip] = domain
				s.dnsHits.Add(1)
			}
			break
		}
	}
}

// emitURL sends a captured URL to the channel.
func (s *LogcatSnooper) emitURL(tag, method, rawURL string) {
	s.urlHits.Add(1)

	// Also extract domain→IP mapping from URL.
	host := extractHostFromURL(rawURL)
	if host != "" {
		s.addDNSMapping(host, "")
	}

	cap := URLCapture{
		Timestamp: time.Now(),
		Tag:       tag,
		Method:    method,
		URL:       rawURL,
	}

	select {
	case s.urlCh <- cap:
	default:
		// Channel full, drop.
	}
}

// loadDeviceDNSCache reads the device's dumpsys DNS cache and netd cache.
func (s *LogcatSnooper) loadDeviceDNSCache(ctx context.Context) {
	shellCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Try dumpsys dnsresolver (Android 10+).
	out, err := s.client.Shell(shellCtx, s.serial, "dumpsys dnsresolver 2>/dev/null")
	if err == nil && len(out) > 100 {
		s.parseDumpsysDNS(out)
	}

	// Also try getprop to find the device's DNS servers.
	out2, err := s.client.Shell(shellCtx, s.serial, "getprop net.dns1 2>/dev/null && getprop net.dns2 2>/dev/null")
	if err == nil {
		s.log.Debug("device DNS servers", "output", strings.TrimSpace(out2))
	}
}

// parseDumpsysDNS parses `dumpsys dnsresolver` output for cached DNS entries.
func (s *LogcatSnooper) parseDumpsysDNS(output string) {
	// Look for cache entries like:
	// "example.com IN A 1.2.3.4 TTL=300"
	// or hostname-to-IP mappings in the cache dump.
	reEntry := regexp.MustCompile(`([a-zA-Z0-9][-a-zA-Z0-9.]*\.[a-zA-Z]{2,})\s+.*?(?:IN\s+A|AAAA?)\s+((?:\d{1,3}\.){3}\d{1,3})`)

	for _, line := range strings.Split(output, "\n") {
		if matches := reEntry.FindStringSubmatch(line); matches != nil {
			domain := strings.ToLower(matches[1])
			ip := matches[2]
			if net.ParseIP(ip) != nil && !isPrivateIP(ip) {
				s.addDNSMapping(domain, ip)
			}
		}
	}

	s.log.Debug("parsed dumpsys dnsresolver", "dns_entries", s.dnsHits.Load())
}

// DeviceNslookup runs nslookup on the device for an IP that failed reverse DNS.
func (s *LogcatSnooper) DeviceNslookup(ctx context.Context, ip string) string {
	shellCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Try nslookup on the device.
	out, err := s.client.Shell(shellCtx, s.serial, fmt.Sprintf("nslookup %s 2>/dev/null || host %s 2>/dev/null", ip, ip))
	if err != nil || out == "" {
		return ""
	}

	// Parse nslookup output: "Name: example.com" or "1.2.3.4.in-addr.arpa domain name pointer example.com."
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)

		// nslookup format: "Name:   example.com" (after Address line)
		if strings.HasPrefix(line, "Name:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
			name = strings.TrimSuffix(name, ".")
			if len(name) > 3 && strings.Contains(name, ".") && !strings.HasPrefix(name, "in-addr") {
				s.addDNSMapping(name, ip)
				return name
			}
		}

		// host command format: "4.78.111.193.in-addr.arpa domain name pointer example.com."
		if strings.Contains(line, "domain name pointer") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				name := strings.TrimSuffix(parts[len(parts)-1], ".")
				if len(name) > 3 && strings.Contains(name, ".") {
					s.addDNSMapping(name, ip)
					return name
				}
			}
		}
	}

	return ""
}

// extractHostFromURL extracts the hostname from a URL string.
func extractHostFromURL(rawURL string) string {
	// Quick extraction without URL parsing.
	after := rawURL
	if idx := strings.Index(after, "://"); idx >= 0 {
		after = after[idx+3:]
	}
	// Remove path.
	if idx := strings.IndexByte(after, '/'); idx >= 0 {
		after = after[:idx]
	}
	// Remove port.
	if idx := strings.LastIndexByte(after, ':'); idx >= 0 {
		after = after[:idx]
	}
	// Remove userinfo.
	if idx := strings.IndexByte(after, '@'); idx >= 0 {
		after = after[idx+1:]
	}
	return strings.ToLower(after)
}
